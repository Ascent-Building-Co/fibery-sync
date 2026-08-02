package fibsync

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("failed to decode log record %q: %v", buf.String(), err)
	}
	return rec
}

func TestRequestLogMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestLogMiddleware(RequestLog{Logger: logger})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("hello"))
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	rec := decodeLog(t, &buf)
	if rec["method"] != "GET" {
		t.Fatalf("method = %v", rec["method"])
	}
	if rec["path"] != "/widgets" {
		t.Fatalf("path = %v", rec["path"])
	}
	if rec["status"].(float64) != http.StatusCreated {
		t.Fatalf("status = %v", rec["status"])
	}
	if rec["bytes"].(float64) != 5 {
		t.Fatalf("bytes = %v, want 5", rec["bytes"])
	}
	if rec["remote"] != "203.0.113.5" {
		t.Fatalf("remote = %v", rec["remote"])
	}
	if _, ok := rec["duration_ms"]; !ok {
		t.Fatalf("expected duration_ms in record")
	}
}

func TestRequestLogDefaultStatus(t *testing.T) {
	run := func(handler http.HandlerFunc) map[string]any {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		h := RequestLogMiddleware(RequestLog{Logger: logger})(handler)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.5:1"
		h.ServeHTTP(httptest.NewRecorder(), req)
		return decodeLog(t, &buf)
	}

	// Body written without an explicit WriteHeader -> 200.
	if rec := run(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}); rec["status"].(float64) != http.StatusOK {
		t.Fatalf("implicit-write status = %v, want 200", rec["status"])
	}

	// Nothing written at all -> still 200.
	if rec := run(func(w http.ResponseWriter, r *http.Request) {}); rec["status"].(float64) != http.StatusOK {
		t.Fatalf("no-write status = %v, want 200", rec["status"])
	}
}

func TestRequestLogComposesWithGzip(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	// Logging outermost, gzip inner.
	h := RequestLogMiddleware(RequestLog{Logger: logger})(
		GzipMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(strings.Repeat("x", 1000)))
		})),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.RemoteAddr = "203.0.113.5:1"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if ce := res.Result().Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("expected gzip Content-Encoding, got %q", ce)
	}
	// The response body is compressed and must decode back to the original.
	gr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, _ := io.ReadAll(gr)
	if len(got) != 1000 {
		t.Fatalf("expected 1000 decompressed bytes, got %d", len(got))
	}

	rec := decodeLog(t, &buf)
	if rec["status"].(float64) != http.StatusOK {
		t.Fatalf("status = %v", rec["status"])
	}
	// Logged bytes reflect the on-wire (compressed) size, which is smaller than
	// the 1000-byte payload.
	logged := rec["bytes"].(float64)
	if logged <= 0 || logged >= 1000 {
		t.Fatalf("expected logged bytes to be the compressed size (0 < n < 1000), got %v", logged)
	}
}
