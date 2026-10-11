package fibsync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// executeStub is a minimal integration that also implements
// IntegrationWithExecute, so route registration and dispatch can be exercised.
type executeStub struct {
	minimalIntegration
	got  ExecuteRequest[testAccountFields]
	resp ExecuteResponse
	err  *IntegrationError
}

func (e *executeStub) Execute(_ context.Context, req ExecuteRequest[testAccountFields]) (ExecuteResponse, *IntegrationError) {
	e.got = req
	return e.resp, e.err
}

// capturedExecuteBody is the payload observed from a live workspace on
// 2026-09-06, with a scalar field added alongside the relation.
const capturedExecuteBody = `{
  "account": {"_id":"6a9df71009920ab8e25945d3","app":"6a9df70a83fa9998bb0c3985","auth":"token","enabled":true,"token":"token1"},
  "action": {"update":{"task":[{"id":"t-2","fields":{"statusId":{"id":"s-done","name":"Done"},"dueDate":"2026-11-18"}}]}},
  "filter": {"enable_sync_source": true},
  "types": ["task","status","user"]
}`

func newExecuteServer(t *testing.T, integ any) *Server {
	t.Helper()
	server := NewServer()
	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), server, integ.(IntegrationCore[testAccountFields, testPage]), WithIntegrationID("qb"),
	)
	if err != nil {
		t.Fatalf("AddIntegration: %v", err)
	}
	return server
}

func postExecute(t *testing.T, server *Server, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/qb/api/v1/synchronizer/execute", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res.Result()
}

// The route is opt-in: an integration that does not implement the interface
// must not answer on it.
func TestExecuteRouteIsOptIn(t *testing.T) {
	t.Run("registered when implemented", func(t *testing.T) {
		server := newExecuteServer(t, &executeStub{})
		if code := postExecute(t, server, capturedExecuteBody).StatusCode; code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
	})

	t.Run("absent when not implemented", func(t *testing.T) {
		server := newExecuteServer(t, minimalIntegration{})
		if code := postExecute(t, server, capturedExecuteBody).StatusCode; code == http.StatusOK {
			t.Fatal("execute should not be routed for an integration without IntegrationWithExecute")
		}
	})
}

// Pinned to the real payload: if Fibery changes shape, this is what fails.
func TestExecuteRequestDecodesCapturedPayload(t *testing.T) {
	stub := &executeStub{}
	server := newExecuteServer(t, stub)

	if code := postExecute(t, server, capturedExecuteBody).StatusCode; code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	got := stub.got
	if got.Account.App != "6a9df70a83fa9998bb0c3985" {
		t.Errorf("account not decoded: %+v", got.Account)
	}
	if got.Account.AccountFields.Token != "token1" {
		t.Errorf("account fields not decoded: %+v", got.Account.AccountFields)
	}
	if len(got.Types) != 3 {
		t.Errorf("want 3 types, got %v", got.Types)
	}
	if got.Filter["enable_sync_source"] != true {
		t.Errorf("filter not decoded: %v", got.Filter)
	}

	updates, err := got.Action.Entities(ExecuteUpdate)
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	entities := updates["task"]
	if len(entities) != 1 || entities[0].ID != "t-2" {
		t.Fatalf("want one entity t-2, got %+v", entities)
	}

	// Relations arrive as {id, name}; scalars are plain.
	ref, ok := AsRelation(entities[0].Fields["statusId"])
	if !ok {
		t.Fatalf("statusId should be a relation ref, got %#v", entities[0].Fields["statusId"])
	}
	if ref.ID != "s-done" || ref.Name != "Done" {
		t.Errorf("want {s-done Done}, got %+v", ref)
	}
	if _, ok := AsRelation(entities[0].Fields["dueDate"]); ok {
		t.Error("a scalar field must not be read as a relation ref")
	}
}

// The refresh dispatch carries whatever the Action returned, not entities.
// Typing the payload as []ExecuteEntity made this body fail to decode at all,
// 400ing every refresh action — so it is pinned to the captured shape.
func TestExecuteDecodesRefreshDispatch(t *testing.T) {
	const body = `{
	  "account": {"app":"a","token":"token1"},
	  "action": {"refresh": {"touched": [1]}},
	  "filter": {},
	  "types": ["task"]
	}`

	var req ExecuteRequest[testAccountFields]
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("a refresh dispatch must decode: %v", err)
	}

	payload, ok := req.Action[ExecuteRefresh]
	if !ok {
		t.Fatalf("refresh action missing: %v", req.Action)
	}
	if string(payload["touched"]) != "[1]" {
		t.Errorf("payload should stay raw, got %s", payload["touched"])
	}
}

// Asking for entities from a payload that is not entities must report, not panic
// or silently drop.
func TestEntitiesReportsUndecodablePayload(t *testing.T) {
	var req ExecuteRequest[testAccountFields]
	if err := json.Unmarshal([]byte(`{"action":{"refresh":{"touched":[1]}}}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, err := req.Action.Entities(ExecuteRefresh); err == nil {
		t.Error("expected an error decoding a non-entity payload as entities")
	}
}

// An action that is not present yields nothing, not an error.
func TestEntitiesAbsentActionIsNotAnError(t *testing.T) {
	var req ExecuteRequest[testAccountFields]
	if err := json.Unmarshal([]byte(`{"action":{"refresh":{}}}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, err := req.Action.Entities(ExecuteUpdate)
	if err != nil {
		t.Errorf("absent action should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want nothing, got %v", got)
	}
}

// Results accumulate across a batch: one invocation per entity, one dispatch.
func TestRefreshDispatchAccumulatesAcrossBatch(t *testing.T) {
	const body = `{
	  "account": {"app":"a","token":"token1"},
	  "action": {"refresh": {"task": ["t-1","t-2","t-3"]}},
	  "types": ["task"]
	}`

	var req ExecuteRequest[testAccountFields]
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	refreshed, err := req.Action.IDs(ExecuteRefresh)
	if err != nil {
		t.Fatalf("IDs: %v", err)
	}
	if got := refreshed["task"]; len(got) != 3 || got[0] != "t-1" || got[2] != "t-3" {
		t.Errorf("want the whole batch, got %v", got)
	}
}

// An Action returning non-string data still decodes as a request; only the IDs
// helper declines it.
func TestIDsReportsNonStringPayload(t *testing.T) {
	var req ExecuteRequest[testAccountFields]
	if err := json.Unmarshal([]byte(`{"action":{"refresh":{"touched":[1,1]}}}`), &req); err != nil {
		t.Fatalf("a numeric batch must still decode: %v", err)
	}
	if _, err := req.Action.IDs(ExecuteRefresh); err == nil {
		t.Error("expected an error decoding numbers as ids")
	}
}

func TestAsRelation(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want RelationRef
		ok   bool
	}{
		{"relation", map[string]any{"id": "s-1", "name": "Open"}, RelationRef{ID: "s-1", Name: "Open"}, true},
		{"relation without name", map[string]any{"id": "s-1"}, RelationRef{ID: "s-1"}, true},
		{"string scalar", "2026-11-18", RelationRef{}, false},
		{"number scalar", float64(3), RelationRef{}, false},
		{"nil", nil, RelationRef{}, false},
		{"object without id", map[string]any{"name": "Open"}, RelationRef{}, false},
		{"non-string id", map[string]any{"id": float64(1)}, RelationRef{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := AsRelation(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("got (%+v, %t), want (%+v, %t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestExecuteErrorSurfacesStatus(t *testing.T) {
	stub := &executeStub{err: &IntegrationError{Status: http.StatusTeapot, Message: "nope"}}
	server := newExecuteServer(t, stub)

	if code := postExecute(t, server, capturedExecuteBody).StatusCode; code != http.StatusTeapot {
		t.Errorf("want 418 from the integration error, got %d", code)
	}
}

func TestExecuteResponseMarshalsToEmptyObject(t *testing.T) {
	b, err := json.Marshal(ExecuteResponse{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "{}" {
		t.Errorf("want {}, got %s", b)
	}
}

func TestExecuteRespectsAccountGate(t *testing.T) {
	stub := &executeStub{}
	h := &integrationHandler[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}]{
		integration: stub,
	}
	WithAccountGate(AccountGate{Secret: "letmein"})(&h.handlerOpts)

	call := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/synchronizer/execute", bytes.NewBufferString(body))
		res := httptest.NewRecorder()
		h.handleExecute(stub)(res, req)
		return res.Result().StatusCode
	}

	if code := call(`{"account":{"app":"a","_integration_gate":"letmein"},"action":{}}`); code != http.StatusOK {
		t.Errorf("correct secret should pass, got %d", code)
	}
	if code := call(`{"account":{"app":"a","_integration_gate":"wrong"},"action":{}}`); code != http.StatusUnauthorized {
		t.Errorf("wrong secret should be rejected, got %d", code)
	}
}

// The regression guard for every existing integration: a type that declares no
// actions must serialize exactly as it did before these fields were added.
func TestSyncConfigTypeUnchangedWhenNotOptedIn(t *testing.T) {
	b, err := json.Marshal(SyncConfigType{ID: "status", Name: "Status"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"id":"status","name":"Status"}` {
		t.Errorf("output changed for a non-opted-in type: %s", b)
	}
}

func TestSyncConfigTypeUpdatable(t *testing.T) {
	b, err := json.Marshal(SyncConfigType{ID: "task", Name: "Task", Default: true, VerifyEntitiesExist: true}.Updatable())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"id":"task","name":"Task","default":true,"verifyEntitiesExist":true,"actions":{"update":{}}}`
	if string(b) != want {
		t.Errorf("\ngot  %s\nwant %s", b, want)
	}
}

// Updatable must not clobber actions already declared.
func TestUpdatablePreservesExistingActions(t *testing.T) {
	base := SyncConfigType{ID: "task", Name: "Task", Actions: SyncTypeActions{"custom": {}}}
	got := base.Updatable()

	if _, ok := got.Actions["custom"]; !ok {
		t.Error("existing action was dropped")
	}
	if _, ok := got.Actions[ExecuteUpdate]; !ok {
		t.Error("update was not added")
	}
}

// Fibery assigns the app id at registration, so Config must not send one.
func TestConfigDoesNotEmitID(t *testing.T) {
	b, err := json.Marshal(Config{Name: "name", Version: "1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["id"]; present {
		t.Errorf("config should not carry an id: %s", b)
	}
}

// Both fields arrive on every sync after the first; the checkpoint has only
// ever been null, so a null must leave it zero rather than failing the decode.
func TestDataRequestDecodesSyncCheckpointFields(t *testing.T) {
	const body = `{
	  "requestedType": "task",
	  "lastSynchronizedAt": "2026-09-07T01:36:49.000Z",
	  "lastFullSynchronizedAt": "2026-09-07T01:36:52.187Z",
	  "lastSynchronizationCheckpoint": null
	}`

	var req DataRequest[testAccountFields, testPage]
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantFull := time.Date(2026, 9, 7, 1, 36, 52, 187000000, time.UTC)
	if !req.LastFullSynchronizedAt.Equal(wantFull) {
		t.Errorf("lastFullSynchronizedAt: got %v, want %v", req.LastFullSynchronizedAt, wantFull)
	}
	if !req.LastSynchronizedAt.Before(req.LastFullSynchronizedAt) {
		t.Errorf("the two timestamps should decode independently: %v vs %v",
			req.LastSynchronizedAt, req.LastFullSynchronizedAt)
	}
	if string(req.LastSynchronizationCheckpoint) != "null" {
		t.Errorf("a null checkpoint should decode as JSON null, got %q", req.LastSynchronizationCheckpoint)
	}
}

// The first sync of an account carries neither field.
func TestDataRequestFirstSyncHasNoCheckpointFields(t *testing.T) {
	var req DataRequest[testAccountFields, testPage]
	if err := json.Unmarshal([]byte(`{"requestedType":"task","lastSynchronizedAt":null}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !req.LastFullSynchronizedAt.IsZero() {
		t.Errorf("want zero, got %v", req.LastFullSynchronizedAt)
	}
	if req.LastSynchronizationCheckpoint != nil {
		t.Errorf("want nil, got %q", req.LastSynchronizationCheckpoint)
	}
}
