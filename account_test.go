package fibsync

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// deletionStub is a minimal integration that also implements
// IntegrationWithAccountDeletion.
type deletionStub struct {
	minimalIntegration
	called bool
	got    Account[testAccountFields]
	err    *IntegrationError
}

func (d *deletionStub) DeleteAccount(_ context.Context, account Account[testAccountFields]) *IntegrationError {
	d.called = true
	d.got = account
	return d.err
}

// The documented payload: the account itself, not an envelope around it.
const accountDeletionBody = `{
  "_id": "6a9df71009920ab8e25945d3",
  "app": "6a9df70a83fa9998bb0c3985",
  "auth": "token",
  "enabled": true,
  "owner": "63991dfce0a3c45e5b6b3704",
  "name": "Joe Doe",
  "token": "user-token"
}`

func newDeletionServer(t *testing.T, integ IntegrationCore[testAccountFields, testPage]) *Server {
	t.Helper()
	server := NewServer()
	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), server, integ, WithIntegrationID("qb"),
	)
	if err != nil {
		t.Fatalf("AddIntegration: %v", err)
	}
	return server
}

func deleteAccount(t *testing.T, server *Server, method, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/qb/api/v1/account", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res.Result()
}

func TestAccountDeletionRouteIsOptIn(t *testing.T) {
	t.Run("registered when implemented", func(t *testing.T) {
		stub := &deletionStub{}
		server := newDeletionServer(t, stub)

		if code := deleteAccount(t, server, http.MethodDelete, accountDeletionBody).StatusCode; code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if !stub.called {
			t.Error("DeleteAccount was not called")
		}
	})

	t.Run("absent when not implemented", func(t *testing.T) {
		server := newDeletionServer(t, minimalIntegration{})
		if code := deleteAccount(t, server, http.MethodDelete, accountDeletionBody).StatusCode; code == http.StatusOK {
			t.Fatal("the route should not exist without IntegrationWithAccountDeletion")
		}
	})
}

// The route is DELETE only; the path must not answer other methods.
func TestAccountDeletionIsDeleteOnly(t *testing.T) {
	server := newDeletionServer(t, &deletionStub{})

	if code := deleteAccount(t, server, http.MethodPost, accountDeletionBody).StatusCode; code == http.StatusOK {
		t.Errorf("POST should not be routed to account deletion, got %d", code)
	}
}

// The payload is flat, so base fields and custom auth fields must both land.
func TestAccountDeletionDecodesBareAccount(t *testing.T) {
	stub := &deletionStub{}
	server := newDeletionServer(t, stub)

	if code := deleteAccount(t, server, http.MethodDelete, accountDeletionBody).StatusCode; code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	if stub.got.ID != "6a9df71009920ab8e25945d3" {
		t.Errorf("_id not decoded: %q", stub.got.ID)
	}
	if stub.got.App != "6a9df70a83fa9998bb0c3985" {
		t.Errorf("app not decoded: %q", stub.got.App)
	}
	if stub.got.Auth != "token" {
		t.Errorf("auth not decoded: %q", stub.got.Auth)
	}
	if !stub.got.Enabled {
		t.Error("enabled not decoded")
	}
	// The custom auth field is the one the integration actually needs in order
	// to revoke anything.
	if stub.got.AccountFields.Token != "user-token" {
		t.Errorf("custom auth field not decoded: %+v", stub.got.AccountFields)
	}
}

// A returned error becomes a non-2xx, which is what aborts deletion in Fibery.
func TestAccountDeletionErrorAbortsDeletion(t *testing.T) {
	stub := &deletionStub{err: &IntegrationError{Status: http.StatusBadGateway, Message: "revoke failed"}}
	server := newDeletionServer(t, stub)

	if code := deleteAccount(t, server, http.MethodDelete, accountDeletionBody).StatusCode; code != http.StatusBadGateway {
		t.Errorf("want 502 from the integration error, got %d", code)
	}
}

// The gate value rides along as a custom auth field, so it is enforced here
// like on every other account-bearing route.
func TestAccountDeletionRespectsAccountGate(t *testing.T) {
	stub := &deletionStub{}
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		integration: stub,
	}
	WithAccountGate(AccountGate{Secret: "letmein"})(&h.handlerOpts)

	call := func(body string) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/account", bytes.NewBufferString(body))
		res := httptest.NewRecorder()
		h.handleAccountDeletion(stub)(res, req)
		return res.Result().StatusCode
	}

	if code := call(`{"app":"a","token":"t","_integration_gate":"letmein"}`); code != http.StatusOK {
		t.Errorf("correct secret should pass, got %d", code)
	}
	if code := call(`{"app":"a","token":"t","_integration_gate":"wrong"}`); code != http.StatusUnauthorized {
		t.Errorf("wrong secret should be rejected, got %d", code)
	}
	if code := call(`{"app":"a","token":"t"}`); code != http.StatusUnauthorized {
		t.Errorf("missing secret should be rejected, got %d", code)
	}
}

// A malformed or empty body must not reach the integration: cleanup would have
// nothing to act on, and a 400 tells Fibery the call failed.
func TestAccountDeletionRejectsBadBody(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"malformed", `{"token":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &deletionStub{}
			server := newDeletionServer(t, stub)

			if code := deleteAccount(t, server, http.MethodDelete, tc.body).StatusCode; code != http.StatusBadRequest {
				t.Errorf("want 400, got %d", code)
			}
			if stub.called {
				t.Error("DeleteAccount must not be called for an undecodable body")
			}
		})
	}
}
