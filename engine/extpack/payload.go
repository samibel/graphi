package extpack

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capability-key namespaces. A capability key is "<namespace>:<name>", and the
// namespace is fixed by the pack KIND — a taint pack cannot provide an
// architecture rule. Namespacing is also what keeps a pack-provided item from
// colliding with a built-in finding id in a consuming analyzer: every
// pack-derived item is emitted under its namespace, and no built-in uses one.
const (
	NSArchitectureRule = "architecture-rule"
	NSTaintSource      = "taint-source"
	NSTaintSink        = "taint-sink"
	NSTaintSanitizer   = "taint-sanitizer"
)

// namespacesForKind maps a pack kind to the capability namespaces it may use.
func namespacesForKind(k Kind) []string {
	switch k {
	case KindArchitectureRules:
		return []string{NSArchitectureRule}
	case KindTaintRules:
		return []string{NSTaintSource, NSTaintSink, NSTaintSanitizer}
	}
	return nil
}

// maxNameLength bounds the name half of a capability key and every rule id.
const maxNameLength = 64

func validateCapabilityKey(kind Kind, key string) error {
	ns, name, ok := strings.Cut(key, ":")
	if !ok {
		return fmt.Errorf("extpack: capability key %q is not \"<namespace>:<name>\"", Bound(key))
	}
	allowed := namespacesForKind(kind)
	found := false
	for _, a := range allowed {
		if ns == a {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("extpack: capability key %q uses namespace %q, which a %q pack may not provide (allowed: %s)",
			Bound(key), Bound(ns), kind, strings.Join(allowed, ", "))
	}
	return validateName(name)
}

// validateName checks a rule / definition id. Same character discipline as a
// pack id and for the same reason: these strings are emitted into artifacts and
// compared as keys, so they are restricted rather than escaped.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("extpack: empty rule id")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("extpack: rule id is %d bytes, the limit is %d", len(name), maxNameLength)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '_' || c == '-':
		default:
			return fmt.Errorf("extpack: rule id %q contains %q: rule ids are [0-9A-Za-z_-]", Bound(name), string(c))
		}
	}
	return nil
}

// maxTextLength bounds free prose a pack ships (rule descriptions, taint match
// patterns). Shorter than MaxFieldLength on purpose: these are repeated per rule
// and per pattern, so the aggregate matters as much as the individual value.
const maxTextLength = 160

func validateText(field, s string) error {
	if s == "" {
		return fmt.Errorf("extpack: %s is empty", field)
	}
	if len(s) > maxTextLength {
		return fmt.Errorf("extpack: %s is %d bytes, the limit is %d", field, len(s), maxTextLength)
	}
	return nil
}

// ArchRule is one declared architecture constraint: unit From must not depend on
// unit To. From and To are matched against an architecture unit's LABEL, which
// in graphi's community view is the dominant path prefix of the unit.
type ArchRule struct {
	ID          string `yaml:"id" json:"id"`
	From        string `yaml:"from" json:"from"`
	To          string `yaml:"to" json:"to"`
	Description string `yaml:"description" json:"description"`

	// Pack is the provenance of the pack this rule came from. It is filled in by
	// the loader, never by the pack, and is what a consuming analyzer renders so
	// a pack-influenced finding is distinguishable from a first-party one.
	Pack Ref `yaml:"-" json:"pack"`
}

// ArchPayload is the architecture-rules artifact.
type ArchPayload struct {
	Version string     `yaml:"version" json:"version"`
	Rules   []ArchRule `yaml:"rules" json:"rules"`
}

// TaintSource / TaintSink / TaintSanitizer mirror engine/analysis/taint's
// definition shapes.
//
// They are MIRRORS rather than the taint types themselves, deliberately: this
// package must not import the analyzers it parameterises, or the pack model
// acquires a dependency on every consumer it grows. The taint package owns the
// conversion, which is also where the built-in-shadowing refusal lives.
type TaintSource struct {
	ID           string   `yaml:"id" json:"id"`
	Label        string   `yaml:"label" json:"label"`
	NodeKinds    []string `yaml:"node_kinds,omitempty" json:"node_kinds,omitempty"`
	NamePatterns []string `yaml:"name_patterns,omitempty" json:"name_patterns,omitempty"`

	// Pack is the loader-filled provenance, never a pack-declared field.
	Pack Ref `yaml:"-" json:"pack"`
}

// TaintSink mirrors taint.SinkDef.
type TaintSink struct {
	ID           string   `yaml:"id" json:"id"`
	Category     string   `yaml:"category" json:"category"`
	NodeKinds    []string `yaml:"node_kinds,omitempty" json:"node_kinds,omitempty"`
	NamePatterns []string `yaml:"name_patterns,omitempty" json:"name_patterns,omitempty"`

	// Pack is the loader-filled provenance, never a pack-declared field.
	Pack Ref `yaml:"-" json:"pack"`
}

// TaintSanitizer mirrors taint.SanitizerDef.
type TaintSanitizer struct {
	ID           string   `yaml:"id" json:"id"`
	NamePatterns []string `yaml:"name_patterns" json:"name_patterns"`
	RemoveLabels []string `yaml:"remove_labels,omitempty" json:"remove_labels,omitempty"`

	// Pack is the loader-filled provenance, never a pack-declared field.
	Pack Ref `yaml:"-" json:"pack"`
}

// TaintPayload is the taint-rules artifact.
type TaintPayload struct {
	Version    string           `yaml:"version" json:"version"`
	Sources    []TaintSource    `yaml:"sources,omitempty" json:"sources,omitempty"`
	Sinks      []TaintSink      `yaml:"sinks,omitempty" json:"sinks,omitempty"`
	Sanitizers []TaintSanitizer `yaml:"sanitizers,omitempty" json:"sanitizers,omitempty"`
}

// payload is the decoded artifact of one pack, keyed by kind.
type payload struct {
	arch  ArchPayload
	taint TaintPayload
}

// provides returns the capability keys the artifact actually defines, in
// canonical order.
func (p payload) provides(kind Kind) []string {
	var out []string
	switch kind {
	case KindArchitectureRules:
		for _, r := range p.arch.Rules {
			out = append(out, NSArchitectureRule+":"+r.ID)
		}
	case KindTaintRules:
		for _, s := range p.taint.Sources {
			out = append(out, NSTaintSource+":"+s.ID)
		}
		for _, s := range p.taint.Sinks {
			out = append(out, NSTaintSink+":"+s.ID)
		}
		for _, s := range p.taint.Sanitizers {
			out = append(out, NSTaintSanitizer+":"+s.ID)
		}
	}
	sort.Strings(out)
	return out
}

// decodePayload parses and validates one artifact against its manifest's kind.
func decodePayload(kind Kind, data []byte) (payload, error) {
	var p payload
	switch kind {
	case KindArchitectureRules:
		if err := decodeStrict(data, &p.arch); err != nil {
			return payload{}, err
		}
		if err := validateArchPayload(p.arch); err != nil {
			return payload{}, err
		}
	case KindTaintRules:
		if err := decodeStrict(data, &p.taint); err != nil {
			return payload{}, err
		}
		if err := validateTaintPayload(p.taint); err != nil {
			return payload{}, err
		}
	default:
		return payload{}, fmt.Errorf("extpack: no artifact decoder for kind %q", Bound(string(kind)))
	}
	return p, nil
}

// decodeStrict decodes one YAML/JSON document with unknown fields rejected.
func decodeStrict(data []byte, into any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		if err == io.EOF {
			return fmt.Errorf("extpack: artifact is empty")
		}
		return fmt.Errorf("extpack: parse artifact: %w", err)
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("extpack: artifact holds more than one document")
		}
		return fmt.Errorf("extpack: parse artifact: %w", err)
	}
	return nil
}

// first returns the head of a check list, or nil.
//
// The artifact validators are written as COLLECTING checks for the same reason
// the manifest ones are (SW-230's linter), and the fail-fast entry points are
// their heads — so a validator and a linter can never disagree about whether an
// artifact is valid.
func first(errs []error) error {
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func validateArchPayload(p ArchPayload) error { return first(checkArchPayload(p)) }

func checkArchPayload(p ArchPayload) []error {
	if p.Version == "" {
		return []error{artifactErrf("version", "extpack: architecture-rules artifact declares no version")}
	}
	if len(p.Rules) == 0 {
		return []error{artifactErrf("rules", "extpack: architecture-rules artifact declares no rules")}
	}
	var out []error
	add := func(err error) {
		if err != nil {
			out = append(out, err)
		}
	}
	seen := map[string]struct{}{}
	for i, r := range p.Rules {
		at := func(leaf string) string { return fmt.Sprintf("rules[%d].%s", i, leaf) }
		if err := validateName(r.ID); err != nil {
			add(withField(ScopeArtifact, at("id"), err))
			continue
		}
		if _, dup := seen[r.ID]; dup {
			add(artifactErrf(at("id"), "extpack: architecture rule %q is declared twice", Bound(r.ID)))
			continue
		}
		seen[r.ID] = struct{}{}
		add(withField(ScopeArtifact, at("from"), validateText(fmt.Sprintf("architecture rule %q from", r.ID), r.From)))
		add(withField(ScopeArtifact, at("to"), validateText(fmt.Sprintf("architecture rule %q to", r.ID), r.To)))
		add(withField(ScopeArtifact, at("description"),
			validateText(fmt.Sprintf("architecture rule %q description", r.ID), r.Description)))
	}
	return out
}

func validateTaintPayload(p TaintPayload) error { return first(checkTaintPayload(p)) }

func checkTaintPayload(p TaintPayload) []error {
	if p.Version == "" {
		return []error{artifactErrf("version", "extpack: taint-rules artifact declares no version")}
	}
	if len(p.Sources)+len(p.Sinks)+len(p.Sanitizers) == 0 {
		return []error{artifactErrf("sources", "extpack: taint-rules artifact declares no sources, sinks or sanitizers")}
	}
	var out []error
	add := func(err error) {
		if err != nil {
			out = append(out, err)
		}
	}
	seen := map[string]struct{}{}
	claim := func(field, id string) error {
		if err := validateName(id); err != nil {
			return withField(ScopeArtifact, field, err)
		}
		if _, dup := seen[id]; dup {
			return artifactErrf(field, "extpack: taint definition id %q is declared twice", Bound(id))
		}
		seen[id] = struct{}{}
		return nil
	}
	patterns := func(field, what string, kinds, names []string) error {
		if len(kinds)+len(names) == 0 {
			return artifactErrf(field, "extpack: %s matches nothing (no node_kinds, no name_patterns)", what)
		}
		for _, v := range append(append([]string(nil), kinds...), names...) {
			if err := validateText(what+" pattern", v); err != nil {
				return withField(ScopeArtifact, field, err)
			}
		}
		return nil
	}
	for i, s := range p.Sources {
		at := func(leaf string) string { return fmt.Sprintf("sources[%d].%s", i, leaf) }
		if err := claim(at("id"), s.ID); err != nil {
			add(err)
			continue
		}
		add(withField(ScopeArtifact, at("label"), validateText(fmt.Sprintf("taint source %q label", s.ID), s.Label)))
		add(patterns(fmt.Sprintf("sources[%d]", i), fmt.Sprintf("taint source %q", s.ID), s.NodeKinds, s.NamePatterns))
	}
	for i, s := range p.Sinks {
		at := func(leaf string) string { return fmt.Sprintf("sinks[%d].%s", i, leaf) }
		if err := claim(at("id"), s.ID); err != nil {
			add(err)
			continue
		}
		add(withField(ScopeArtifact, at("category"), validateText(fmt.Sprintf("taint sink %q category", s.ID), s.Category)))
		add(patterns(fmt.Sprintf("sinks[%d]", i), fmt.Sprintf("taint sink %q", s.ID), s.NodeKinds, s.NamePatterns))
	}
	for i, s := range p.Sanitizers {
		at := func(leaf string) string { return fmt.Sprintf("sanitizers[%d].%s", i, leaf) }
		if err := claim(at("id"), s.ID); err != nil {
			add(err)
			continue
		}
		add(patterns(fmt.Sprintf("sanitizers[%d]", i), fmt.Sprintf("taint sanitizer %q", s.ID), nil, s.NamePatterns))
		// A sanitizer with an empty remove_labels is a UNIVERSAL sanitizer in
		// engine/analysis/taint — it strips every label. graphi ships several, but
		// a pack may not: a universal sanitizer with a broad name pattern is a
		// one-line way to make a repository's taint findings disappear, and
		// "suppress everything" is not an additive capability. A pack must name
		// the labels it claims to sanitise.
		if len(s.RemoveLabels) == 0 {
			add(artifactErrf(at("remove_labels"), "extpack: taint sanitizer %q declares no remove_labels: "+
				"a pack may not ship a universal sanitizer, it must name the labels it removes", Bound(s.ID)))
			continue
		}
		for _, l := range s.RemoveLabels {
			add(withField(ScopeArtifact, at("remove_labels"),
				validateText(fmt.Sprintf("taint sanitizer %q remove_labels entry", s.ID), l)))
		}
	}
	return out
}
