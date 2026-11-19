package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(bytes.Repeat([]byte("x"), 2048))
	}))
	r := httptest.NewRequest("GET", "/github.com/a/b?x=1", nil)
	r.Header.Set("User-Agent", "LinkedInBot/1.0")
	r.Header.Set("Accept", "*/*")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)

	line := buf.String()
	for _, want := range []string{`GET /github.com/a/b?x=1 200 text/html 2.0KB `, `ip=1.2.3.4 `, `ua="LinkedInBot/1.0"`, `accept="*/*"`, `dest=""`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}

	buf.Reset()
	h = accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if !strings.Contains(buf.String(), "GET /x 304 - 0B ") {
		t.Errorf("got %q", buf.String())
	}
}
