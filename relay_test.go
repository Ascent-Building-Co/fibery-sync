package fibsync

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type relayStub struct {
	target    RelayTarget
	err       *IntegrationError
	gotBody   []byte
	gotHeader http.Header
}

func (s *relayStub) WebhookRelay(_ context.Context, rawBody []byte, header http.Header) (RelayTarget, *IntegrationError) {
	s.gotBody = rawBody
	s.gotHeader = header
	return s.target, s.err
}

func newRelayHandler(opts serverOptions) *integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}] {
	return &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		serverOpts: opts,
	}
}

func TestHandleWebhookRelayForwards(t *testing.T) {
	var (
		gotBody       []byte
		gotCT         string
		gotSig        string
		gotConnection string
		gotExtra      string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		gotSig = r.Header.Get("X-Signature")
		gotConnection = r.Header.Get("Connection")
		gotExtra = r.Header.Get("X-Extra")
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("upstream-body"))
	}))
	defer upstream.Close()

	relay := &relayStub{target: RelayTarget{
		URL:         upstream.URL,
		ContentType: "application/json",
		Header:      http.Header{"X-Extra": {"added"}},
	}}
	h := newRelayHandler(serverOptions{httpClient: upstream.Client()})

	req := httptest.NewRequest(http.MethodPost, "/quickbooks/webhook", bytes.NewBufferString("inbound"))
	req.Header.Set("Content-Type", "application/cloudevents-batch+json")
	req.Header.Set("X-Signature", "sig123")
	req.Header.Set("Connection", "keep-alive") // hop-by-hop: must be dropped
	res := httptest.NewRecorder()

	h.handleWebhookRelay(relay)(res, req)

	if string(gotBody) != "inbound" {
		t.Fatalf("upstream body = %q, want verbatim inbound", string(gotBody))
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want overridden application/json", gotCT)
	}
	if gotSig != "sig123" {
		t.Fatalf("X-Signature = %q, want forwarded", gotSig)
	}
	if gotConnection != "" {
		t.Fatalf("hop-by-hop Connection header should be dropped, got %q", gotConnection)
	}
	if gotExtra != "added" {
		t.Fatalf("X-Extra = %q, want target header override applied", gotExtra)
	}
	if code := res.Result().StatusCode; code != http.StatusAccepted {
		t.Fatalf("mirrored status = %d, want 202", code)
	}
	if res.Result().Header.Get("X-Upstream") != "yes" {
		t.Fatalf("upstream response header not mirrored")
	}
	if body, _ := io.ReadAll(res.Result().Body); string(body) != "upstream-body" {
		t.Fatalf("mirrored body = %q, want upstream-body", string(body))
	}
	if string(relay.gotBody) != "inbound" {
		t.Fatalf("relay received body = %q", string(relay.gotBody))
	}
}

func TestHandleWebhookRelayErrors(t *testing.T) {
	call := func(h *integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}], relay *relayStub, setup func(*http.Request)) int {
		req := httptest.NewRequest(http.MethodPost, "/quickbooks/webhook", bytes.NewBufferString("body"))
		if setup != nil {
			setup(req)
		}
		res := httptest.NewRecorder()
		h.handleWebhookRelay(relay)(res, req)
		return res.Result().StatusCode
	}

	t.Run("nil body", func(t *testing.T) {
		h := newRelayHandler(serverOptions{httpClient: http.DefaultClient})
		if code := call(h, &relayStub{}, func(r *http.Request) { r.Body = nil }); code != http.StatusBadRequest {
			t.Fatalf("nil body = %d, want 400", code)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		h := newRelayHandler(serverOptions{httpClient: http.DefaultClient})
		if code := call(h, &relayStub{}, func(r *http.Request) { r.Body = io.NopCloser(bytes.NewReader(nil)) }); code != http.StatusBadRequest {
			t.Fatalf("empty body = %d, want 400", code)
		}
	})

	t.Run("resolve error maps to its status", func(t *testing.T) {
		h := newRelayHandler(serverOptions{httpClient: http.DefaultClient})
		if code := call(h, &relayStub{err: Unauthorized("nope")}, nil); code != http.StatusUnauthorized {
			t.Fatalf("resolve error = %d, want 401", code)
		}
	})

	t.Run("empty target URL", func(t *testing.T) {
		h := newRelayHandler(serverOptions{httpClient: http.DefaultClient})
		if code := call(h, &relayStub{target: RelayTarget{URL: ""}}, nil); code != http.StatusInternalServerError {
			t.Fatalf("empty URL = %d, want 500", code)
		}
	})

	t.Run("oversize body", func(t *testing.T) {
		h := newRelayHandler(serverOptions{httpClient: http.DefaultClient, maxBody: 2})
		relay := &relayStub{target: RelayTarget{URL: "http://example.test"}}
		if code := call(h, relay, func(r *http.Request) { r.Body = io.NopCloser(bytes.NewBufferString("too large")) }); code != http.StatusBadRequest {
			t.Fatalf("oversize = %d, want 400", code)
		}
	})
}

// relayIntegration is a gateStub that also implements IntegrationWithWebhookRelay.
type relayIntegration struct {
	*gateStub
}

func (relayIntegration) WebhookRelay(context.Context, []byte, http.Header) (RelayTarget, *IntegrationError) {
	return RelayTarget{URL: "http://example.test"}, nil
}

func TestServerIPAllowlistExemptsRelay(t *testing.T) {
	srv := NewServer()
	if err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), srv, relayIntegration{gateStub: &gateStub{}}, WithIntegrationID("quickbooks"),
	); err != nil {
		t.Fatalf("AddIntegration: %v", err)
	}

	mw := srv.IPAllowlistMiddleware(IPAllowlist{Allowed: mustCIDRs(t, "10.0.0.0/8")})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	call := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = "8.8.8.8:1" // not in the allowlist
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res.Result().StatusCode
	}

	if code := call("/quickbooks/webhook"); code != http.StatusOK {
		t.Fatalf("relay path should be exempt from allowlist, got %d", code)
	}
	if code := call("/quickbooks/api/v1/synchronizer/data"); code != http.StatusForbidden {
		t.Fatalf("non-public path from disallowed IP should be 403, got %d", code)
	}
}

func TestServerHandle(t *testing.T) {
	srv := NewServer()
	if err := srv.Handle("GET /tos", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("tos"))
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tos", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Result().StatusCode != http.StatusOK || res.Body.String() != "tos" {
		t.Fatalf("static route: status=%d body=%q", res.Result().StatusCode, res.Body.String())
	}

	srv.started.Store(true)
	if err := srv.Handle("GET /privacy", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != ErrServerStarted {
		t.Fatalf("Handle after start = %v, want ErrServerStarted", err)
	}
}
