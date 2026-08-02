package fibsync

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// Middleware is a function that wraps an http.Handler and returns a new
// http.Handler. The first middleware in a slice is the outermost wrapper.
type Middleware func(http.Handler) http.Handler

func applyMiddleware(handler http.Handler, middleware ...Middleware) http.Handler {
	wrapped := handler
	for idx := len(middleware) - 1; idx >= 0; idx-- {
		mw := middleware[idx]
		if mw != nil {
			wrapped = mw(wrapped)
		}
	}
	return wrapped
}

// SkipWhen wraps mw so it is bypassed for requests where skip returns true;
// those requests pass straight to the next handler. It is useful for exempting
// specific routes — for example a public, third-party-facing webhook endpoint —
// from an IP allowlist or other guard while keeping that guard on every other
// route. A nil skip applies mw to every request.
//
// mw is constructed once (any per-instance state it holds, such as a rate
// limiter's buckets, is shared across requests as usual); only the skip check
// runs per request.
func SkipWhen(mw Middleware, skip func(*http.Request) bool) Middleware {
	return func(next http.Handler) http.Handler {
		guarded := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

// GzipMiddleware returns a Middleware that compresses response bodies using
// gzip when the client indicates support via the Accept-Encoding header.
//
// The middleware always sets Vary: Accept-Encoding so that caches
// differentiate between compressed and uncompressed variants. If the
// handler has already set a Content-Encoding header (e.g. because an
// upstream response was forwarded as-is), the middleware will not
// double-compress.
func GzipMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")

			if !acceptsGzip(r) {
				next.ServeHTTP(w, r)
				return
			}

			gz := gzip.NewWriter(w)
			grw := &gzipResponseWriter{
				ResponseWriter: w,
				gz:             gz,
			}
			defer grw.Close()

			next.ServeHTTP(grw, r)
		})
	}
}

// acceptsGzip reports whether the request's Accept-Encoding header
// includes "gzip" as a supported encoding.
func acceptsGzip(r *http.Request) bool {
	for _, val := range r.Header.Values("Accept-Encoding") {
		for _, enc := range strings.Split(val, ",") {
			// Trim whitespace and ignore quality parameters (e.g. "gzip;q=1.0").
			enc = strings.TrimSpace(enc)
			if semi := strings.IndexByte(enc, ';'); semi >= 0 {
				enc = strings.TrimSpace(enc[:semi])
			}
			if strings.EqualFold(enc, "gzip") {
				return true
			}
		}
	}
	return false
}

// gzipResponseWriter wraps an http.ResponseWriter and compresses writes
// through a gzip.Writer. It removes the Content-Length header (the
// compressed size is unknown ahead of time) and sets Content-Encoding: gzip.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	skipGzip    bool // set when the handler already provided Content-Encoding
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true

	// If the handler already set a Content-Encoding, pass through without
	// compressing to avoid double-encoding (e.g. proxied responses).
	if g.ResponseWriter.Header().Get("Content-Encoding") != "" {
		g.skipGzip = true
		g.ResponseWriter.WriteHeader(code)
		return
	}

	g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.skipGzip {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

// Flush flushes the gzip writer and the underlying ResponseWriter if it
// implements http.Flusher.
func (g *gzipResponseWriter) Flush() {
	if !g.skipGzip {
		g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for http.ResponseController
// compatibility.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

// Close closes the gzip writer, flushing any buffered data. It is safe to
// call multiple times.
func (g *gzipResponseWriter) Close() {
	if !g.skipGzip && g.gz != nil {
		g.gz.Close()
	}
}
