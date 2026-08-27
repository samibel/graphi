// SW-227 (AX-07) characterization baseline for EVERY runtime start path.
//
// This file is written BEFORE the RuntimeBuilder refactor and is the artifact
// AC-4 is answered from. It does not assert "the right answer"; it records what
// each start path produces today — resolved paths, the capability set the bound
// client advertises, the canonical bytes of a representative operation slice,
// the tool list a real MCP session lists, and the lifecycle-verb statistics —
// and it is the fixture the post-refactor comparison replays.
//
// The start paths characterized here are the ones the story names, and they are
// the complete set reachable from this package:
//
//	zero-config       OpenSession with a Cwd fallback (bare `graphi`, `graphi mcp`
//	                  with no roots and no pin)
//	explicit root     OpenSession with Options.Root (`graphi mcp -root`, GRAPHI_ROOT)
//	transport roots   OpenSession with Options.Roots (the MCP roots/list bind)
//	db override       OpenSession with Options.DBOverride (short-circuits to Attach)
//	attach -db        Attach with an explicit store path
//	attach memory     Attach with an empty store path (the historical CLI fallback)
//	attach -daemon    Attach with a socket (a remote client, no local state)
//	mcp bind          a real mcp.Server bound through OpenSession, driven over
//	                  the JSON-RPC protocol
//	lifecycle verbs   SyncRepo / RebuildRepo over an OpenSession runtime
//
// Two properties are pinned:
//
//   - non-vacuity — every record actually reaches the engine (a start path that
//     silently stopped answering would otherwise compare equal to itself), and
//   - in-process determinism — the same start path taken twice in one process
//     produces the byte-identical record, which is what makes the cross-
//     composition comparison in ax07_composition_test.go meaningful rather than
//     a comparison of two noisy samples.
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// charFixtureSource is the pinned fixture repository. It is deliberately small
// and deliberately has cross-file structure (a caller, a callee, an unused
// symbol) so the probed operations return something rather than an empty
// document.
const charFixtureSource = `package fixture

// Greeter builds greetings.
type Greeter struct{ Name string }

// Greet returns the greeting.
func (g Greeter) Greet() string { return "hello " + g.Name }

// Run is the entry point.
func Run() string {
	g := Greeter{Name: "world"}
	return g.Greet()
}

// Orphan is never called.
func Orphan() int { return 42 }
`

// startPathRecord is the characterization of ONE start path. Every field is
// derived, never asserted against a hand-written expectation: the value of this
// record is that two runs of it can be compared, not that a human predicted it.
type startPathRecord struct {
	Label string `json:"label"`
	// Err is the construction error, if the start path failed. A failing start
	// path is characterized too — "fails the same way" is half of parity.
	Err string `json:"err,omitempty"`
	// Root/DBPath/MetaDir are normalized: absolute temp paths are replaced by
	// stable placeholders so the record compares across fixtures.
	Root    string `json:"root"`
	DBPath  string `json:"db_path"`
	MetaDir string `json:"meta_dir"`

	HasStore  bool `json:"has_store"`
	HasBroker bool `json:"has_broker"`

	// Capabilities is the sorted set of operation names the bound client says it
	// can execute (client.CapabilityReporter). Empty for a client that reports
	// no capabilities (the daemon transport).
	Capabilities []string `json:"capabilities"`

	// Ops maps a probe name to a digest of its normalized canonical bytes, or to
	// its error text. Both halves matter: a path that fails where another
	// succeeded must not compare equal.
	Ops map[string]string `json:"ops"`

	// MCPTools is the tool list a real MCP session advertises over this client,
	// and MCPCalls the digests of the tool calls that session made.
	MCPTools []string          `json:"mcp_tools,omitempty"`
	MCPCalls map[string]string `json:"mcp_calls,omitempty"`

	// Lifecycle records what SyncRepo/RebuildRepo did, when the start path
	// exercised them.
	Lifecycle map[string]string `json:"lifecycle,omitempty"`
}

// charRepository writes the pinned fixture repository under a fresh temp dir.
func charRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(charFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// pathNormalizer replaces machine-specific absolute paths with placeholders so
// two records taken over different temp directories still compare.
type pathNormalizer struct{ replacements []string }

func newPathNormalizer(pairs ...string) *pathNormalizer {
	return &pathNormalizer{replacements: pairs}
}

func (n *pathNormalizer) apply(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i+1 < len(n.replacements); i += 2 {
		from := n.replacements[i]
		if from == "" {
			continue
		}
		s = strings.ReplaceAll(s, from, n.replacements[i+1])
		if resolved, err := filepath.EvalSymlinks(from); err == nil && resolved != from {
			s = strings.ReplaceAll(s, resolved, n.replacements[i+1])
		}
	}
	return s
}

func (n *pathNormalizer) digest(b []byte) string {
	sum := sha256.Sum256([]byte(n.apply(string(b))))
	return hex.EncodeToString(sum[:])[:16]
}

// charProbes is the operation slice every record runs. It spans the stable
// structural queries, lexical search, all four agent-context operations, the
// analyzer dispatch seam and the AX-06 canary — i.e. every service the
// composition wires.
var charProbes = []struct {
	name string
	run  func(context.Context, client.Client) ([]byte, error)
}{
	{"query.callers", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Query(ctx, "callers", "fixture.Greeter.Greet", 1)
	}},
	{"query.callees", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Query(ctx, "callees", "fixture.Run", 1)
	}},
	{"query.definition", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Query(ctx, "definition", "fixture.Run", 1)
	}},
	{"query.references", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Query(ctx, "references", "fixture.Greeter", 1)
	}},
	{"query.neighborhood", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Query(ctx, "neighborhood", "fixture.Run", 2)
	}},
	{"search", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Search(ctx, "Greet", 10)
	}},
	{"semantic_search", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.SemanticSearch(ctx, "greeting", 5)
	}},
	{"analyze.impact", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: "fixture.Greeter.Greet", MaxNodes: 20})
	}},
	{"agent_brief", func(ctx context.Context, c client.Client) ([]byte, error) {
		body, _, err := c.Brief(ctx, "Greet")
		return body, err
	}},
	{"explain_symbol", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.ExplainSymbol(ctx, "fixture.Greeter.Greet", 5)
	}},
	{"related_files", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.RelatedFiles(ctx, "fixture.Run", "both", 5)
	}},
	{"change_risk", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.ChangeRisk(ctx, "fixture.Run", "", 5)
	}},
	{"dead_code", func(ctx context.Context, c client.Client) ([]byte, error) {
		return c.DeadCode(ctx, client.DeadCodeParams{MaxItems: 5})
	}},
}

// capabilityProbeNames is the fixed, sorted name set SupportsCapability is asked
// about. It is written out rather than derived from a registry so that a
// registry losing an entry shows up as a capability going false, not as a
// shorter question.
var capabilityProbeNames = []string{
	"agent_brief", "analyze", "analyze_contracts", "analyze_githistory",
	"analyze_interproc", "analyze_pdg", "analyze_pr_questions", "analyze_pr_risk",
	"analyze_pr_signals", "analyze_taint", "architecture", "architecture_violations",
	"callees", "callers", "change_impact", "change_risk", "compare_branches",
	"compound", "conflicts_prs", "critique_review", "dead_code", "definition",
	"diagnose", "distill", "explain_symbol", "find_clones", "framework_map",
	"graph_health", "hotspots", "impact", "implements", "inline", "list_prs",
	"memory", "neighborhood", "overrides", "pr_comment", "references",
	"refactor", "refactor_preview", "related_files", "safe_delete", "savings",
	"search", "search_ast", "search_hybrid", "search_semantic", "skillgen",
	"strict_query", "subtypes", "suggest_reviewers", "symbol_context",
	"task_context", "test_impact", "triage_prs", "trust_report", "undo",
}

// recordRuntime characterizes one constructed Runtime.
func recordRuntime(t *testing.T, label string, rt *Runtime, err error, norm *pathNormalizer) startPathRecord {
	t.Helper()
	rec := startPathRecord{Label: label, Ops: map[string]string{}}
	if err != nil {
		rec.Err = norm.apply(err.Error())
		return rec
	}
	rec.Root = norm.apply(rt.Root)
	rec.DBPath = norm.apply(rt.DBPath)
	rec.MetaDir = norm.apply(rt.MetaDir)
	rec.HasStore = rt.Store() != nil
	rec.HasBroker = rt.Broker() != nil

	if reporter, ok := rt.Client.(client.CapabilityReporter); ok {
		for _, name := range capabilityProbeNames {
			if reporter.SupportsCapability(name) {
				rec.Capabilities = append(rec.Capabilities, name)
			}
		}
		sort.Strings(rec.Capabilities)
	}

	ctx := context.Background()
	for _, probe := range charProbes {
		body, perr := probe.run(ctx, rt.Client)
		if perr != nil {
			rec.Ops[probe.name] = "err:" + norm.apply(perr.Error())
			continue
		}
		rec.Ops[probe.name] = fmt.Sprintf("ok:%d:%s", len(body), norm.digest(body))
	}
	return rec
}

// mcpSession drives a real MCP stdio session over a bound client and returns the
// advertised tool list plus a digest per tool call — the bytes an MCP client
// actually receives.
//
// The server is constructed with an already-bound client (mcp.NewServerWithClient)
// rather than with the asynchronous binder, deliberately. The binder's async
// lifecycle — generation counting, roots supersession, the retryable -32002
// window — is refereed by surfaces/mcp_session_journey_subprocess_test.go, which
// this story must leave untouched; replaying it here would characterize a race,
// not a composition. What belongs to AX-07 is what the bind HANDS the surface,
// and that is exactly the client this session is built over. The bind's own
// resolution half is characterized by the "transport-roots" record above, which
// calls the same OpenSession the binder closure in cmd/graphi/serve.go calls.
func mcpSession(t *testing.T, rt *Runtime, norm *pathNormalizer) (tools []string, calls map[string]string) {
	t.Helper()
	srv := mcp.NewServerWithClient(rt.Client, mcp.WithLabs(), mcp.WithRepository(client.Repository{
		Root: rt.Root, DBPath: rt.DBPath, MetaDir: rt.MetaDir,
	}))
	defer srv.Close()
	calls = map[string]string{}

	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name": "search", "arguments": map[string]any{"symbol": "Greet", "depth": 10},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
			"name": "explain_symbol", "arguments": map[string]any{"symbol": "fixture.Greeter.Greet"},
		}},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{
			"name": "callers", "arguments": map[string]any{"symbol": "fixture.Greeter.Greet"},
		}},
		{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{
			"name": "dead_code", "arguments": map[string]any{},
		}},
	}
	callKeys := map[int]string{3: "mcp.search", 4: "mcp.explain_symbol", 5: "mcp.callers", 6: "mcp.dead_code"}

	var in bytes.Buffer
	for _, req := range requests {
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		in.Write(append(body, '\n'))
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), bytes.NewReader(in.Bytes()), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}
	return collectMCPSession(out.String(), callKeys, calls, norm)
}

// collectMCPSession decodes the session transcript into the record's shape.
func collectMCPSession(transcript string, callKeys map[int]string, calls map[string]string, norm *pathNormalizer) ([]string, map[string]string) {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(transcript), "\n") {
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID == 2 {
			for _, tool := range resp.Result.Tools {
				names = append(names, tool.Name)
			}
			continue
		}
		key, ok := callKeys[resp.ID]
		if !ok {
			continue
		}
		switch {
		case resp.Error != nil:
			calls[key] = fmt.Sprintf("err:%d:%s", resp.Error.Code, norm.apply(resp.Error.Message))
		case len(resp.Result.Content) != 1:
			calls[key] = fmt.Sprintf("content:%d", len(resp.Result.Content))
		default:
			text := resp.Result.Content[0].Text
			calls[key] = fmt.Sprintf("ok:%d:%s", len(text), norm.digest([]byte(text)))
		}
	}
	sort.Strings(names)
	return names, calls
}

// captureStartPaths runs every start path once and returns the records, keyed by
// label and ordered deterministically.
//
// stateHome and the fixture repository are supplied by the caller so the same
// capture can be replayed with different composition wiring over IDENTICAL
// inputs.
func captureStartPaths(t *testing.T, repo, stateHome string) []startPathRecord {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", stateHome)
	norm := newPathNormalizer(repo, "<REPO>", stateHome, "<STATE>")
	ctx := context.Background()

	var records []startPathRecord

	// 1. zero-config: the cwd walk decides (Roots nil). This is bare `graphi`
	// and `graphi mcp` with no roots and no pin. It also carries the MCP-session
	// record, because this is the client an MCP bind ends up serving.
	dbPath, metaDir := "", ""
	func() {
		rt, err := OpenSession(ctx, Options{Cwd: repo})
		if rt != nil {
			defer rt.Close()
		}
		rec := recordRuntime(t, "zero-config", rt, err, norm)
		if rt != nil && err == nil {
			dbPath, metaDir = rt.DBPath, rt.MetaDir
			norm.replacements = append(norm.replacements, dbPath, "<DB>", metaDir, "<META>")
			rec.Root, rec.DBPath, rec.MetaDir = norm.apply(rt.Root), norm.apply(rt.DBPath), norm.apply(rt.MetaDir)
		}
		records = append(records, rec)
	}()
	if dbPath == "" {
		t.Fatal("the zero-config start path did not resolve a store; the capture cannot continue")
	}

	// 2. explicit root pin (`graphi mcp -root`, GRAPHI_ROOT).
	func() {
		rt, err := OpenSession(ctx, Options{Root: repo, Cwd: filepath.Join(stateHome, "elsewhere")})
		if rt != nil {
			defer rt.Close()
		}
		records = append(records, recordRuntime(t, "explicit-root", rt, err, norm))
	}()

	// 3. transport roots — the resolution half of the MCP bind: exactly the
	// OpenSession call the binder closure in cmd/graphi/serve.go makes.
	func() {
		rt, err := OpenSession(ctx, Options{Roots: []string{repo}, Cwd: filepath.Join(stateHome, "elsewhere")})
		if rt != nil {
			defer rt.Close()
		}
		rec := recordRuntime(t, "mcp-bind", rt, err, norm)
		if rt != nil && err == nil {
			rec.MCPTools, rec.MCPCalls = mcpSession(t, rt, norm)
		}
		records = append(records, rec)
	}()

	// 4. OpenSession with DBOverride — the documented short-circuit to Attach.
	func() {
		rt, err := OpenSession(ctx, Options{DBOverride: dbPath, Cwd: repo})
		if rt != nil {
			defer rt.Close()
		}
		records = append(records, recordRuntime(t, "opensession-db-override", rt, err, norm))
	}()

	// 5. Attach with an explicit -db (+ -meta) — every CLI verb, and `graphi mcp -db`.
	func() {
		rt, err := Attach(dbPath, "", metaDir)
		if rt != nil {
			defer rt.Close()
		}
		rec := recordRuntime(t, "attach-db", rt, err, norm)
		if rt != nil && err == nil {
			rec.MCPTools, rec.MCPCalls = mcpSession(t, rt, norm)
		}
		records = append(records, rec)
	}()

	// 6. Attach with no store at all — the historical in-memory CLI fallback.
	func() {
		rt, err := Attach("", "", "")
		if rt != nil {
			defer rt.Close()
		}
		records = append(records, recordRuntime(t, "attach-memory", rt, err, norm))
	}()

	// 7. Attach with a -daemon socket. No daemon is running, so every probe
	// fails — which is exactly the behaviour being pinned: the transport is
	// chosen at construction and the failure is a dial failure, not a wiring
	// difference. The socket path is derived from stateHome so its error text
	// normalizes.
	func() {
		socket := filepath.Join(stateHome, "absent.sock")
		rt, err := Attach("", socket, "")
		if rt != nil {
			defer rt.Close()
		}
		records = append(records, recordRuntime(t, "attach-daemon", rt, err, norm))
	}()

	// 8. Lifecycle verbs over a bound session (`graphi sync` / `graphi rebuild`).
	records = append(records, recordLifecycle(t, ctx, repo, norm))

	return records
}

// recordLifecycle characterizes SyncRepo and RebuildRepo over a bound session —
// the `graphi sync` / `graphi rebuild` verbs.
func recordLifecycle(t *testing.T, ctx context.Context, repo string, norm *pathNormalizer) startPathRecord {
	t.Helper()
	rt, err := OpenSession(ctx, Options{Cwd: repo})
	if rt != nil {
		defer rt.Close()
	}
	rec := recordRuntime(t, "lifecycle-verbs", rt, err, norm)
	if err != nil {
		return rec
	}
	rec.Lifecycle = map[string]string{}

	// OpenSession released the ingest lock before returning, so the verbs take
	// it themselves exactly as the CLI verbs do.
	store := rt.Store()
	ing, ierr := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), rt.MetaDir)
	if ierr != nil {
		rec.Lifecycle["open-ingester"] = "err:" + norm.apply(ierr.Error())
		return rec
	}
	defer func() { _ = ing.Close() }()

	stats, serr := SyncRepo(ctx, ing, store, rt.Root, nil)
	rec.Lifecycle["sync"] = lifecycleLine(stats, serr, norm)
	if rerr := RebuildRepo(ctx, ing, store, rt.Root); rerr != nil {
		rec.Lifecycle["rebuild"] = "err:" + norm.apply(rerr.Error())
	} else {
		rec.Lifecycle["rebuild"] = "ok"
	}
	stats2, serr2 := SyncRepo(ctx, ing, store, rt.Root, nil)
	rec.Lifecycle["sync-after-rebuild"] = lifecycleLine(stats2, serr2, norm)
	return rec
}

func lifecycleLine(s SyncStats, err error, norm *pathNormalizer) string {
	if err != nil {
		return "err:" + norm.apply(err.Error())
	}
	return fmt.Sprintf("full=%t checked=%d added=%d changed=%d removed=%d", s.Full, s.Checked, s.Added, s.Changed, s.Removed)
}

// renderRecords serializes a capture for comparison and for a readable diff.
func renderRecords(t *testing.T, records []startPathRecord) string {
	t.Helper()
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatalf("render records: %v", err)
	}
	return string(body)
}

// TestAX07_StartPathCharacterizationIsNonVacuous is the guard that keeps the
// baseline honest. A characterization suite that stopped reaching the engine
// would still compare equal to itself, so the properties that make the record
// evidence are asserted here explicitly.
func TestAX07_StartPathCharacterizationIsNonVacuous(t *testing.T) {
	repo := charRepository(t)
	records := captureStartPaths(t, repo, t.TempDir())

	byLabel := map[string]startPathRecord{}
	for _, rec := range records {
		byLabel[rec.Label] = rec
	}
	for _, label := range []string{
		"zero-config", "explicit-root", "mcp-bind", "opensession-db-override",
		"attach-db", "attach-memory", "attach-daemon", "lifecycle-verbs",
	} {
		if _, ok := byLabel[label]; !ok {
			t.Fatalf("start path %q was not characterized; the suite no longer covers every entry point", label)
		}
	}

	// The graph-backed paths must actually answer over the fixture.
	for _, label := range []string{"zero-config", "explicit-root", "mcp-bind", "attach-db", "lifecycle-verbs"} {
		rec := byLabel[label]
		if rec.Err != "" {
			t.Fatalf("%s failed to construct: %s", label, rec.Err)
		}
		if !rec.HasStore {
			t.Errorf("%s bound no store", label)
		}
		for _, probe := range []string{"query.callers", "search", "explain_symbol", "dead_code"} {
			if got := rec.Ops[probe]; !strings.HasPrefix(got, "ok:") {
				t.Errorf("%s: probe %q did not reach the engine: %s", label, probe, got)
			}
		}
		if len(rec.Capabilities) < 20 {
			t.Errorf("%s advertises only %d capabilities; the capability probe is not reaching a wired client",
				label, len(rec.Capabilities))
		}
	}

	// The MCP session must have bound and listed a real catalog, and every tool
	// call it made must have answered.
	for _, label := range []string{"mcp-bind", "attach-db"} {
		rec := byLabel[label]
		if rec.Root == "" && label == "mcp-bind" {
			t.Error("mcp-bind never resolved a root")
		}
		if len(rec.MCPTools) == 0 {
			t.Errorf("%s advertised no MCP tools", label)
		}
		for _, call := range []string{"mcp.search", "mcp.explain_symbol", "mcp.callers", "mcp.dead_code"} {
			if got := rec.MCPCalls[call]; !strings.HasPrefix(got, "ok:") {
				t.Errorf("%s: %s did not answer: %s", label, call, got)
			}
		}
	}

	// The daemon attach is characterized as the failure it is.
	daemon := byLabel["attach-daemon"]
	if daemon.Err != "" {
		t.Fatalf("attach-daemon must construct (the dial is lazy): %s", daemon.Err)
	}
	if got := daemon.Ops["search"]; !strings.HasPrefix(got, "err:") {
		t.Errorf("attach-daemon search reached something: %s", got)
	}

	// The lifecycle verbs must have run.
	life := byLabel["lifecycle-verbs"]
	for _, verb := range []string{"sync", "rebuild", "sync-after-rebuild"} {
		line := life.Lifecycle[verb]
		if line == "" || strings.HasPrefix(line, "err:") {
			t.Errorf("lifecycle verb %q: %s", verb, line)
		}
	}

	if testing.Verbose() {
		t.Logf("start-path characterization:\n%s", renderRecords(t, records))
	}
}

// TestAX07_StartPathCharacterizationIsDeterministic pins the property the
// cross-composition comparison depends on: taking the same start path twice, in
// one process, over identical inputs, yields the identical record. Without it a
// later "the two compositions agree" claim would rest on a sample that is not
// stable in the first place.
func TestAX07_StartPathCharacterizationIsDeterministic(t *testing.T) {
	repo := charRepository(t)
	first := renderRecords(t, captureStartPaths(t, repo, t.TempDir()))
	second := renderRecords(t, captureStartPaths(t, repo, t.TempDir()))
	if first != second {
		t.Fatalf("the start-path characterization is not deterministic\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
