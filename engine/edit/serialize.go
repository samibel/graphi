package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalInlineResult is the single canonical serializer for an InlineResult,
// shared by every surface (CLI, MCP, HTTP, daemon). It is the ONLY place an
// InlineResult becomes bytes, so all surfaces emit byte-identical output for
// identical results by construction. The result's slices are already built in a
// deterministic order (files sorted, ops span-ordered); HTML escaping is disabled
// and the trailing newline trimmed so repeated/ cross-surface marshals match.
func MarshalInlineResult(r InlineResult) ([]byte, error) {
	// PlannedOps is internal (json:"-"); the wire shape excludes it. Marshal the
	// value as-is — its exported fields are deterministic.
	return marshalCanonical(r, "inline result")
}

// MarshalSafeDeleteResult is the single canonical serializer for a
// SafeDeleteResult, shared by every surface. BlockingRefs and NewlyDead are
// already canonically ordered by ApplySafeDelete; this serializer is the single
// byte-source so every surface is byte-identical.
func MarshalSafeDeleteResult(r SafeDeleteResult) ([]byte, error) {
	return marshalCanonical(r, "safe-delete result")
}

// marshalCanonical encodes v with HTML escaping disabled and the trailing newline
// trimmed — the shared byte-stable encoding used by every surface result type.
func marshalCanonical(v any, what string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("edit: marshal %s: %w", what, err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
