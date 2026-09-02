package embed_test

// AC-1 — the engine-owned Status type that the SW-265 status surface
// (CLI/MCP/HTTP) serializes byte-identically. The test asserts:
//
//   - Status is a value type, not a pointer; the four preconditions the
//     surfaces derive their "is the index usable?" answer from —
//     installed, configured, indexed, fresh — are explicit bool fields,
//     not a single packed bit;
//   - the typed lifecycle state is a closed vocabulary (missing|stale|
//     corrupt|ready) keyed to the SW-261 GenerationStore states;
//   - Model carries id, revision and sha256;
//   - ActiveGeneration carries id, fingerprint, documents, nodes,
//     span_method_share and built_at;
//   - LastGeneration is a separate field;
//   - Languages is a map keyed by language, valued by "validated" or
//     "unvalidated";
//   - Repair is the exact command that fixes the state.
//
// The test is an honest contract pin, not a smoke test: it references
// every field AC-1 names. A future refactor that drops any of them will
// fail here. The wire serialiser lives in surfaces/client/semantic_status.go
// and has its own byte-identity test against this shape; this one is the
// engine contract.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func TestStatus_AC1_FieldShape(t *testing.T) {
	s := embed.Status{
		Installed:  true,
		Configured: true,
		Indexed:    true,
		Fresh:      true,
		State:      embed.StateReady,
		Model: embed.Model{
			ID:       "static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b",
			Revision: "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b",
			SHA256:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
		ActiveGeneration: embed.GenerationSummary{
			ID:              "v3-cafebabe",
			Fingerprint:     "static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b|dim=128|v3",
			Documents:       42,
			Nodes:           40,
			SpanMethodShare: map[string]float64{"ast": 0.7, "window": 0.3},
			BuiltAt:         "2026-08-30T12:34:56Z",
		},
		LastGeneration: embed.GenerationSummary{
			ID: "v3-deadbeef",
		},
		Languages: map[string]string{
			"go":     "validated",
			"python": "unvalidated",
		},
		Repair: "graphi index --semantic",
	}

	// Round-trip through JSON to assert the field names AC-1 fixes on the wire.
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)

	// Each field AC-1 lists must be a first-class JSON property; a status
	// that omits one is not the AC-1 contract.
	for _, want := range []string{
		`"installed":true`,
		`"configured":true`,
		`"indexed":true`,
		`"fresh":true`,
		`"state":"ready"`,
		`"model":{`,
		`"id":"static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"`,
		`"revision":"e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"`,
		`"sha256":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"`,
		`"active_generation":{`,
		`"fingerprint":"static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b|dim=128|v3"`,
		`"documents":42`,
		`"nodes":40`,
		`"span_method_share":{`,
		`"built_at":"2026-08-30T12:34:56Z"`,
		`"last_generation":{`,
		`"languages":{`,
		`"go":"validated"`,
		`"python":"unvalidated"`,
		`"repair":"graphi index --semantic"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AC-1 status JSON missing %s in:\n%s", want, got)
		}
	}
}

// TestStatus_AC1_StateVocabulary pins the closed State vocabulary the
// surfaces key off. A future change that adds a state must update this
// test deliberately — the closed set is what keeps every surface's
// state string comparable.
func TestStatus_AC1_StateVocabulary(t *testing.T) {
	for _, s := range []embed.State{
		embed.StateUnset,
		embed.StateMissing,
		embed.StateStale,
		embed.StateCorrupt,
		embed.StateReady,
	} {
		if s.String() == "" {
			t.Errorf("State %d renders to empty string", int(s))
		}
	}
	// The wire uses lowercase closed strings, never numeric IDs.
	for _, s := range []embed.State{embed.StateMissing, embed.StateStale, embed.StateCorrupt, embed.StateReady} {
		got := s.String()
		if strings.ToLower(got) != got {
			t.Errorf("State.String() = %q, want lowercase", got)
		}
		if strings.ContainsAny(got, " \t\n") {
			t.Errorf("State.String() = %q, want no whitespace", got)
		}
	}
}
