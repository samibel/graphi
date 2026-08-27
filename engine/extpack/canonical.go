package extpack

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SW-230 (AX-10) — a canonical byte form for the merged pack set.
//
// The determinism claim SW-229 made is that merge order is a function of the
// LOCKFILE CONTENT, never of install order. That claim was proven by comparing
// the individual accessors. The conformance harness has to compare two whole
// merges — twice over, and from a pack author's machine — so it needs one byte
// form to compare, and it needs that form to be the property under test rather
// than a rendering invented at comparison time.
//
// Canonical is that form: the merged view exactly as the accessors expose it, in
// exactly the order sortAll pinned, encoded by encoding/json (which sorts object
// keys). Nothing here re-sorts, re-shapes or omits — a Canonical that fixed up an
// ordering would be hiding the defect it exists to find.

// canonicalSet is the wire shape of a merged pack set. The field order is fixed
// by the struct; every slice is already in (pack id, item id) order.
type canonicalSet struct {
	Packs       []Ref            `json:"packs"`
	ArchRules   []ArchRule       `json:"architecture_rules"`
	TaintSource []TaintSource    `json:"taint_sources"`
	TaintSink   []TaintSink      `json:"taint_sinks"`
	Sanitizers  []TaintSanitizer `json:"taint_sanitizers"`
}

// Canonical renders the merged pack set as deterministic, indented JSON.
//
// A nil or empty Set renders the empty document rather than failing: "no packs"
// is a state the harness compares like any other, and a byte form that could not
// express it would make the commonest case the untestable one.
func (s *Set) Canonical() ([]byte, error) {
	doc := canonicalSet{
		Packs:       nonNilRefs(s.Refs()),
		ArchRules:   s.ArchRules(),
		TaintSource: s.TaintSources(),
		TaintSink:   s.TaintSinks(),
		Sanitizers:  s.TaintSanitizers(),
	}
	if doc.ArchRules == nil {
		doc.ArchRules = []ArchRule{}
	}
	if doc.TaintSource == nil {
		doc.TaintSource = []TaintSource{}
	}
	if doc.TaintSink == nil {
		doc.TaintSink = []TaintSink{}
	}
	if doc.Sanitizers == nil {
		doc.Sanitizers = []TaintSanitizer{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("extpack: encode canonical pack set: %w", err)
	}
	return buf.Bytes(), nil
}

func nonNilRefs(refs []Ref) []Ref {
	if refs == nil {
		return []Ref{}
	}
	return refs
}
