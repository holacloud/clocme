package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureLogs sends the default slog logger to a JSON buffer during a test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestAccessLog(t *testing.T) {
	buf := captureLogs(t)

	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(bytes.Repeat([]byte("x"), 2048))
	}))
	r := httptest.NewRequest("GET", "/github.com/a/b?x=1", nil)
	r.Header.Set("User-Agent", "LinkedInBot/1.0")
	r.Header.Set("Accept", "*/*")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)

	var e map[string]any
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("not a JSON line: %v: %s", err, buf)
	}
	want := map[string]any{
		"level": "INFO", "msg": "http", "method": "GET", "path": "/github.com/a/b?x=1",
		"status": 200.0, "type": "text/html", "bytes": 2048.0, "ip": "1.2.3.4",
		"ua": "LinkedInBot/1.0", "accept": "*/*", "dest": "",
	}
	for k, v := range want {
		if e[k] != v {
			t.Errorf("%s = %v, want %v", k, e[k], v)
		}
	}
	if _, ok := e["dur_ms"].(float64); !ok {
		t.Errorf("dur_ms missing: %v", e)
	}

	buf.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	if buf.Len() != 0 {
		t.Errorf("healthz must not be logged: %q", buf.String())
	}

	buf.Reset()
	h = accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	json.Unmarshal(buf.Bytes(), &e)
	if e["level"] != "ERROR" || e["status"] != 502.0 || e["type"] != "-" {
		t.Errorf("5xx entry: %v", e)
	}
}
