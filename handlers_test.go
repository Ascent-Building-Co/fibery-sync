package fibsync

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

type testAccountFields struct {
	Token string `json:"token"`
}

type testPage struct {
	Cursor string `json:"cursor"`
}

type testWebhookParams struct {
	Value string `json:"value"`
}

type testWebhookReply struct {
	OK bool `json:"ok"`
}

type testWebhookHeaders struct {
	Signature string `header:"X-Signature"`
}

type testWebhookNotification struct {
	Event string `json:"event"`
}

type testResourceParams struct {
	ID string `json:"id"`
}

type testHeadersStruct struct {
	Signature string   `header:"X-Signature"`
	Multi     []string `header:"X-Multi"`
	Raw       []byte   `header:"X-Raw"`
	Ptr       *string  `header:"X-Ptr"`
}

type testWebhookPreProcess struct {
	gotRawBody      []byte
	gotHeaders      testWebhookHeaders
	gotNotification testWebhookNotification
	resp            WebhookPreProcessResponse[testWebhookReply]
	err             *IntegrationError
}

func (t *testWebhookPreProcess) WebhookPreProcess(ctx context.Context, rawBody []byte, headers testWebhookHeaders, notification testWebhookNotification) (WebhookPreProcessResponse[testWebhookReply], *IntegrationError) {
	t.gotRawBody = rawBody
	t.gotHeaders = headers
	t.gotNotification = notification
	return t.resp, t.err
}

func TestDecodeHelper(t *testing.T) {
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}

	validReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{\"token\":\"t\"}"))
	validRec := httptest.NewRecorder()
	var out testAccountFields
	if ok := h.decode(validRec, validReq, &out); !ok {
		t.Fatalf("expected decode to succeed")
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{invalid"))
	invalidRec := httptest.NewRecorder()
	if ok := h.decode(invalidRec, invalidReq, &out); ok {
		t.Fatalf("expected decode to fail")
	}
	if invalidRec.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", invalidRec.Result().StatusCode)
	}
}

func TestDecodeWebhookHeadersStruct(t *testing.T) {
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testHeadersStruct, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Signature", "sig")
	req.Header.Add("X-Multi", "a")
	req.Header.Add("X-Multi", "b")
	req.Header.Set("X-Raw", "raw")
	req.Header.Set("X-Ptr", "ptr")

	got, err := h.decodeWebhookHeaders(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Signature != "sig" {
		t.Fatalf("expected signature sig, got %q", got.Signature)
	}
	if !reflect.DeepEqual(got.Multi, []string{"a", "b"}) {
		t.Fatalf("expected multi values, got %v", got.Multi)
	}
	if string(got.Raw) != "raw" {
		t.Fatalf("expected raw bytes, got %q", string(got.Raw))
	}
	if got.Ptr == nil || *got.Ptr != "ptr" {
		t.Fatalf("expected pointer value to be set")
	}
}

func TestDecodeWebhookHeadersMapTypes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Add("X-Test", "a")
	req.Header.Add("X-Test", "b")

	mapStringHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, map[string]string, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	mapString, err := mapStringHandler.decodeWebhookHeaders(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapString["X-Test"] != "a,b" {
		t.Fatalf("expected joined values, got %q", mapString["X-Test"])
	}

	mapSliceHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, map[string][]string, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	mapSlice, err := mapSliceHandler.decodeWebhookHeaders(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(mapSlice["X-Test"], []string{"a", "b"}) {
		t.Fatalf("expected slice values, got %v", mapSlice["X-Test"])
	}

	mapAnyHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, map[string]any, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	mapAny, err := mapAnyHandler.decodeWebhookHeaders(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(mapAny["X-Test"], []string{"a", "b"}) {
		t.Fatalf("expected any values, got %v", mapAny["X-Test"])
	}
}

func TestDecodeWebhookHeadersErrors(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Test", "value")

	badKeyHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, map[int]string, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	if _, err := badKeyHandler.decodeWebhookHeaders(req); err == nil {
		t.Fatalf("expected error for non-string map key")
	}

	badValueHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, map[string]int, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	if _, err := badValueHandler.decodeWebhookHeaders(req); err == nil {
		t.Fatalf("expected error for unsupported map value type")
	}

	badTypeHandler := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, int, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}
	if _, err := badTypeHandler.decodeWebhookHeaders(req); err == nil {
		t.Fatalf("expected error for unsupported headers type")
	}
}

func TestSetHeaderField(t *testing.T) {
	text := "value"
	str := ""
	if err := setHeaderField(reflect.ValueOf(&str).Elem(), []string{text}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if str != text {
		t.Fatalf("expected %q, got %q", text, str)
	}

	list := []string{}
	if err := setHeaderField(reflect.ValueOf(&list).Elem(), []string{"a", "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(list, []string{"a", "b"}) {
		t.Fatalf("unexpected list value: %v", list)
	}

	bytesField := []byte{}
	if err := setHeaderField(reflect.ValueOf(&bytesField).Elem(), []string{"raw"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(bytesField) != "raw" {
		t.Fatalf("unexpected bytes value: %q", string(bytesField))
	}

	ptr := (*string)(nil)
	if err := setHeaderField(reflect.ValueOf(&ptr).Elem(), []string{text}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ptr == nil || *ptr != text {
		t.Fatalf("expected pointer value to be set")
	}

	unsupported := 0
	if err := setHeaderField(reflect.ValueOf(&unsupported).Elem(), []string{"x"}); err == nil {
		t.Fatalf("expected error for unsupported type")
	}
}

func TestRouteWithBasePathAndID(t *testing.T) {
	baseCases := []struct {
		name     string
		basePath string
		id       string
		expected string
	}{
		{name: "no base path no id", basePath: "", id: "", expected: "/"},
		{name: "id only", basePath: "", id: "quickbooks", expected: "/quickbooks/"},
		{name: "base path only", basePath: "integrations", id: "", expected: "/integrations/"},
		{name: "base path and id", basePath: "integrations", id: "quickbooks", expected: "/integrations/quickbooks/"},
		{name: "root base path", basePath: "/", id: "", expected: "/"},
		{name: "id with slashes", basePath: "integrations", id: "/qb/", expected: "/integrations/qb/"},
	}

	for _, tt := range baseCases {
		t.Run(tt.name, func(t *testing.T) {
			h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
				handlerOpts: handlerOptions{basePath: tt.basePath, integrationID: tt.id},
			}
			if got := h.route("/"); got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestLogError(t *testing.T) {
	// logError uses slog.Default() directly; just verify nil error is a no-op
	// and non-nil error does not panic.
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{},
	}

	h.logError("ignored", nil)

	h.logError("boom", io.EOF)

	h = &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		handlerOpts: handlerOptions{integrationID: "qb"},
	}
	h.logError("fail", io.EOF)
}

func TestHandleWebhookPreProcess(t *testing.T) {
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{maxBody: 1024},
	}

	// success: raw body, headers, and decoded notification all passed through
	webhooks := &testWebhookPreProcess{
		resp: WebhookPreProcessResponse[testWebhookReply]{
			WorkspaceIDs: []string{"ws1"},
			Reply:        testWebhookReply{OK: true},
		},
	}
	body := `{"event":"created"}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Signature", "sig123")
	res := httptest.NewRecorder()
	h.handleWebhookPreProcess(webhooks)(res, req)
	if res.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Result().StatusCode)
	}
	if string(webhooks.gotRawBody) != body {
		t.Fatalf("expected raw body %q, got %q", body, string(webhooks.gotRawBody))
	}
	if webhooks.gotHeaders.Signature != "sig123" {
		t.Fatalf("expected signature sig123, got %q", webhooks.gotHeaders.Signature)
	}
	if webhooks.gotNotification.Event != "created" {
		t.Fatalf("expected event created, got %q", webhooks.gotNotification.Event)
	}

	// missing body
	res = httptest.NewRecorder()
	req = &http.Request{}
	h.handleWebhookPreProcess(webhooks)(res, req)
	if res.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing body, got %d", res.Result().StatusCode)
	}

	// empty body
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(nil))
	h.handleWebhookPreProcess(webhooks)(res, req)
	if res.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 for empty body, got %d", res.Result().StatusCode)
	}

	// body too large
	hSmall := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: serverOptions{maxBody: 4},
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"event":"x"}`))
	hSmall.handleWebhookPreProcess(webhooks)(res, req)
	if res.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 for large body, got %d", res.Result().StatusCode)
	}

	// malformed JSON
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{invalid`))
	h.handleWebhookPreProcess(webhooks)(res, req)
	if res.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 for malformed json, got %d", res.Result().StatusCode)
	}

	// integration error propagated
	webhooksErr := &testWebhookPreProcess{err: Unauthorized("bad sig")}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	h.handleWebhookPreProcess(webhooksErr)(res, req)
	if res.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", res.Result().StatusCode)
	}
}

// trackingReadCloser wraps an io.Reader and records whether Close was called.
type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

type testLogo struct {
	resp *LogoResponse
	err  *IntegrationError
}

func (t *testLogo) Logo(_ context.Context) (*LogoResponse, *IntegrationError) {
	return t.resp, t.err
}

func TestHandleLogo(t *testing.T) {
	newHandler := func() *integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}] {
		return &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{}
	}

	t.Run("204 when nil response", func(t *testing.T) {
		h := newHandler()
		logo := &testLogo{resp: nil}
		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)
		if res.Result().StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", res.Result().StatusCode)
		}
		body, _ := io.ReadAll(res.Result().Body)
		if len(body) != 0 {
			t.Fatalf("expected empty body, got %q", string(body))
		}
		if ct := res.Result().Header.Get("Content-Type"); ct != "" {
			t.Fatalf("expected no Content-Type, got %q", ct)
		}
	})

	t.Run("204 when both fields nil", func(t *testing.T) {
		h := newHandler()
		logo := &testLogo{resp: &LogoResponse{}}
		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)
		if res.Result().StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", res.Result().StatusCode)
		}
		body, _ := io.ReadAll(res.Result().Body)
		if len(body) != 0 {
			t.Fatalf("expected empty body, got %q", string(body))
		}
	})

	t.Run("200 with svg body", func(t *testing.T) {
		h := newHandler()
		const svg = `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
		body := &trackingReadCloser{Reader: bytes.NewBufferString(svg)}
		logo := &testLogo{resp: &LogoResponse{Body: body}}

		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)

		if res.Result().StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", res.Result().StatusCode)
		}
		if ct := res.Result().Header.Get("Content-Type"); ct != "image/svg+xml" {
			t.Fatalf("expected image/svg+xml, got %q", ct)
		}
		got, _ := io.ReadAll(res.Result().Body)
		if string(got) != svg {
			t.Fatalf("expected body %q, got %q", svg, string(got))
		}
		if !body.closed {
			t.Fatalf("expected body Close to have been called")
		}
	})

	t.Run("302 redirect on URL", func(t *testing.T) {
		h := newHandler()
		u, err := url.Parse("https://example.test/logo.svg")
		if err != nil {
			t.Fatalf("unexpected url parse error: %v", err)
		}
		logo := &testLogo{resp: &LogoResponse{URL: u}}

		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)

		if res.Result().StatusCode != http.StatusFound {
			t.Fatalf("expected 302, got %d", res.Result().StatusCode)
		}
		if loc := res.Result().Header.Get("Location"); loc != u.String() {
			t.Fatalf("expected Location %q, got %q", u.String(), loc)
		}
	})

	t.Run("204 on integration error", func(t *testing.T) {
		h := newHandler()
		logo := &testLogo{err: InternalError("boom")}
		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)
		if res.Result().StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204 on error, got %d", res.Result().StatusCode)
		}
		body, _ := io.ReadAll(res.Result().Body)
		if len(body) != 0 {
			t.Fatalf("expected empty body on error, got %q", string(body))
		}
	})

	t.Run("204 on integration error even when response set", func(t *testing.T) {
		// Defensive: if the integration returns both an error AND a body,
		// the framework should still treat it as an error path.
		h := newHandler()
		body := &trackingReadCloser{Reader: bytes.NewBufferString("ignored")}
		logo := &testLogo{
			resp: &LogoResponse{Body: body},
			err:  Unauthorized("bad creds"),
		}
		req := httptest.NewRequest(http.MethodGet, "/logo", nil)
		res := httptest.NewRecorder()
		h.handleLogo(logo)(res, req)
		if res.Result().StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", res.Result().StatusCode)
		}
		got, _ := io.ReadAll(res.Result().Body)
		if len(got) != 0 {
			t.Fatalf("expected empty body, got %q", string(got))
		}
	})
}
