package logship

import (
	"io"
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
	s := newShipper(Config{Endpoint: url, ApiKey: "k", ApiSecret: "s"})
	s.interval = 10 * time.Millisecond
	s.errOut = io.Discard
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
	in.fail.Store(true)
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newTestShipper(srv.URL)
	s.maxBuffer = 10
	s.start()
	defer s.Close(time.Second)

	s.Write([]byte("12345678\n")) // 9 bytes, fits
	s.Write([]byte("newer\n"))    // 15 > 10: the oldest line goes
	s.Write([]byte("last\n"))     // 11 > 10: "newer" goes
	in.fail.Store(false)
	waitFor(t, func() bool { return strings.Contains(in.all(), "last\n") })
	got := in.all()
	if !strings.Contains(got, "dropped 2 old log lines") || strings.Contains(got, "12345678") || strings.Contains(got, "newer") {
		t.Fatalf("got %q", got)
	}
}

func TestTrimOldest(t *testing.T) {
	cases := []struct {
		in      string
		max     int
		want    string
		dropped int
	}{
		{"aaa\nbbb\nccc\n", 12, "aaa\nbbb\nccc\n", 0},
		{"aaa\nbbb\nccc\n", 11, "bbb\nccc\n", 1},
		{"aaa\nbbb\nccc\n", 8, "bbb\nccc\n", 1},
		{"aaa\nbbb\nccc\n", 7, "ccc\n", 2},
		{"aaa\nbbb\nccc\n", 3, "", 3}, // even the last line does not fit
		{"a-very-long-line-without-newline", 5, "", 1},
	}
	for _, c := range cases {
		s := newShipper(Config{Endpoint: "http://x"})
		s.maxBuffer = c.max
		s.buf.WriteString(c.in)
		s.trimOldest()
		if s.buf.String() != c.want || s.dropped != c.dropped {
			t.Errorf("%q max %d: got %q dropped %d, want %q dropped %d", c.in, c.max, s.buf.String(), s.dropped, c.want, c.dropped)
		}
	}
}

func TestFailedBatchKeepsNewestLines(t *testing.T) {
	in := &ingest{}
	in.fail.Store(true)
	srv := httptest.NewServer(in)
	defer srv.Close()
	s := newTestShipper(srv.URL)
	s.maxBuffer = 12
	s.errOut = io.Discard

	s.Write([]byte("old1\nold2\n"))
	s.flush(t.Context()) // fails: batch goes back in front
	s.Write([]byte("new1\nnew2\n"))
	// 20 bytes > 12: the two old lines are dropped, the new ones kept
	if got := s.buf.String(); got != "new1\nnew2\n" {
		t.Fatalf("buffer %q", got)
	}
	if s.dropped != 2 {
		t.Fatalf("dropped %d", s.dropped)
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
