package logship

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ingest struct {
	mu      sync.Mutex
	bodies  []string
	headers http.Header
	fail    atomic.Bool
	hang    chan struct{}
}

func (i *ingest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if i.hang != nil {
		<-i.hang
	}
	if i.fail.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	b, _ := io.ReadAll(r.Body)
	i.mu.Lock()
	i.bodies = append(i.bodies, string(b))
	i.headers = r.Header.Clone()
	i.mu.Unlock()
	w.Write([]byte(`{"n":1}`))
}

func (i *ingest) all() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return strings.Join(i.bodies, "")
}

// newTestShipper returns a fast, quiet shipper; call start() after tweaking it.
func newTestShipper(url string) *Shipper {
	s := newShipper(Config{Endpoint: url, ApiKey: "k", ApiSecret: "s"}, 1000)
	s.interval = 10 * time.Millisecond
	s.logger = slog.New(slog.DiscardHandler)
	return s
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timeout")
}

func TestDisabled(t *testing.T) {
	var s *Shipper = New(Config{})
	if s != nil {
		t.Fatal("expected nil shipper")
	}
	if n, err := s.Write([]byte("x\n")); n != 2 || err != nil {
		t.Fatal("nil shipper must be a no-op writer")
	}
	s.Close(time.Second)
}

func TestDefaultEndpoint(t *testing.T) {
	s := New(Config{LoggerID: "abc"})
	defer s.Close(0)
	if s.endpoint != "https://instantlogs.io/v1/loggers/abc/ingest" {
		t.Fatal(s.endpoint)
	}
}

func TestShipsBatchesWithCredentials(t *testing.T) {
	in := &ingest{}
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newTestShipper(srv.URL)
	s.start()

	s.Write([]byte("one\n"))
	s.Write([]byte("two\n"))
	waitFor(t, func() bool { return in.all() == "one\ntwo\n" })
	if in.headers.Get("Api-Key") != "k" || in.headers.Get("Api-Secret") != "s" {
		t.Fatalf("headers %v", in.headers)
	}

	s.Write([]byte("three\n"))
	s.Close(time.Second)
	if in.all() != "one\ntwo\nthree\n" {
		t.Fatalf("got %q", in.all())
	}
}

func TestRetriesWhenEndpointFails(t *testing.T) {
	in := &ingest{}
	in.fail.Store(true)
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newTestShipper(srv.URL)
	s.start()
	defer s.Close(time.Second)

	s.Write([]byte("a\n"))
	time.Sleep(50 * time.Millisecond) // a few failed attempts
	s.Write([]byte("b\n"))
	in.fail.Store(false)
	waitFor(t, func() bool { return in.all() == "a\nb\n" })
}

func TestDropsOldestWhenFullAndReports(t *testing.T) {
	in := &ingest{}
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newShipper(Config{Endpoint: srv.URL}, 2)
	s.interval = 10 * time.Millisecond
	s.logger = slog.New(slog.DiscardHandler)

	s.Write([]byte("oldest\n"))
	s.Write([]byte("old\n"))
	s.Write([]byte("new\n"))    // full: "oldest" goes
	s.Write([]byte("newest\n")) // full: "old" goes
	s.start()
	defer s.Close(time.Second)

	waitFor(t, func() bool { return strings.Contains(in.all(), "newest\n") })
	got := in.all()
	if !strings.Contains(got, `"dropped":2`) || strings.Contains(got, "oldest") || strings.Contains(got, "old\n") {
		t.Fatalf("got %q", got)
	}
	if !strings.HasSuffix(got, "new\nnewest\n") {
		t.Fatalf("order: %q", got)
	}
}

func TestPendingBatchIsDroppedWhenQueueFills(t *testing.T) {
	in := &ingest{}
	in.fail.Store(true)
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newShipper(Config{Endpoint: srv.URL}, 2)
	s.logger = slog.New(slog.DiscardHandler)

	s.Write([]byte("b1\n"))
	s.Write([]byte("b2\n"))
	if _, err := s.flush(t.Context()); err == nil { // fails: b1,b2 pending
		t.Fatal("expected failure")
	}
	s.Write([]byte("n1\n"))
	s.Write([]byte("n2\n")) // queue full again: pending is the oldest data
	in.fail.Store(false)
	for more := true; more; {
		var err error
		if more, err = s.flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got := in.all()
	if strings.Contains(got, "b1") || !strings.Contains(got, `"dropped":2`) || !strings.HasSuffix(got, "n1\nn2\n") {
		t.Fatalf("got %q", got)
	}
}

func TestBacklogIsSentInSeveralBatches(t *testing.T) {
	in := &ingest{}
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newShipper(Config{Endpoint: srv.URL}, 5000)
	s.interval = 10 * time.Millisecond
	line := strings.Repeat("x", 499) + "\n" // 4000 x 500 B = 2 MB > one batch
	for i := 0; i < 4000; i++ {
		s.Write([]byte(line))
	}
	s.start()
	defer s.Close(time.Second)
	waitFor(t, func() bool { return len(in.all()) == 4000*500 })
	in.mu.Lock()
	batches := len(in.bodies)
	in.mu.Unlock()
	if batches < 2 {
		t.Fatalf("expected several batches, got %d", batches)
	}
}

func TestWriteNeverBlocks(t *testing.T) {
	in := &ingest{hang: make(chan struct{})}
	srv := httptest.NewServer(in)
	defer srv.Close()
	defer close(in.hang)
	s := newTestShipper(srv.URL)
	s.start()
	defer s.stop()

	start := time.Now()
	line := []byte(strings.Repeat("x", 99) + "\n")
	for i := 0; i < 50000; i++ { // 5MB, far over the buffer, endpoint hanging
		s.Write(line)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("writes took %s", d)
	}
}
