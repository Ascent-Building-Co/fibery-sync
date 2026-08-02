package fibsync

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipMiddleware(t *testing.T) {
	body := `{"types":[{"id":"item","name":"Item"}]}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})

	wrapped := GzipMiddleware()(handler)

	t.Run("compresses when client accepts gzip", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate")
		res := httptest.NewRecorder()

		wrapped.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", res.Code)
		}
		if ce := res.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Fatalf("expected Content-Encoding gzip, got %q", ce)
		}
		if cl := res.Header().Get("Content-Length"); cl != "" {
			t.Fatalf("expected Content-Length to be removed, got %q", cl)
		}
		if vary := res.Header().Get("Vary"); vary != "Accept-Encoding" {
			t.Fatalf("expected Vary Accept-Encoding, got %q", vary)
		}

		gr, err := gzip.NewReader(res.Body)
		if err != nil {
			t.Fatalf("failed to create gzip reader: %v", err)
		}
		defer gr.Close()
		got, err := io.ReadAll(gr)
		if err != nil {
			t.Fatalf("failed to read gzip body: %v", err)
		}
		if string(got) != body {
			t.Fatalf("expected body %q, got %q", body, string(got))
		}
	})

	t.Run("passes through when client does not accept gzip", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		res := httptest.NewRecorder()

		wrapped.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", res.Code)
		}
		if ce := res.Header().Get("Content-Encoding"); ce != "" {
			t.Fatalf("expected no Content-Encoding, got %q", ce)
		}
		if vary := res.Header().Get("Vary"); vary != "Accept-Encoding" {
			t.Fatalf("expected Vary Accept-Encoding, got %q", vary)
		}
		if res.Body.String() != body {
			t.Fatalf("expected body %q, got %q", body, res.Body.String())
		}
	})

	t.Run("handles gzip with quality parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=1.0")
		res := httptest.NewRecorder()

		wrapped.ServeHTTP(res, req)

		if ce := res.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Fatalf("expected Content-Encoding gzip, got %q", ce)
		}
	})

	t.Run("case insensitive accept encoding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "GZIP")
		res := httptest.NewRecorder()

		wrapped.ServeHTTP(res, req)

		if ce := res.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Fatalf("expected Content-Encoding gzip, got %q", ce)
		}
	})
}

func TestGzipMiddlewareSkipsWhenContentEncodingSet(t *testing.T) {
	original := []byte("already compressed data")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(original)
	})

	wrapped := GzipMiddleware()(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	res := httptest.NewRecorder()

	wrapped.ServeHTTP(res, req)

	if ce := res.Header().Get("Content-Encoding"); ce != "br" {
		t.Fatalf("expected Content-Encoding br, got %q", ce)
	}
	if !bytes.Equal(res.Body.Bytes(), original) {
		t.Fatalf("expected body to pass through unmodified")
	}
}

func TestGzipMiddlewareImplicitWriteHeader(t *testing.T) {
	body := "hello"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write without calling WriteHeader first.
		w.Write([]byte(body))
	})

	wrapped := GzipMiddleware()(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	wrapped.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if ce := res.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("expected Content-Encoding gzip, got %q", ce)
	}

	gr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gr.Close()
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("failed to read gzip body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("expected body %q, got %q", body, string(got))
	}
}

func TestGzipMiddlewareContentLengthRemoved(t *testing.T) {
	body := "some content"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "12")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})

	wrapped := GzipMiddleware()(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	wrapped.ServeHTTP(res, req)

	if cl := res.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("expected Content-Length to be removed, got %q", cl)
	}
}

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"empty", "", false},
		{"gzip only", "gzip", true},
		{"gzip with others", "deflate, gzip, br", true},
		{"gzip with quality", "gzip;q=0.8", true},
		{"no gzip", "deflate, br", false},
		{"case insensitive", "Gzip", true},
		{"multiple headers", "deflate", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Accept-Encoding", tt.header)
			}
			if got := acceptsGzip(req); got != tt.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
