package api

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// statusRecorder captures what the handler answered.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// accessLog logs one line per request with what was asked (user agent,
// Accept, Sec-Fetch-Dest: the inputs of the content negotiation) and what
// was served (status, content type, size), e.g.
//
//	GET /github.com/a/b 200 text/html 18.2KB 2.1ms ip=1.2.3.4 ua="LinkedInBot/1.0 ..." accept="*/*" dest=""
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		ctype, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
		if ctype == "" {
			ctype = "-"
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("%s %s %d %s %s %s ip=%s ua=%q accept=%q dest=%q",
			r.Method, r.URL.RequestURI(), status, ctype, humanBytes(rec.bytes),
			time.Since(start).Round(time.Microsecond), clientIP(r),
			r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Sec-Fetch-Dest"))
	})
}

// clientIP prefers the address reported by the proxy in front (Glue2).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + "MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + "KB"
	default:
		return strconv.Itoa(n) + "B"
	}
}
