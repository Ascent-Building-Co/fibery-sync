package fibsync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// countingHandler is a slog.Handler that counts Error-level records.
type countingHandler struct {
	count atomic.Int32
}

func (h *countingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelError
}

func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.count.Add(1)
	}
	return nil
}

func (h *countingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(_ string) slog.Handler      { return h }

// withCountingLogger installs a counting slog handler for the duration of a
// test, restoring slog.Default() on cleanup.
func withCountingLogger(t *testing.T) *countingHandler {
	t.Helper()
	h := &countingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func TestReadJSONErrors(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	tests := []struct {
		name   string
		body   io.Reader
		max    int64
		strict bool
	}{
		{name: "missing body", body: nil, max: 0, strict: false},
		{name: "empty body", body: bytes.NewReader(nil), max: 0, strict: false},
		{name: "too large", body: bytes.NewBufferString("{}"), max: 1, strict: false},
		{name: "malformed json", body: bytes.NewBufferString("{\"count\":"), max: 0, strict: false},
		{name: "unexpected eof", body: bytes.NewBufferString("{\"count\":\""), max: 0, strict: false},
		{name: "wrong type", body: bytes.NewBufferString("{\"count\":\"x\"}"), max: 0, strict: false},
		{name: "unknown field strict", body: bytes.NewBufferString("{\"count\":1,\"extra\":2}"), max: 0, strict: true},
		{name: "multiple values", body: bytes.NewBufferString("{\"count\":1} {\"count\":2}"), max: 0, strict: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := &http.Request{}
			if tt.body != nil {
				req.Body = io.NopCloser(tt.body)
			}
			var out payload
			err := readJSON(w, req, &out, tt.max, tt.strict)
			if err == nil {
				t.Fatalf("expected error")
			}
			if err.Status != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d", err.Status)
			}
		})
	}
}

func TestReadJSONSuccess(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{\"count\":1}"))
	var out payload
	if err := readJSON(w, req, &out, 0, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected count 1, got %d", out.Count)
	}
}

func TestReadJSONAllowsUnknownFieldsWhenNotStrict(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{\"count\":1,\"extra\":2}"))
	var out payload
	if err := readJSON(w, req, &out, 0, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected count 1, got %d", out.Count)
	}
}

func TestRespondWithJSON(t *testing.T) {
	w := httptest.NewRecorder()
	payload := map[string]any{"ok": true}
	respondWithJSON(w, http.StatusCreated, payload)

	res := w.Result()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content-type application/json, got %s", got)
	}
	var decoded map[string]any
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded["ok"] != true {
		t.Fatalf("expected ok true, got %v", decoded["ok"])
	}
}

func TestRespondWithJSONMarshalErrorLogs(t *testing.T) {
	h := withCountingLogger(t)

	w := httptest.NewRecorder()
	respondWithJSON(w, http.StatusOK, math.Inf(1))

	res := w.Result()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", res.StatusCode)
	}
	if h.count.Load() == 0 {
		t.Fatalf("expected logger to be called")
	}
}

func TestRespondWithError(t *testing.T) {
	w := httptest.NewRecorder()
	respondWithError(w, BadRequest("oops"))

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", res.StatusCode)
	}
	var payload IntegrationError
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Message != "oops" {
		t.Fatalf("expected message oops, got %s", payload.Message)
	}
}

func TestRespondWithErrorLogsServerErrors(t *testing.T) {
	h := withCountingLogger(t)

	w := httptest.NewRecorder()
	respondWithError(w, InternalError("boom"))

	res := w.Result()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", res.StatusCode)
	}
	if h.count.Load() == 0 {
		t.Fatalf("expected logger to be called")
	}
}

func TestRespondWithErrorDefaultsStatus(t *testing.T) {
	w := httptest.NewRecorder()
	respondWithError(w, &IntegrationError{Message: "oops"})

	res := w.Result()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", res.StatusCode)
	}
}

func TestDecodeJSONSuccess(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	var out payload
	if err := DecodeJSON([]byte(`{"count":1}`), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected count 1, got %d", out.Count)
	}
}

func TestDecodeJSONErrors(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	tests := []struct {
		name   string
		data   []byte
		strict bool
	}{
		{name: "empty", data: []byte{}, strict: false},
		{name: "malformed json", data: []byte(`{"count":`), strict: false},
		{name: "unexpected eof", data: []byte(`{"count":"`), strict: false},
		{name: "wrong type", data: []byte(`{"count":"x"}`), strict: false},
		{name: "unknown field strict", data: []byte(`{"count":1,"extra":2}`), strict: true},
		{name: "multiple values", data: []byte(`{"count":1} {"count":2}`), strict: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out payload
			err := DecodeJSON(tt.data, &out, tt.strict)
			if err == nil {
				t.Fatalf("expected error")
			}
			if err.Status != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d", err.Status)
			}
		})
	}
}

func TestDecodeJSONAllowsUnknownFieldsWhenNotStrict(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}

	var out payload
	if err := DecodeJSON([]byte(`{"count":1,"extra":2}`), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected count 1, got %d", out.Count)
	}
}

func TestDecodeJSONInvalidUnmarshalTarget(t *testing.T) {
	err := DecodeJSON([]byte(`{"count":1}`), nil, false)
	if err == nil {
		t.Fatalf("expected error for nil target")
	}
	if err.Status != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", err.Status)
	}
}

// Fibery occasionally sends the workspace as an empty object rather than a
// string. It must decode as "" without loosening strict mode for the rest of
// the request.
func TestSyncMetaRequestWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		want    Workspace
		wantErr string
	}{
		{name: "string", field: `,"workspace":"ascent"`, want: "ascent"},
		{name: "empty object", field: `,"workspace":{}`, want: ""},
		{name: "empty object with space", field: `,"workspace":{ }`, want: ""},
		{name: "null", field: `,"workspace":null`, want: ""},
		{name: "absent", field: ``, want: ""},
		{name: "non-empty object", field: `,"workspace":{"id":"x"}`, wantErr: "invalid value for field workspace"},
		{name: "number", field: `,"workspace":1`, wantErr: "invalid value for field workspace"},
		{name: "strict still applies", field: `,"workspace":{},"extra":1`, wantErr: "invalid json body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"types":["task"],"account":{"app":"a"}` + tt.field + `}`
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
			var out SyncMetaRequest[testAccountFields]

			err := readJSON(httptest.NewRecorder(), req, &out, 0, true)
			if tt.wantErr != "" {
				if err == nil || err.Message != tt.wantErr {
					t.Fatalf("want error %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Workspace != tt.want {
				t.Errorf("workspace = %q, want %q", out.Workspace, tt.want)
			}
			if len(out.Types) != 1 || out.Types[0] != "task" {
				t.Errorf("rest of the request not decoded: %+v", out)
			}
		})
	}
}
