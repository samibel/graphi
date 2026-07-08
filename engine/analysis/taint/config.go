package taint

import (
	"fmt"
	"sort"
	"strings"
)

// SourceDef defines a taint source — a pattern that, when matched against a
// graph node's kind+qualified name, marks that node as an origin of tainted
// data. Each source carries a label (the taint kind, e.g. "user_input") that
// propagates along def-use edges.
type SourceDef struct {
	// ID is the unique identifier for this source definition (e.g. "http_request_param").
	ID string `json:"id"`
	// Label is the taint kind propagated from this source (e.g. "user_input").
	Label string `json:"label"`
	// NodeKinds matches against model.Node.Kind() (e.g. "parameter", "call").
	NodeKinds []string `json:"node_kinds,omitempty"`
	// NamePatterns matches against model.Node.QualifiedName() via substring.
	NamePatterns []string `json:"name_patterns,omitempty"`
}

// SinkDef defines a taint sink — a pattern that, when matched, marks a node as
// a dangerous operation where tainted data must not arrive unsanitized.
type SinkDef struct {
	// ID is the unique identifier for this sink definition (e.g. "sql_exec").
	ID string `json:"id"`
	// Category classifies the injection type (e.g. "sql_injection", "command_injection").
	Category string `json:"category"`
	// NodeKinds matches against model.Node.Kind().
	NodeKinds []string `json:"node_kinds,omitempty"`
	// NamePatterns matches against model.Node.QualifiedName() via substring.
	NamePatterns []string `json:"name_patterns,omitempty"`
}

// SanitizerDef defines a sanitizer — a function/operation that removes taint
// labels from values passing through it. A sanitizer matches by name pattern
// and removes the specified labels (or all labels if RemoveLabels is empty).
type SanitizerDef struct {
	// ID is the unique identifier (e.g. "sql_escape").
	ID string `json:"id"`
	// NamePatterns matches against model.Node.QualifiedName() via substring.
	NamePatterns []string `json:"name_patterns"`
	// RemoveLabels specifies which taint labels this sanitizer removes. Empty
	// means it removes ALL labels (a universal sanitizer).
	RemoveLabels []string `json:"remove_labels,omitempty"`
}

// Config is the taint analysis configuration. It defines the sources, sinks,
// and sanitizers that drive label propagation. The config is immutable after
// construction and validated at load time.
type Config struct {
	Version    string         `json:"version"`
	Sources    []SourceDef    `json:"sources"`
	Sinks      []SinkDef      `json:"sinks"`
	Sanitizers []SanitizerDef `json:"sanitizers"`
	// ContentHash is a deterministic hash of the config content, included in
	// every finding's provenance so consumers can verify which config produced
	// a given result.
	ContentHash string `json:"content_hash,omitempty"`
}

// Validate checks the config for structural correctness: no duplicate IDs,
// no empty required fields, at least one source and one sink.
func (c Config) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("taint config: version must not be empty")
	}
	if len(c.Sources) == 0 {
		return fmt.Errorf("taint config: at least one source is required")
	}
	if len(c.Sinks) == 0 {
		return fmt.Errorf("taint config: at least one sink is required")
	}
	ids := make(map[string]struct{})
	for _, s := range c.Sources {
		if s.ID == "" {
			return fmt.Errorf("taint config: source ID must not be empty")
		}
		if s.Label == "" {
			return fmt.Errorf("taint config: source %q must have a label", s.ID)
		}
		if _, dup := ids[s.ID]; dup {
			return fmt.Errorf("taint config: duplicate ID %q", s.ID)
		}
		ids[s.ID] = struct{}{}
	}
	for _, s := range c.Sinks {
		if s.ID == "" {
			return fmt.Errorf("taint config: sink ID must not be empty")
		}
		if _, dup := ids[s.ID]; dup {
			return fmt.Errorf("taint config: duplicate ID %q", s.ID)
		}
		ids[s.ID] = struct{}{}
	}
	for _, s := range c.Sanitizers {
		if s.ID == "" {
			return fmt.Errorf("taint config: sanitizer ID must not be empty")
		}
		if _, dup := ids[s.ID]; dup {
			return fmt.Errorf("taint config: duplicate ID %q", s.ID)
		}
		ids[s.ID] = struct{}{}
	}
	return nil
}

// matchSource reports whether a node matches any source definition, returning
// the matching source's label and ID. Empty string means no match.
func (c Config) matchSource(kind, qualifiedName string) (label, sourceID string) {
	qLower := strings.ToLower(qualifiedName)
	kLower := strings.ToLower(kind)
	for _, src := range c.Sources {
		if matchDef(kLower, qLower, src.NodeKinds, src.NamePatterns) {
			return src.Label, src.ID
		}
	}
	return "", ""
}

// matchSink reports whether a node matches any sink definition, returning the
// matching sink's ID and category.
func (c Config) matchSink(kind, qualifiedName string) (sinkID, category string) {
	qLower := strings.ToLower(qualifiedName)
	kLower := strings.ToLower(kind)
	for _, s := range c.Sinks {
		if matchDef(kLower, qLower, s.NodeKinds, s.NamePatterns) {
			return s.ID, s.Category
		}
	}
	return "", ""
}

// matchSanitizer reports whether a node matches any sanitizer definition,
// returning the sanitizer and whether it matched.
func (c Config) matchSanitizer(kind, qualifiedName string) (SanitizerDef, bool) {
	qLower := strings.ToLower(qualifiedName)
	for _, san := range c.Sanitizers {
		for _, pat := range san.NamePatterns {
			if strings.Contains(qLower, strings.ToLower(pat)) {
				return san, true
			}
		}
	}
	_ = kind // sanitizers match by name only
	return SanitizerDef{}, false
}

// MatchSource is the exported wrapper over matchSource so sibling engine
// packages (e.g. the interprocedural taint solver) can classify a node against
// this config without re-implementing the matching rules. Returns the matching
// source's label and ID; empty strings mean no match.
func (c Config) MatchSource(kind, qualifiedName string) (label, sourceID string) {
	return c.matchSource(kind, qualifiedName)
}

// MatchSink is the exported wrapper over matchSink. Returns the matching sink's
// ID and category; empty strings mean no match.
func (c Config) MatchSink(kind, qualifiedName string) (sinkID, category string) {
	return c.matchSink(kind, qualifiedName)
}

// MatchSanitizer is the exported wrapper over matchSanitizer. Returns the
// matching sanitizer definition and whether it matched.
func (c Config) MatchSanitizer(kind, qualifiedName string) (SanitizerDef, bool) {
	return c.matchSanitizer(kind, qualifiedName)
}

// matchDef is the shared matching logic: a node matches if its kind is in
// nodeKinds OR its qualified name contains any of the name patterns. Both lists
// empty means no match.
func matchDef(kindLower, qualNameLower string, nodeKinds, namePatterns []string) bool {
	for _, k := range nodeKinds {
		if strings.ToLower(k) == kindLower {
			return true
		}
	}
	for _, pat := range namePatterns {
		if strings.Contains(qualNameLower, strings.ToLower(pat)) {
			return true
		}
	}
	return false
}

// DefaultConfig returns the canonical default taint configuration for Go
// projects, covering Go stdlib sources, sinks, and sanitizers. This is the
// Go-embedded immutable default shipped with graphi.
func DefaultConfig() Config {
	return Config{
		Version: "1.0.0",
		Sources: []SourceDef{
			{ID: "http_request", Label: "user_input", NamePatterns: []string{"http.Request", "net/http.Request"}},
			{ID: "request_form", Label: "user_input", NamePatterns: []string{".FormValue", ".PostFormValue", ".URL.Query"}},
			{ID: "env_var", Label: "env_input", NamePatterns: []string{"os.Getenv", "os.LookupEnv"}},
			{ID: "stdin", Label: "user_input", NamePatterns: []string{"os.Stdin", "bufio.NewReader", "bufio.NewScanner"}},
			{ID: "json_decode", Label: "deserialized", NamePatterns: []string{"json.Unmarshal", "json.NewDecoder", "json.Decoder.Decode"}},
			{ID: "xml_decode", Label: "deserialized", NamePatterns: []string{"xml.Unmarshal", "xml.NewDecoder", "xml.Decoder.Decode"}},
			{ID: "yaml_decode", Label: "deserialized", NamePatterns: []string{"yaml.Unmarshal", "yaml.NewDecoder"}},
			{ID: "gob_decode", Label: "deserialized", NamePatterns: []string{"gob.NewDecoder", "gob.Decoder.Decode"}},
		},
		Sinks: []SinkDef{
			{ID: "sql_exec", Category: "sql_injection", NamePatterns: []string{"database/sql.DB.Query", "database/sql.DB.Exec", "database/sql.DB.QueryRow", ".Query(", ".Exec(", ".QueryRow("}},
			{ID: "os_exec", Category: "command_injection", NamePatterns: []string{"os/exec.Command", "os/exec.CommandContext", "syscall.Exec"}},
			{ID: "file_open", Category: "path_traversal", NamePatterns: []string{"os.Open", "os.Create", "os.OpenFile", "os.ReadFile", "os.WriteFile"}},
			{ID: "template_exec", Category: "template_injection", NamePatterns: []string{"template.Template.Execute", "template.Template.ExecuteTemplate", "html/template.Template.Execute"}},
			{ID: "http_redirect", Category: "open_redirect", NamePatterns: []string{"http.Redirect"}},
			{ID: "net_dial", Category: "ssrf", NamePatterns: []string{"net.Dial", "net.DialTimeout", "http.Get", "http.Post", "http.NewRequest"}},
			{ID: "ldap_search", Category: "ldap_injection", NamePatterns: []string{"ldap.Search", "ldap.SearchRequest"}},
			// log_write intentionally OMITS fmt.Fprintf: it is a general-purpose
			// formatter (used for all formatted output — stderr diagnostics, file
			// writers, buffers — not specifically logging), and was the single
			// largest false-positive source in adversarial verification (WP-05b-3
			// precision fix B). Real logging via log.Printf/Print/Println stays.
			{ID: "log_write", Category: "log_injection", NamePatterns: []string{"log.Printf", "log.Print", "log.Println"}},
			{ID: "regex_compile", Category: "redos", NamePatterns: []string{"regexp.Compile", "regexp.MustCompile"}},
		},
		Sanitizers: []SanitizerDef{
			{ID: "html_escape", NamePatterns: []string{"html.EscapeString", "template.HTMLEscapeString"}, RemoveLabels: []string{"user_input"}},
			{ID: "url_escape", NamePatterns: []string{"url.QueryEscape", "url.PathEscape"}},
			{ID: "filepath_clean", NamePatterns: []string{"filepath.Clean", "path.Clean"}},
			{ID: "sql_param", NamePatterns: []string{"pq.QuoteIdentifier", "pq.QuoteLiteral"}},
			{ID: "strconv", NamePatterns: []string{"strconv.Atoi", "strconv.ParseInt", "strconv.ParseFloat", "strconv.ParseBool"}},
		},
	}
}

// LabelSet is a sorted, deduplicated set of taint labels. The sorted invariant
// ensures deterministic comparison and serialization.
type LabelSet []string

// NewLabelSet creates a LabelSet from the given labels, deduplicating and
// sorting for determinism.
func NewLabelSet(labels ...string) LabelSet {
	if len(labels) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if _, dup := seen[l]; !dup {
			seen[l] = struct{}{}
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return LabelSet(out)
}

// Contains reports whether the set contains label.
func (ls LabelSet) Contains(label string) bool {
	i := sort.SearchStrings(ls, label)
	return i < len(ls) && ls[i] == label
}

// Union returns a new LabelSet containing all labels from both sets.
func (ls LabelSet) Union(other LabelSet) LabelSet {
	combined := make([]string, 0, len(ls)+len(other))
	combined = append(combined, ls...)
	combined = append(combined, other...)
	return NewLabelSet(combined...)
}

// Remove returns a new LabelSet with the specified labels removed. If
// toRemove is empty, returns an empty LabelSet (removes all).
func (ls LabelSet) Remove(toRemove []string) LabelSet {
	if len(toRemove) == 0 {
		return nil // universal sanitizer: remove all
	}
	rm := make(map[string]struct{}, len(toRemove))
	for _, r := range toRemove {
		rm[r] = struct{}{}
	}
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		if _, found := rm[l]; !found {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return LabelSet(out)
}

// Empty reports whether the label set is empty (no taint).
func (ls LabelSet) Empty() bool { return len(ls) == 0 }

// Equal reports whether two label sets are identical.
func (ls LabelSet) Equal(other LabelSet) bool {
	if len(ls) != len(other) {
		return false
	}
	for i := range ls {
		if ls[i] != other[i] {
			return false
		}
	}
	return true
}

// String returns a deterministic string representation of the label set.
func (ls LabelSet) String() string {
	return "{" + strings.Join(ls, ",") + "}"
}
