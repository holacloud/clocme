package api

import (
	"log/slog"
	"net"
	"net/http"
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

// accessLog logs one structured entry per request with what was asked (user
// agent, Accept, Sec-Fetch-Dest: the inputs of the content negotiation) and
// what was served (status, content type, size), e.g. with the JSON handler:
//
//	{"time":"...","level":"INFO","msg":"http","method":"GET","path":"/github.com/a/b",
//	 "status":200,"type":"text/html","bytes":18634,"dur_ms":2.1,"ip":"1.2.3.4",
//	 "ua":"LinkedInBot/1.0 ...","accept":"*/*","dest":""}
//
// 5xx responses are logged as errors.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			// docker healthcheck every 30s: ~2.9k useless lines a day
			next.ServeHTTP(w, r)
			return
		}
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
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		slog.LogAttrs(r.Context(), level, "http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.RequestURI()),
			slog.Int("status", status),
			slog.String("type", ctype),
			slog.Int("bytes", rec.bytes),
			slog.Float64("dur_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("ip", clientIP(r)),
			slog.String("ua", r.Header.Get("User-Agent")),
			slog.String("accept", r.Header.Get("Accept")),
			slog.String("dest", r.Header.Get("Sec-Fetch-Dest")),
		)
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
