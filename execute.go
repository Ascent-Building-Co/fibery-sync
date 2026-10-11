package fibsync

import (
	"encoding/json"
	"fmt"
)

// Fibery→source dispatch.
//
// Fibery POSTs to {prefix}/api/v1/synchronizer/execute when a user edits a
// writable field, and again to deliver an ActionSync result. Undocumented;
// shapes captured from a live workspace.

// ExecuteRequest is what Fibery POSTs, once per edit. Fibery re-reads the sync
// config immediately beforehand, so changes to SyncConfigType.Actions take
// effect on the next edit rather than the next resync.
type ExecuteRequest[A any] struct {
	Account Account[A]     `json:"account"`
	Filter  map[string]any `json:"filter"`
	Types   []string       `json:"types"`
	Action  ExecuteAction  `json:"action"`
}

// ExecuteAction maps an action name to its payload, grouped by key. Payloads stay
// raw because the shape varies by action: ExecuteUpdate carries entities keyed by
// type ID (see Entities), ExecuteRefresh whatever the Action returned (see IDs).
type ExecuteAction map[ExecuteActionName]map[string]json.RawMessage

// IDs decodes a payload as string slices keyed by type ID — the ExecuteRefresh
// convention, where an Action returning {"<typeID>": "<sourceID>"} accumulates
// into {"<typeID>": ["id1","id2"]}. Fails if the values are not strings.
func (a ExecuteAction) IDs(name ExecuteActionName) (map[string][]string, error) {
	payload, ok := a[name]
	if !ok {
		return nil, nil
	}
	out := make(map[string][]string, len(payload))
	for key, raw := range payload {
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, fmt.Errorf("action %q key %q: %w", name, key, err)
		}
		out[key] = ids
	}
	return out, nil
}

// Entities decodes a payload as entities keyed by type ID, for ExecuteUpdate. It
// errors rather than skipping an undecodable key, so an unexpected shape surfaces
// instead of silently dropping an edit.
func (a ExecuteAction) Entities(name ExecuteActionName) (map[string][]ExecuteEntity, error) {
	payload, ok := a[name]
	if !ok {
		return nil, nil
	}
	out := make(map[string][]ExecuteEntity, len(payload))
	for key, raw := range payload {
		var entities []ExecuteEntity
		if err := json.Unmarshal(raw, &entities); err != nil {
			return nil, fmt.Errorf("action %q key %q: %w", name, key, err)
		}
		out[key] = entities
	}
	return out, nil
}

// ExecuteActionName is an action declared in SyncConfigType.Actions and used as
// the key of ExecuteRequest.Action.
type ExecuteActionName string

const (
	// ExecuteUpdate carries a user's edit of a writable field, keyed by type ID.
	// Creation and deletion stay source-owned: declaring "create" or "delete" is
	// silently ignored — use an Action that writes to the source instead.
	ExecuteUpdate ExecuteActionName = "update"

	// ExecuteRefresh delivers an ActionSync result: the Action's Data, accumulated
	// across the batch. A dispatch back to the integration, not a sync — no
	// SyncData call follows.
	ExecuteRefresh ExecuteActionName = "refresh"
)

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

// ExecuteResponse is the reply to an execute call. Fibery ignores it.
type ExecuteResponse struct {
	Data any `json:"data,omitempty"`
}
