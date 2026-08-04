package mcp

// Adversarial pins for the P1 graph_health Labs tool (PRD §17, contract §2/§4).
//
// The CLI/MCP byte parity is structural — ONE shared composition
// (surfaces/client/trust_report.go) feeds both surfaces — but structure can be
// refactored away silently, so this file pins it EXECUTABLY: a fixture
// repository gets a REAL full ingest, then the MCP tool text is compared
// byte-for-byte against client.TrustReport (exactly what `graphi trust-report
// --json` writes minus the trailing newline; the CLI half of the pin lives in
// cmd/graphi/trust_report_test.go). Also pinned here: the surface passes the
// four PRD §17 wire arguments through verbatim and never invents store
// locations, wire-level determinism, the §4 error model (-32602 input /
// -32603 operational, never an empty success), Labs gating (absent from the
// Stable catalog, dispatch-rejected before the client), and the PRD §30 token
// budget for the default output.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/surfaces/client"
)

// buildGraphHealthFixture writes a tiny Go repository — plus one malformed
// JSON file the parser skips fail-closed, so the details lists carry real
// content — and runs one REAL full ingest into a durable SQLite store +
// sidecar, the exact state the shared trust-report composition observes
// read-only (mirrors the client-side fixture in surfaces/client).
func buildGraphHealthFixture(t *testing.T) (root, dbPath, metaDir string) {
	t.Helper()
	ctx := context.Background()

	root = t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/fix\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		"main.go":      "package main\n\nimport \"example.com/fix/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
		// Handlebars-templated JSON: strict encoding/json rejects it, producing
		// one SkipParseError diagnostic (the parse-skipped evidence sample).
		"bad.json": "{\n  {{#each xs}}\n  \"id\": \"x\"\n  {{/each}}\n}\n",
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	metaDir = t.TempDir()
	dbPath = filepath.Join(t.TempDir(), "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close ingester: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return root, dbPath, metaDir
}

// graphHealthFixtureClient stands in for the composition root: it records the
// TrustReportOptions the MCP surface constructed (the surface must pass the
// wire arguments through verbatim and must NOT invent store locations), then
// injects the fixture's repository location — exactly what the real runtime
// supplies via the server process's working directory — and delegates to the
// ONE shared composition. A non-nil err simulates an operational failure.
type graphHealthFixtureClient struct {
	client.Client
	root, dbPath, metaDir string
	err                   error
	got                   []client.TrustReportOptions
}

func (c *graphHealthFixtureClient) TrustReport(ctx context.Context, opts client.TrustReportOptions) ([]byte, trust.Verdict, trust.State, error) {
	c.got = append(c.got, opts)
	if c.err != nil {
		return nil, "", "", c.err
	}
	opts.Root, opts.DBPath, opts.MetaDir = c.root, c.dbPath, c.metaDir
	return c.Client.TrustReport(ctx, opts)
}

// toolText unwraps a successful tools/call response into its single text
// content block, failing the test on tool errors, isError results, or any
// shape other than exactly one text block.
func toolText(t *testing.T, resp toolInvocationResponse) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("tool call failed: %+v", resp.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, resp.Result)
	}
	if result.IsError {
		t.Fatalf("tool result marked isError: %s", resp.Result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tool result is not exactly one text block: %s", resp.Result)
	}
	return result.Content[0].Text
}

// TestGraphHealth_ParityWithSharedComposition is the executable CLI↔MCP parity
// pin: for every target/policy/details/limit combination, the graph_health
// text over the wire equals client.TrustReport's canonical bytes — which are
// byte-for-byte what `graphi trust-report --json` writes minus the trailing
// newline (pinned on the CLI side by
// TestRunTrustReport_JSONMatchesSharedCompositionBytes). It also pins that the
// surface forwarded exactly the wire arguments and left Root/DBPath/MetaDir
// empty for the composition root to fill.
func TestGraphHealth_ParityWithSharedComposition(t *testing.T) {
	root, dbPath, metaDir := buildGraphHealthFixture(t)
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil), root: root, dbPath: dbPath, metaDir: metaDir}
	server := NewServerWithClient(fx, WithLabs())

	cases := []struct {
		name string
		args map[string]any
		opts client.TrustReportOptions
	}{
		{"default", map[string]any{}, client.TrustReportOptions{}},
		{"file target", map[string]any{"target": "util/util.go"},
			client.TrustReportOptions{Target: "util/util.go"}},
		{"file target with evidence and policy", map[string]any{"target": "util/util.go", "policy": trust.PolicyNameAutomatedChange},
			client.TrustReportOptions{Target: "util/util.go", Policy: trust.PolicyNameAutomatedChange}},
		{"unresolvable target", map[string]any{"target": "no_such_symbol_xyz"},
			client.TrustReportOptions{Target: "no_such_symbol_xyz"}},
		{"policy exploratory", map[string]any{"policy": trust.PolicyNameExploratory},
			client.TrustReportOptions{Policy: trust.PolicyNameExploratory}},
		{"policy review", map[string]any{"policy": trust.PolicyNameReview},
			client.TrustReportOptions{Policy: trust.PolicyNameReview}},
		{"policy automated_change", map[string]any{"policy": trust.PolicyNameAutomatedChange},
			client.TrustReportOptions{Policy: trust.PolicyNameAutomatedChange}},
		{"details", map[string]any{"details": true},
			client.TrustReportOptions{Details: true}},
		{"details limited", map[string]any{"details": true, "limit": 1},
			client.TrustReportOptions{Details: true, Limit: 1}},
		{"kitchen sink", map[string]any{"target": "no_such_symbol_xyz", "policy": trust.PolicyNameAutomatedChange, "details": true, "limit": 2},
			client.TrustReportOptions{Target: "no_such_symbol_xyz", Policy: trust.PolicyNameAutomatedChange, Details: true, Limit: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(fx.got)
			text := toolText(t, invokeTool(t, server, ToolGraphHealth, tc.args))
			if len(fx.got) != before+1 {
				t.Fatalf("TrustReport calls = %d, want %d", len(fx.got), before+1)
			}
			passed := fx.got[before]
			if passed.Root != "" || passed.DBPath != "" || passed.MetaDir != "" {
				t.Errorf("the surface invented store locations: %+v", passed)
			}
			if passed.Target != tc.opts.Target || passed.Policy != tc.opts.Policy ||
				passed.Details != tc.opts.Details || passed.Limit != tc.opts.Limit {
				t.Errorf("the surface mangled the wire arguments:\n got: %+v\nwant: %+v", passed, tc.opts)
			}

			opts := tc.opts
			opts.Root, opts.DBPath, opts.MetaDir = root, dbPath, metaDir
			want, _, _, err := client.TrustReport(context.Background(), opts)
			if err != nil {
				t.Fatalf("client.TrustReport: %v", err)
			}
			if text != string(want) {
				t.Errorf("MCP text != shared composition bytes (CLI --json minus trailing newline):\nmcp: %s\ncli: %s", text, want)
			}
		})
	}
}

// TestGraphHealth_ScopeEvidenceParity pins the additive scope_evidence object
// across the surface seam for a target whose persisted file evidence exists
// (util/util.go — a clean parsed row, pinned upstream in engine/ingest): the
// MCP tool text carries the SAME scope_evidence bytes as the shared
// composition (which the CLI --json emits verbatim), the object reports the
// fetched row (available=true, parse_status parsed), and the scoped policy
// verdict rides it to PASS on both surfaces — the byte-parity loop above
// guarantees the whole documents match; this test makes the scope_evidence
// half of that unmissable and self-describing.
func TestGraphHealth_ScopeEvidenceParity(t *testing.T) {
	root, dbPath, metaDir := buildGraphHealthFixture(t)
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil), root: root, dbPath: dbPath, metaDir: metaDir}
	server := NewServerWithClient(fx, WithLabs())

	args := map[string]any{"target": "util/util.go", "policy": trust.PolicyNameAutomatedChange}
	text := toolText(t, invokeTool(t, server, ToolGraphHealth, args))

	want, _, _, err := client.TrustReport(context.Background(), client.TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "util/util.go", Policy: trust.PolicyNameAutomatedChange,
	})
	if err != nil {
		t.Fatalf("client.TrustReport: %v", err)
	}
	if text != string(want) {
		t.Fatalf("MCP text != shared composition bytes for the evidence-backed target:\nmcp: %s\ncli: %s", text, want)
	}

	type scopedDoc struct {
		ScopeEvidence trust.ScopeFacts `json:"scope_evidence"`
		Policy        struct {
			Verdict trust.Verdict `json:"verdict"`
		} `json:"policy"`
	}
	var mcpDoc, cliDoc scopedDoc
	if err := json.Unmarshal([]byte(text), &mcpDoc); err != nil {
		t.Fatalf("decode MCP document: %v", err)
	}
	if err := json.Unmarshal(want, &cliDoc); err != nil {
		t.Fatalf("decode composition document: %v", err)
	}
	if mcpDoc.ScopeEvidence != cliDoc.ScopeEvidence {
		t.Errorf("scope_evidence differs across surfaces:\nmcp: %+v\ncli: %+v", mcpDoc.ScopeEvidence, cliDoc.ScopeEvidence)
	}
	if !mcpDoc.ScopeEvidence.Available || mcpDoc.ScopeEvidence.File.ParseStatus != trust.ScopeParseStatusParsed {
		t.Errorf("scope_evidence = %+v, want the fetched parsed row (available=true)", mcpDoc.ScopeEvidence)
	}
	if mcpDoc.Policy.Verdict != trust.VerdictPass {
		t.Errorf("automated_change over the clean evidence-backed file = %s, want PASS", mcpDoc.Policy.Verdict)
	}
}

// TestGraphHealth_DeterministicOverTheWire pins MCP-side byte determinism for
// the details+limit shape (the CLI-side twin lives in cmd/graphi): two full
// JSON-RPC round trips over identical facts yield identical text.
func TestGraphHealth_DeterministicOverTheWire(t *testing.T) {
	root, dbPath, metaDir := buildGraphHealthFixture(t)
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil), root: root, dbPath: dbPath, metaDir: metaDir}
	server := NewServerWithClient(fx, WithLabs())

	args := map[string]any{"policy": trust.PolicyNameExploratory, "details": true, "limit": 3}
	first := toolText(t, invokeTool(t, server, ToolGraphHealth, args))
	second := toolText(t, invokeTool(t, server, ToolGraphHealth, args))
	if first != second {
		t.Fatalf("two graph_health calls over identical facts differ:\n%s\n---\n%s", first, second)
	}
}

// TestGraphHealth_DefaultOutputWithinTokenBudget pins the PRD §30 budget for
// the default (no details) output: ≤ 2,000 estimated tokens (the PRD target;
// the hard gate is 8,000). No token estimator exists in surfaces/mcp, so the
// documented chars/4 estimate is used.
func TestGraphHealth_DefaultOutputWithinTokenBudget(t *testing.T) {
	root, dbPath, metaDir := buildGraphHealthFixture(t)
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil), root: root, dbPath: dbPath, metaDir: metaDir}
	server := NewServerWithClient(fx, WithLabs())

	text := toolText(t, invokeTool(t, server, ToolGraphHealth, map[string]any{}))
	if tokens := len(text) / 4; tokens > 2000 {
		t.Fatalf("default graph_health output ≈ %d tokens (chars/4), above the 2,000-token target (hard cap 8,000):\n%s", tokens, text)
	}
}

// TestGraphHealth_ErrorModel pins the contract §4 surface mapping: an unknown
// policy and a negative limit are input errors (-32602, the limit rejected
// BEFORE the composition runs — input parity with the CLI's exit 2); an
// operational failure is -32603; and no error path ever returns an empty
// success in place of the typed error.
func TestGraphHealth_ErrorModel(t *testing.T) {
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil)}
	server := NewServerWithClient(fx, WithLabs())

	resp := invokeTool(t, server, ToolGraphHealth, map[string]any{"policy": "certainly-not-a-policy"})
	if resp.Error == nil {
		t.Fatalf("unknown policy returned a success: %s", resp.Result)
	}
	if resp.Error.Code != -32602 {
		t.Errorf("unknown policy code = %d, want -32602 (input error)", resp.Error.Code)
	}
	if resp.Error.Message == "" || len(resp.Result) != 0 {
		t.Errorf("unknown policy must be a typed error, never an empty success: %+v", resp)
	}

	before := len(fx.got)
	resp = invokeTool(t, server, ToolGraphHealth, map[string]any{"details": true, "limit": -1})
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("negative limit response = %+v, want -32602 (a negative limit must not silently uncap the details lists)", resp)
	}
	if len(fx.got) != before {
		t.Errorf("negative limit reached the composition: %+v", fx.got[before:])
	}

	broken := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil), err: client.ErrTrustUnavailable}
	brokenServer := NewServerWithClient(broken, WithLabs())
	resp = invokeTool(t, brokenServer, ToolGraphHealth, map[string]any{})
	if resp.Error == nil {
		t.Fatalf("operational failure returned a success: %s", resp.Result)
	}
	if resp.Error.Code != -32603 {
		t.Errorf("operational failure code = %d, want -32603", resp.Error.Code)
	}
	if resp.Error.Message == "" || len(resp.Result) != 0 {
		t.Errorf("operational failure must be a typed error, never an empty success: %+v", resp)
	}
}

// TestGraphHealth_LabsGated pins the SCOPE-01 boundary for the trust tool
// specifically: graph_health is absent from the Stable catalog and a direct
// invocation against a Stable-profile server is rejected at the catalog
// boundary — with the Labs hint — before the client is ever reached. (The
// whole-catalog equality pins live in profile_test.go; this is the
// tool-specific dispatch half.)
func TestGraphHealth_LabsGated(t *testing.T) {
	fx := &graphHealthFixtureClient{Client: client.NewDirect(nil, nil)}
	server := NewServerWithClient(fx) // default Stable profile

	if containsTool(descriptorNames(server.toolDescriptors()), ToolGraphHealth) {
		t.Fatal("graph_health advertised in the Stable catalog")
	}
	resp := invokeTool(t, server, ToolGraphHealth, map[string]any{})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "Stable") {
		t.Fatalf("Stable-profile graph_health call must fail closed with the Labs hint: %+v", resp)
	}
	if len(fx.got) != 0 {
		t.Fatalf("rejected graph_health call reached the client: %+v", fx.got)
	}
}
