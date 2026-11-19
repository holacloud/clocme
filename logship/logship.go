// Package logship sends the service logs to instantlogs.io (or any
// compatible ingest endpoint) in addition to stdout/stderr.
//
// The protocol is the one of the instantlogs client: a POST of plain text
// lines to /v1/loggers/{logger_id}/ingest with Api-Key / Api-Secret headers.
// The client itself lives in a main package and cannot be imported, so this
// is a small reimplementation:
//
//   - Write never blocks: each log entry goes to a buffered channel of
//     QueueSize entries. When it is full the OLDEST entry is dropped to make
//     room (like the instantlogs client) and the drop is reported once
//     delivery resumes.
//   - A single goroutine drains the channel into a reused buffer and sends
//     batches (up to 1 MB) every second, immediately again while there is
//     backlog.
//   - When the endpoint fails, the batch is kept and retried with backoff.
//     If the queue fills up meanwhile, the pending batch is the oldest data
//     and is dropped first.
//   - logship never logs through the standard logger (it would feed itself),
//     its own errors go straight to stderr.
package logship

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type Config struct {
	LoggerID  string `json:"logger_id" usage:"instantlogs logger id; empty disables log shipping"`
	ApiKey    string `json:"api_key" usage:"instantlogs Api-Key"`
	ApiSecret string `json:"api_secret" usage:"instantlogs Api-Secret"`
	Endpoint  string `json:"endpoint" usage:"Ingest URL (default https://instantlogs.io/v1/loggers/{logger_id}/ingest)"`
}

func (c Config) Enabled() bool { return c.LoggerID != "" || c.Endpoint != "" }

const (
	// QueueSize is the number of log entries kept while the endpoint is slow
	// or down. The channel slots (16 bytes each, 1.6 MB) only become resident
	// RAM as they are used; the strings themselves are the log lines.
	QueueSize = 100_000

	maxBatchBytes   = 1 << 20
	defaultInterval = time.Second
	maxBackoff      = 30 * time.Second
)

type Shipper struct {
	endpoint  string
	apiKey    string
	apiSecret string
	client    *http.Client
	interval  time.Duration
	errOut    io.Writer

	lines   chan string
	dropped atomic.Int64 // entries dropped and not reported yet

	// owned by the sender goroutine (and by Close after it stopped)
	body         bytes.Buffer // reused batch buffer
	pending      []byte       // batch that failed to be sent, retried as is
	pendingLines int          // log entries in pending (the drop notice excluded)

	stop    context.CancelFunc
	stopped chan struct{}
}

// New returns a running shipper, or nil if c is not enabled. A nil *Shipper
// is a valid no-op writer.
func New(c Config) *Shipper {
	if !c.Enabled() {
		return nil
	}
	s := newShipper(c, QueueSize)
	s.start()
	return s
}

func newShipper(c Config, queueSize int) *Shipper {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://instantlogs.io/v1/loggers/" + c.LoggerID + "/ingest"
	}
	return &Shipper{
		endpoint:  endpoint,
		apiKey:    c.ApiKey,
		apiSecret: c.ApiSecret,
		client:    &http.Client{Timeout: 10 * time.Second},
		interval:  defaultInterval,
		errOut:    os.Stderr,
		lines:     make(chan string, queueSize),
		stopped:   make(chan struct{}),
	}
}

func (s *Shipper) start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	go s.loop(ctx)
}

// Write queues one log entry (the log package writes whole entries). It never
// blocks and never fails: when the queue is full the oldest entry is dropped.
func (s *Shipper) Write(p []byte) (int, error) {
	if s == nil {
		return len(p), nil
	}
	line := string(p) // copy: the log package reuses p
	for {
		select {
		case s.lines <- line:
			return len(p), nil
		default:
			select { // full: make room by dropping the oldest entry
			case <-s.lines:
				s.dropped.Add(1)
			default: // the sender just made room
			}
		}
	}
}

func (s *Shipper) loop(ctx context.Context) {
	defer close(s.stopped)
	backoff := s.interval
	timer := time.NewTimer(s.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		more, err := s.flush(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(s.errOut, "logship: %v (retrying in %s)\n", err, backoff)
			timer.Reset(backoff)
			backoff = min(backoff*2, maxBackoff)
		case more:
			backoff = s.interval
			timer.Reset(0) // backlog: send the next batch right away
		default:
			backoff = s.interval
			timer.Reset(s.interval)
		}
	}
}

// flush sends one batch: the pending one if a previous attempt failed,
// otherwise a new one drained from the queue. more reports that the queue
// still has entries.
func (s *Shipper) flush(ctx context.Context) (more bool, err error) {
	if s.pending != nil && len(s.lines) == cap(s.lines) {
		// the queue filled up while retrying: the pending batch is the
		// oldest data, drop it in favor of the newer entries
		s.dropped.Add(int64(s.pendingLines))
		s.pending, s.pendingLines = nil, 0
	}

	if s.pending == nil {
		s.body.Reset()
		if d := s.dropped.Swap(0); d > 0 {
			fmt.Fprintf(&s.body, "logship: dropped %d old log lines while %s was unreachable\n", d, s.endpoint)
		}
		n := 0
	drain:
		for s.body.Len() < maxBatchBytes {
			select {
			case line := <-s.lines:
				s.body.WriteString(line)
				n++
			default:
				break drain
			}
		}
		if s.body.Len() == 0 {
			return false, nil
		}
		s.pending, s.pendingLines = s.body.Bytes(), n
	}

	if err := s.send(ctx, s.pending); err != nil {
		return false, err
	}
	s.pending, s.pendingLines = nil, 0
	return len(s.lines) > 0, nil
}

func (s *Shipper) send(ctx context.Context, batch []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(batch))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if s.apiKey != "" || s.apiSecret != "" {
		req.Header.Set("Api-Key", s.apiKey)
		req.Header.Set("Api-Secret", s.apiSecret)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("ingest: status %d", resp.StatusCode)
	}
	return nil
}

// Close stops the background loop and tries to deliver what is queued,
// waiting at most timeout.
func (s *Shipper) Close(timeout time.Duration) {
	if s == nil {
		return
	}
	s.stop()
	<-s.stopped // from here on this goroutine owns body/pending
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		more, err := s.flush(ctx)
		if err != nil {
			fmt.Fprintf(s.errOut, "logship: final flush: %v\n", err)
			return
		}
		if !more {
			return
		}
	}
}
