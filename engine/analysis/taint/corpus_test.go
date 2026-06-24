package taint

// Labeled taint ground-truth corpus (PB-001 §10: "taint 100% recall on labeled
// set"). Each case is a committed, hand-labeled scenario: a small graph plus the
// EXHAUSTIVE set of source→sink flows a correct analyzer MUST report. Positive
// cases model a real injection class (CWE noted); negative cases carry an empty
// want set and assert the analyzer reports nothing (so a trivially over-tainting
// analyzer cannot "earn" 100% recall — it would fail the negatives instead).
//
// Ground truth is keyed on config-derived/qualified-name facts (sink category +
// sink qualified name), never on node ids: node ids are content-derived and must
// not leak into the labels, so the corpus survives id-scheme changes.
//
// The corpus is test-only (this is a _test.go file): it is committed and
// CI-enforced via recall_test.go, but ships in no binary (size budget #7) and
// adds no import to the analyzer package.

import (
	"testing"

	"github.com/samibel/graphi/core/model"
)

// expectedFlow is one labeled source→sink flow, identified by stable, node-id-
// independent facts.
type expectedFlow struct {
	sinkCategory string // e.g. "sql_injection"
	sinkName     string // sink node qualified name, e.g. "database/sql.DB.Query"
}

// labeledCase is one committed taint scenario with its exhaustive ground truth.
type labeledCase struct {
	name     string
	cwe      string // informational: the weakness class this models
	negative bool   // true ⇒ want must be empty and the analyzer must find nothing
	build    func(t *testing.T) ([]model.Node, []model.Edge)
	want     []expectedFlow
}

// nodeSpec is a (kind, qualifiedName) pair used to build a linear taint chain.
type nodeSpec struct{ kind, name string }

// buildLinear builds a source→…→sink chain: it creates one node per spec and a
// def-use edge between each consecutive pair. Edge kind is irrelevant to the
// analyzer's adjacency (it follows all edges), so "references" is used uniformly.
func buildLinear(t *testing.T, specs ...nodeSpec) ([]model.Node, []model.Edge) {
	t.Helper()
	nodes := make([]model.Node, len(specs))
	for i, s := range specs {
		nodes[i] = mustNode(t, s.kind, s.name, "corpus.go", i+1, 1)
	}
	edges := make([]model.Edge, 0, len(nodes))
	for i := 0; i+1 < len(nodes); i++ {
		edges = append(edges, mustEdge(t, nodes[i].ID(), nodes[i+1].ID(), "references"))
	}
	return nodes, edges
}

// minCorpusCases is the floor enforced by TestTaintCorpus_NonEmpty: the corpus
// must keep covering every default sink category (9) plus negatives. A drop
// below this floor is a silent-coverage regression and fails CI.
const minCorpusCases = 13

// labeledCorpus returns the committed ground-truth set. Every default sink
// category is covered by at least one positive case; deserialization/stdin
// source variety and four sanitizer/no-path negatives guard against false
// positives.
func labeledCorpus() []labeledCase {
	return []labeledCase{
		// ── Positive cases: one per default sink category ──────────────────────
		{
			name: "sql_injection/form_to_query",
			cwe:  "CWE-89",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "userInput"},
					nodeSpec{"call", "database/sql.DB.Query"})
			},
			want: []expectedFlow{{"sql_injection", "database/sql.DB.Query"}},
		},
		{
			name: "command_injection/env_to_exec",
			cwe:  "CWE-78",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "os.Getenv"},
					nodeSpec{"variable", "cmdName"},
					nodeSpec{"call", "os/exec.Command"})
			},
			want: []expectedFlow{{"command_injection", "os/exec.Command"}},
		},
		{
			name: "path_traversal/query_to_open",
			cwe:  "CWE-22",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.URL.Query"},
					nodeSpec{"variable", "path"},
					nodeSpec{"call", "os.Open"})
			},
			want: []expectedFlow{{"path_traversal", "os.Open"}},
		},
		{
			name: "template_injection/request_to_execute",
			cwe:  "CWE-1336",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "net/http.Request"},
					nodeSpec{"variable", "data"},
					nodeSpec{"call", "template.Template.Execute"})
			},
			want: []expectedFlow{{"template_injection", "template.Template.Execute"}},
		},
		{
			name: "open_redirect/form_to_redirect",
			cwe:  "CWE-601",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "target"},
					nodeSpec{"call", "http.Redirect"})
			},
			want: []expectedFlow{{"open_redirect", "http.Redirect"}},
		},
		{
			name: "ssrf/form_to_dial",
			cwe:  "CWE-918",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "addr"},
					nodeSpec{"call", "net.Dial"})
			},
			want: []expectedFlow{{"ssrf", "net.Dial"}},
		},
		{
			name: "ldap_injection/form_to_search",
			cwe:  "CWE-90",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "filter"},
					nodeSpec{"call", "ldap.Search"})
			},
			want: []expectedFlow{{"ldap_injection", "ldap.Search"}},
		},
		{
			name: "log_injection/form_to_log",
			cwe:  "CWE-117",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "msg"},
					nodeSpec{"call", "log.Printf"})
			},
			want: []expectedFlow{{"log_injection", "log.Printf"}},
		},
		{
			name: "redos/form_to_compile",
			cwe:  "CWE-1333",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"variable", "pattern"},
					nodeSpec{"call", "regexp.Compile"})
			},
			want: []expectedFlow{{"redos", "regexp.Compile"}},
		},
		// ── Source-variety positives (deserialization + stdin) ─────────────────
		{
			name: "sql_injection/json_decode_source",
			cwe:  "CWE-89",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "json.Unmarshal"},
					nodeSpec{"variable", "payload"},
					nodeSpec{"call", "database/sql.DB.Exec"})
			},
			want: []expectedFlow{{"sql_injection", "database/sql.DB.Exec"}},
		},
		{
			name: "command_injection/stdin_source",
			cwe:  "CWE-78",
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				return buildLinear(t,
					nodeSpec{"call", "bufio.NewScanner"},
					nodeSpec{"variable", "line"},
					nodeSpec{"call", "syscall.Exec"})
			},
			want: []expectedFlow{{"command_injection", "syscall.Exec"}},
		},
		// ── Negative cases: a correct analyzer reports NOTHING ──────────────────
		{
			name:     "negative/html_escape_sanitizes",
			cwe:      "CWE-89 (sanitized)",
			negative: true,
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				// user_input → html.EscapeString (removes user_input) → sql sink.
				return buildLinear(t,
					nodeSpec{"call", "req.FormValue"},
					nodeSpec{"call", "html.EscapeString"},
					nodeSpec{"call", "database/sql.DB.Query"})
			},
		},
		{
			name:     "negative/url_escape_universal_sanitizer",
			cwe:      "CWE-78 (sanitized)",
			negative: true,
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				// env_input → url.QueryEscape (RemoveLabels empty ⇒ removes all) → exec.
				return buildLinear(t,
					nodeSpec{"call", "os.Getenv"},
					nodeSpec{"call", "url.QueryEscape"},
					nodeSpec{"call", "os/exec.Command"})
			},
		},
		{
			name:     "negative/no_path_between_source_and_sink",
			cwe:      "n/a",
			negative: true,
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				src := mustNode(t, "call", "req.FormValue", "corpus.go", 1, 1)
				sink := mustNode(t, "call", "database/sql.DB.Query", "corpus.go", 2, 1)
				// No edge connects them: tainted data never reaches the sink.
				return []model.Node{src, sink}, nil
			},
		},
		{
			name:     "negative/reassignment_breaks_chain",
			cwe:      "n/a",
			negative: true,
			build: func(t *testing.T) ([]model.Node, []model.Edge) {
				src := mustNode(t, "call", "os.Getenv", "corpus.go", 1, 1)
				tainted := mustNode(t, "variable", "x", "corpus.go", 2, 1)
				safe := mustNode(t, "literal", "constLiteral", "corpus.go", 3, 1)
				sink := mustNode(t, "call", "os/exec.Command", "corpus.go", 4, 1)
				// The sink is fed by `safe` (untainted), not by `tainted`.
				e1 := mustEdge(t, src.ID(), tainted.ID(), "references")
				e2 := mustEdge(t, safe.ID(), sink.ID(), "references")
				return []model.Node{src, tainted, safe, sink}, []model.Edge{e1, e2}
			},
		},
	}
}
