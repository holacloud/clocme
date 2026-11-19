// Package logship sends the service logs to instantlogs.io (or any
// compatible ingest endpoint) in addition to stdout/stderr.
//
// The protocol is the one of the instantlogs client: a POST of plain text
// lines to /v1/loggers/{logger_id}/ingest with Api-Key / Api-Secret headers.
// The client itself lives in a main package and cannot be imported, so this
// is a small reimplementation with different trade-offs:
//
//   - Write never blocks: lines go to a bounded in-memory buffer and are
//     sent in batches every second by a background goroutine.
//   - When the endpoint is down, batches are kept and retried with backoff;
//     if the buffer fills up, the OLDEST lines are dropped to make room for
//     the new ones (like the instantlogs client), and the drop is reported
//     once delivery resumes. The service is never slowed down.
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
	"sync"
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
	defaultMaxBuffer = 1 << 20 // bytes kept in memory while the endpoint is slow or down
	defaultInterval  = time.Second
	maxBackoff       = 30 * time.Second
)

type Shipper struct {
	endpoint  string
	apiKey    string
	apiSecret string
	client    *http.Client
	interval  time.Duration
	maxBuffer int

	mu      sync.Mutex
	buf     bytes.Buffer // lines waiting to be sent
	dropped int          // lines dropped since the last successful send
	stopped chan struct{}
	stop    context.CancelFunc
	errOut  io.Writer
}

// New returns a running shipper, or nil if c is not enabled. A nil *Shipper
// is a valid no-op writer.
func New(c Config) *Shipper {
	if !c.Enabled() {
		return nil
	}
	s := newShipper(c)
	s.start()
	return s
}

func newShipper(c Config) *Shipper {
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
		maxBuffer: defaultMaxBuffer,
		stopped:   make(chan struct{}),
		errOut:    os.Stderr,
	}
}

func (s *Shipper) start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	go s.loop(ctx)
}

// Write buffers p (whole log lines, as written by the log package). It never
// blocks on the network and never fails.
func (s *Shipper) Write(p []byte) (int, error) {
	if s == nil {
		return len(p), nil
	}
	s.mu.Lock()
	s.buf.Write(p)
	s.trimOldest()
	s.mu.Unlock()
	return len(p), nil
}

// trimOldest drops whole lines from the front of the buffer until it fits in
// maxBuffer. Must be called with s.mu held.
func (s *Shipper) trimOldest() {
	excess := s.buf.Len() - s.maxBuffer
	if excess <= 0 {
		return
	}
	data := s.buf.Bytes()
	cut := len(data) // no line boundary after excess: drop everything
	if i := bytes.IndexByte(data[excess-1:], '\n'); i >= 0 {
		cut = excess + i // keep from the line that starts after the boundary
	}
	s.dropped += max(1, bytes.Count(data[:cut], []byte{'\n'}))
	s.buf.Next(cut)
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
		if err := s.flush(ctx); err != nil {
			fmt.Fprintf(s.errOut, "logship: %v (retrying in %s)\n", err, backoff)
			timer.Reset(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = s.interval
		timer.Reset(s.interval)
	}
}

// flush sends everything buffered in one request. On failure the batch is
// put back in front of the buffer to be retried; if together with the newer
// lines it does not fit, its oldest lines are the ones dropped.
func (s *Shipper) flush(ctx context.Context) error {
	s.mu.Lock()
	if s.buf.Len() == 0 && s.dropped == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := make([]byte, 0, s.buf.Len()+64)
	if s.dropped > 0 {
		batch = fmt.Appendf(batch, "logship: dropped %d old log lines while %s was unreachable\n", s.dropped, s.endpoint)
	}
	batch = append(batch, s.buf.Bytes()...)
	dropped := s.dropped
	s.buf.Reset()
	s.dropped = 0
	s.mu.Unlock()

	err := s.send(ctx, batch)
	if err == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if dropped > 0 { // the drop notice is regenerated on the next try
		batch = batch[bytes.IndexByte(batch, '\n')+1:]
	}
	// put it back in front, newer lines after it, then trim the oldest
	rest := append([]byte(nil), s.buf.Bytes()...)
	s.buf.Reset()
	s.buf.Write(batch)
	s.buf.Write(rest)
	s.dropped += dropped
	s.trimOldest()
	return err
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

// Close stops the background loop and makes a last attempt to deliver what
// is buffered, waiting at most timeout.
func (s *Shipper) Close(timeout time.Duration) {
	if s == nil {
		return
	}
	s.stop()
	<-s.stopped
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.flush(ctx); err != nil {
		fmt.Fprintf(s.errOut, "logship: final flush: %v\n", err)
	}
}
