package fibsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gateStub is a minimal IntegrationCore for exercising gate behavior.
type gateStub struct {
	cfg Config
}

func (g *gateStub) Init(context.Context, InitConfig) error { return nil }
func (g *gateStub) Config(context.Context) (Config, *IntegrationError) {
	return g.cfg, nil
}
func (g *gateStub) Validate(context.Context, ValidateRequest[testAccountFields]) (*testAccountFields, *IntegrationError) {
	return &testAccountFields{}, nil
}
func (g *gateStub) SyncConfig(context.Context, SyncMetaRequest[testAccountFields]) (SyncConfig, *IntegrationError) {
	return SyncConfig{}, nil
}
func (g *gateStub) SyncSchema(context.Context, SyncMetaRequest[testAccountFields]) (SyncSchema, *IntegrationError) {
	return SyncSchema{}, nil
}
func (g *gateStub) SyncData(context.Context, DataRequest[testAccountFields, testPage]) (DataResponse[testPage], *IntegrationError) {
	return DataResponse[testPage]{}, nil
}

func newGateHandler(stub *gateStub, opts ...HandlerOption) *integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}] {
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		integration: stub,
	}
	for _, opt := range opts {
		opt(&h.handlerOpts)
	}
	return h
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret(32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 32 bytes -> 43 chars unpadded base64url.
	if len(s1) != 43 {
		t.Fatalf("expected 43 chars for 32 bytes, got %d (%q)", len(s1), s1)
	}
	if strings.ContainsAny(s1, "+/=") {
		t.Fatalf("expected URL-safe unpadded output, got %q", s1)
	}
	s2, _ := GenerateSecret(32)
	if s1 == s2 {
		t.Fatalf("expected unique secrets, got duplicate %q", s1)
	}
	// Non-positive length defaults to 32 bytes.
	d, _ := GenerateSecret(0)
	if len(d) != 43 {
		t.Fatalf("expected default 32-byte secret (43 chars), got %d", len(d))
	}
}

func TestSecretSet(t *testing.T) {
	set := NewSecretSet("alpha", "beta")
	if !set.Valid("alpha") || !set.Valid("beta") {
		t.Fatalf("expected both member secrets to be valid")
	}
	if set.Valid("gamma") {
		t.Fatalf("non-member secret should be invalid")
	}
	if set.Valid("") {
		t.Fatalf("empty presented value should be invalid")
	}

	// Rotation: swap in a new set, old secrets become invalid.
	set.Set("gamma")
	if set.Valid("alpha") {
		t.Fatalf("old secret should be invalid after rotation")
	}
	if !set.Valid("gamma") {
		t.Fatalf("new secret should be valid after rotation")
	}

	// Empty set fails closed.
	if NewSecretSet().Valid("anything") {
		t.Fatalf("empty set should reject everything")
	}

	// Empty strings are ignored, not treated as valid secrets.
	mixed := NewSecretSet("", "x")
	if mixed.Valid("") {
		t.Fatalf("empty string should not be a valid secret")
	}
	if !mixed.Valid("x") {
		t.Fatalf("non-empty secret should remain valid")
	}
}

func TestWithAccountGateNormalization(t *testing.T) {
	t.Run("static secret", func(t *testing.T) {
		var o handlerOptions
		WithAccountGate(AccountGate{Secret: "s3cret", Title: "Pw"})(&o)
		if !o.gate.enabled {
			t.Fatalf("expected gate enabled")
		}
		if o.gate.validate == nil {
			t.Fatalf("expected validate to be set from Secret")
		}
		if !o.gate.validate("s3cret") {
			t.Fatalf("expected static secret to validate")
		}
		if o.gate.validate("nope") {
			t.Fatalf("expected wrong secret to fail")
		}
		if o.gate.title != "Pw" {
			t.Fatalf("expected title propagated, got %q", o.gate.title)
		}
	})

	t.Run("Validate takes precedence over Secret", func(t *testing.T) {
		var o handlerOptions
		WithAccountGate(AccountGate{
			Secret:   "ignored",
			Validate: func(p string) bool { return p == "v" },
		})(&o)
		if !o.gate.validate("v") {
			t.Fatalf("expected custom validator to accept its value")
		}
		if o.gate.validate("ignored") {
			t.Fatalf("Secret must not be consulted when Validate is set")
		}
	})
}

// TestAccountGateKeyRoundTrip guards against the injection key (the
// accountGateFieldID const, used in handleConfig) drifting out of sync with
// the extraction key (the JSON tag in Account.UnmarshalJSON). If they diverge,
// Fibery echoes the injected key but the framework reads a different one,
// silently extracting "" and rejecting every request with 401. Building the
// JSON from the const ties the two together so a desync fails here instead of
// in production.
func TestAccountGateKeyRoundTrip(t *testing.T) {
	body := fmt.Sprintf(`{"app":"a","%s":"secret"}`, accountGateFieldID)
	var acc Account[testAccountFields]
	if err := json.Unmarshal([]byte(body), &acc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acc.gate != "secret" {
		t.Fatalf("gate key %q not extracted: the injection const and the Account.UnmarshalJSON tag are out of sync", accountGateFieldID)
	}
}

func TestAccountUnmarshalGateAndAuth(t *testing.T) {
	var acc Account[testAccountFields]
	raw := `{"app":"a","auth":"token","token":"tok","_integration_gate":"g"}`
	if err := json.Unmarshal([]byte(raw), &acc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acc.Auth != "token" {
		t.Fatalf("expected Auth to be populated, got %q", acc.Auth)
	}
	if acc.gate != "g" {
		t.Fatalf("expected gate value g, got %q", acc.gate)
	}
	if acc.AccountFields.Token != "tok" {
		t.Fatalf("expected account fields decoded, got %q", acc.AccountFields.Token)
	}
}

func TestAccountGateConfigInjection(t *testing.T) {
	stub := &gateStub{cfg: Config{
		Authentication: []Authentication{
			{ID: AuthToken, Name: "Token"},
			{ID: AuthOAuth2, Name: "OAuth2"},
		},
	}}

	t.Run("injects into every auth method when enabled", func(t *testing.T) {
		h := newGateHandler(stub, WithAccountGate(AccountGate{Secret: "x", Title: "Access Password"}))
		res := httptest.NewRecorder()
		h.handleConfig(res, httptest.NewRequest(http.MethodGet, "/", nil))

		var got Config
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Authentication) != 2 {
			t.Fatalf("expected 2 auth methods, got %d", len(got.Authentication))
		}
		for _, a := range got.Authentication {
			var field *AuthField
			for idx := range a.Fields {
				if a.Fields[idx].ID == accountGateFieldID {
					field = &a.Fields[idx]
				}
			}
			if field == nil {
				t.Fatalf("gate field not injected into auth %q", a.ID)
			}
			if field.Type != ConfigPasswordField {
				t.Fatalf("expected password field type, got %q", field.Type)
			}
			if field.Title != "Access Password" {
				t.Fatalf("expected title, got %q", field.Title)
			}
		}
	})

	t.Run("no injection when disabled", func(t *testing.T) {
		h := newGateHandler(stub)
		res := httptest.NewRecorder()
		h.handleConfig(res, httptest.NewRequest(http.MethodGet, "/", nil))

		var got Config
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, a := range got.Authentication {
			for _, f := range a.Fields {
				if f.ID == accountGateFieldID {
					t.Fatalf("gate field should not be injected when disabled")
				}
			}
		}
	})
}

func TestAccountGateEnforcement(t *testing.T) {
	stub := &gateStub{}

	call := func(h *integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}], body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/synchronizer/config", bytes.NewBufferString(body))
		res := httptest.NewRecorder()
		h.handleSyncConfig(res, req)
		return res.Result().StatusCode
	}

	t.Run("correct secret passes", func(t *testing.T) {
		h := newGateHandler(stub, WithAccountGate(AccountGate{Secret: "letmein"}))
		if code := call(h, `{"account":{"app":"a","_integration_gate":"letmein"}}`); code != http.StatusOK {
			t.Fatalf("expected 200 with correct secret, got %d", code)
		}
	})

	t.Run("wrong secret rejected", func(t *testing.T) {
		h := newGateHandler(stub, WithAccountGate(AccountGate{Secret: "letmein"}))
		if code := call(h, `{"account":{"app":"a","_integration_gate":"wrong"}}`); code != http.StatusUnauthorized {
			t.Fatalf("expected 401 with wrong secret, got %d", code)
		}
	})

	t.Run("missing secret rejected", func(t *testing.T) {
		h := newGateHandler(stub, WithAccountGate(AccountGate{Secret: "letmein"}))
		if code := call(h, `{"account":{"app":"a"}}`); code != http.StatusUnauthorized {
			t.Fatalf("expected 401 with missing secret, got %d", code)
		}
	})

	t.Run("disabled gate passes without secret", func(t *testing.T) {
		h := newGateHandler(stub)
		if code := call(h, `{"account":{"app":"a"}}`); code != http.StatusOK {
			t.Fatalf("expected 200 when gate disabled, got %d", code)
		}
	})
}

func TestAddIntegrationGateFailClosed(t *testing.T) {
	srv := NewServer()
	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), srv, &gateStub{}, WithAccountGate(AccountGate{Title: "no secret"}),
	)
	if err == nil {
		t.Fatalf("expected error when gate enabled without Secret or Validate")
	}
}

// oauthStub records whether the integration's OAuth authorize was reached.
type oauthStub struct {
	gateStub
	reached bool
}

func (o *oauthStub) Oauth1Authorize(context.Context, Oauth1AuthRequest) (Oauth1AuthResponse, *IntegrationError) {
	o.reached = true
	return Oauth1AuthResponse{RedirectURI: "https://provider/authorize"}, nil
}
func (o *oauthStub) Oauth1Token(context.Context, Oauth1TokenRequest[struct{}]) (testAccountFields, *IntegrationError) {
	return testAccountFields{}, nil
}
func (o *oauthStub) Oauth2Authorize(context.Context, Oauth2AuthRequest) (Oauth2AuthResponse, *IntegrationError) {
	o.reached = true
	return Oauth2AuthResponse{RedirectURI: "https://provider/authorize"}, nil
}
func (o *oauthStub) Oauth2TokenRequest(context.Context, Oauth2TokenRequest[struct{}]) (testAccountFields, *IntegrationError) {
	return testAccountFields{}, nil
}

// The authorize requests carry the auth form's values, gate included, so a
// wrong password must stop the flow before the provider's consent screen.
func TestAccountGateOAuthAuthorize(t *testing.T) {
	type handler = integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]

	endpoints := []struct {
		name string
		body string
		call func(h *handler, stub *oauthStub) http.HandlerFunc
	}{
		{
			name: "oauth1",
			body: `{"callback_uri":"https://cb"%s}`,
			call: func(h *handler, stub *oauthStub) http.HandlerFunc { return h.handleOauth1Authorize(stub) },
		},
		{
			name: "oauth2",
			body: `{"callback_uri":"https://cb","state":"s","auth":"oauth2"%s}`,
			call: func(h *handler, stub *oauthStub) http.HandlerFunc { return h.handleOauth2Authorize(stub) },
		},
	}

	cases := []struct {
		name     string
		gate     bool
		fields   string
		wantCode int
	}{
		{name: "correct secret passes", gate: true, fields: `,"fields":{"_integration_gate":"letmein"}`, wantCode: http.StatusOK},
		{name: "wrong secret rejected", gate: true, fields: `,"fields":{"_integration_gate":"wrong"}`, wantCode: http.StatusUnauthorized},
		{name: "missing secret rejected", gate: true, fields: `,"fields":{}`, wantCode: http.StatusUnauthorized},
		{name: "missing fields rejected", gate: true, fields: ``, wantCode: http.StatusUnauthorized},
		{name: "non-string secret rejected", gate: true, fields: `,"fields":{"_integration_gate":1}`, wantCode: http.StatusUnauthorized},
		{name: "disabled gate passes", gate: false, fields: ``, wantCode: http.StatusOK},
	}

	for _, ep := range endpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				stub := &oauthStub{}
				var opts []HandlerOption
				if tc.gate {
					opts = append(opts, WithAccountGate(AccountGate{Secret: "letmein"}))
				}
				h := newGateHandler(&stub.gateStub, opts...)

				body := fmt.Sprintf(ep.body, tc.fields)
				res := httptest.NewRecorder()
				ep.call(h, stub)(res, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))

				if res.Code != tc.wantCode {
					t.Fatalf("expected %d, got %d: %s", tc.wantCode, res.Code, res.Body.String())
				}
				if want := tc.wantCode == http.StatusOK; stub.reached != want {
					t.Fatalf("integration reached = %v, want %v", stub.reached, want)
				}
			})
		}
	}
}
