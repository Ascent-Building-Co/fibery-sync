package fibsync

// Fibery→source write-back.
//
// When a user edits a writable field, Fibery POSTs the change to
// {prefix}/api/v1/synchronizer/execute. The endpoint is undocumented; the shape
// below was captured from a live workspace.

// ExecuteRequest is the payload Fibery POSTs to /api/v1/synchronizer/execute
// when a user edits a writable field, once per edit. Fibery re-reads the sync
// config immediately beforehand, so changes to SyncConfigType.Actions take
// effect on the next edit rather than the next resync.
type ExecuteRequest[A any] struct {
	Account Account[A]     `json:"account"`
	Filter  map[string]any `json:"filter"`
	Types   []string       `json:"types"`
	Action  ExecuteAction  `json:"action"`
}

// ExecuteAction maps an action name to the affected entities, grouped by type
// ID. One call may carry several types and several entities.
type ExecuteAction map[ExecuteActionName]map[string][]ExecuteEntity

// ExecuteActionName is an action declared in SyncConfigType.Actions and used as
// the key of ExecuteRequest.Action.
type ExecuteActionName string

// ExecuteUpdate is the only action Fibery sends. Entity creation and deletion
// remain source-owned: use an Action (IntegrationWithAction) that writes to the
// source, after which the entity appears on the next sync.
const ExecuteUpdate ExecuteActionName = "update"

// ExecuteEntity is one entity in an execute action. Fields carries only the
// fields the user changed, unlike SyncData where entities are applied whole.
type ExecuteEntity struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// RelationRef is the envelope Fibery sends for a relation field's value. Scalar
// fields arrive as plain JSON values.
type RelationRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AsRelation reports whether v is a relation reference and returns it:
//
//	if ref, ok := fibsync.AsRelation(fields["statusId"]); ok {
//		row.StatusID = ref.ID
//	}
func AsRelation(v any) (RelationRef, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return RelationRef{}, false
	}
	id, ok := obj["id"].(string)
	if !ok {
		return RelationRef{}, false
	}
	name, _ := obj["name"].(string)
	return RelationRef{ID: id, Name: name}, true
}

// ExecuteResponse is the reply to an execute call. Fibery accepts an empty
// object; no richer response contract is known.
type ExecuteResponse struct {
	Data any `json:"data,omitempty"`
}
