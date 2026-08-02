package fibsync

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"
)

// RequestLog configures RequestLogMiddleware.
type RequestLog struct {
	// Logger receives the access records. Defaults to slog.Default().
	Logger *slog.Logger
	// Level is the level access records are emitted at. Defaults to LevelInfo.
	Level slog.Level
	// Trusted lists proxy CIDRs whose forwarding headers may be believed when
	// resolving the logged client IP. See ClientIP.
	Trusted []netip.Prefix
}

// statusRecorder wraps an http.ResponseWriter to capture the response status
// code and byte count. It implements Flush and Unwrap so it composes with
// other ResponseWriter wrappers (for example gzipResponseWriter).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// RequestLogMiddleware returns a Middleware that emits one structured access
// record per request after it completes. Only metadata is logged (method,
// path, status, duration, response bytes, client IP) — never request or
// response bodies, which may contain OAuth tokens or the account-gate secret.
func RequestLogMiddleware(cfg RequestLog) Middleware {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	level := cfg.Level
	trusted := cfg.Trusted

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int64("bytes", rec.bytes),
				slog.String("remote", ClientIP(r, trusted).String()),
			)
		})
	}
}
