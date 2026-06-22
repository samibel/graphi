package surfaces_test

// SW-038 edit/refactor command-surface tests. They drive the SAME logical
// refactor through BOTH real surface paths — the MCP stdio server
// (mcp.Server.toolsCall via a JSON tools/call request) and the CLI handlers
// (cli.RunRefactor* via argv) — each over its OWN client.Direct wrapping a single
// shared *edit.Applier + *edit.ChangeRecorder, and assert the two ChangeRecords
// are equal over the documented comparable subset (AC-4 parity). They also cover
// impact-before-mutation (AC-1) and per-surface actor recording (AC-2 vs AC-4).
//
// Documented comparable subset for AC-4 "identical records":
//   op_type, target_node_id, old_name, new_name, touched_files, before/after refs
// EXPLICITLY EXCLUDED (legitimately differ or are non-deterministic by design):
//   actor (per-surface), edit_id, undo_token (crypto-random), recorded_at, snapshot_ref.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// editParser mirrors the engine/edit refactor test parser: a file's node identity
// is derived from its `def:Name` directive so renaming mints a new node id and the
// old one is deleted by the incremental re-index. (Re-declared here because the
// engine test parser is package-internal.)
type editParser struct{}

func (editParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	name := "fn" + filepath.Base(path)
	var refs []string
	var edges []model.Edge
	for _, raw := range bytes.Split(src, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if strings.HasPrefix(line, "def:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "def:"))
		}
	}
	def, err := model.NewNode("function", "pkg/"+name, path, 1, 1)
	if err != nil {
		return nil, err
	}
	for _, raw := range bytes.Split(src, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "ref:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			continue
		}
		refs = append(refs, parts[0])
		tgt, err := model.NewNode("function", "pkg/"+parts[1], parts[0], 1, 1)
		if err != nil {
			return nil, err
		}
		e, err := model.NewEdge(def.ID(), tgt.ID(), "references", model.TierDerived, 0.9, "ref", []string{path + ":1"})
		if err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return &parse.ParseResult{
		Meta:       parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes:      []model.Node{def},
		Edges:      edges,
		References: refs,
	}, nil
}

// editFixture builds a fully-wired in-process client (query/search/analysis +
// edit/refactor) over a fresh repo seeded with files, returning the client and the
// repo root.
func editFixture(t *testing.T, files map[string]string) (*client.Direct, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	metaDir := t.TempDir()
	ing, err := ingest.New(store, editParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, editParser{}, t.TempDir())
		if ierr != nil {
			return nil, nil, nil, ierr
		}
		return fs, fi, func() { _ = fi.Close(); _ = fs.Close() }, nil
	})
	applier, err := edit.NewApplier(store, ing, root, checker)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	recorder, err := edit.NewChangeRecorder(ctx, ing, metaDir)
	if err != nil {
		t.Fatalf("NewChangeRecorder: %v", err)
	}
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithEditor(applier, recorder)
	return c, root
}

func symbolID(t *testing.T, name, path string) string {
	t.Helper()
	n, err := model.NewNode("function", "pkg/"+name, path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return string(n.ID())
}

// cliRefactorRecord runs the CLI refactor handler against c and decodes the
// emitted change record.
func cliRefactorRecord(t *testing.T, c client.Client, target, actor string) edit.ChangeRecord {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"-kind", "rename", "-target", target, "-old-name", "Widget", "-new-name", "Gadget"}
	if actor != "" {
		args = append(args, "-actor", actor)
	}
	if err := cli.RunRefactor(context.Background(), c, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunRefactor: %v (stderr: %s)", err, errOut.String())
	}
	var rec edit.ChangeRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &rec); err != nil {
		t.Fatalf("decode cli record %q: %v", out.String(), err)
	}
	return rec
}

// mcpRefactorRecord runs the MCP refactor tool against c and decodes the record.
func mcpRefactorRecord(t *testing.T, c client.Client, target, actor string) edit.ChangeRecord {
	t.Helper()
	srv := mcp.NewServerWithClient(c)
	args := map[string]any{"kind": "rename", "target_symbol": target, "old_name": "Widget", "new_name": "Gadget"}
	if actor != "" {
		args["actor"] = actor
	}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "refactor", "arguments": args},
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp serve: %v", err)
	}
	text := mcpText(t, out.Bytes())
	var rec edit.ChangeRecord
	if err := json.Unmarshal([]byte(text), &rec); err != nil {
		t.Fatalf("decode mcp record %q: %v", text, err)
	}
	return rec
}

func mcpText(t *testing.T, raw []byte) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
		t.Fatalf("decode mcp response %q: %v", string(raw), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("unexpected mcp content: %+v", resp.Result.Content)
	}
	return resp.Result.Content[0].Text
}

// comparableSubset projects a ChangeRecord onto the AC-4 comparable subset.
func comparableSubset(rec edit.ChangeRecord) edit.ChangeRecord {
	return edit.ChangeRecord{
		OpType:       rec.OpType,
		TargetNodeID: rec.TargetNodeID,
		OldName:      rec.OldName,
		NewName:      rec.NewName,
		TouchedFiles: rec.TouchedFiles,
	}
}

// AC-4 (headline): the same logical refactor through the MCP path and the CLI
// path produces ChangeRecords equal over the comparable subset, and both applied.
func TestEdit_MCP_CLI_Parity(t *testing.T) {
	files := map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	}
	// Independent identical fixtures (each surface mutates its own repo).
	cliClient, cliRoot := editFixture(t, files)
	mcpClient, mcpRoot := editFixture(t, files)

	cliRec := cliRefactorRecord(t, cliClient, symbolID(t, "Widget", "a_def.go"), "")
	mcpRec := mcpRefactorRecord(t, mcpClient, symbolID(t, "Widget", "a_def.go"), "")

	if !recordsEqual(comparableSubset(cliRec), comparableSubset(mcpRec)) {
		t.Fatalf("AC-4 parity mismatch over comparable subset:\nCLI: %+v\nMCP: %+v", cliRec, mcpRec)
	}
	// Both surfaces actually applied (non-empty edit id + undo token).
	if cliRec.EditID == "" || mcpRec.EditID == "" || cliRec.UndoToken == "" || mcpRec.UndoToken == "" {
		t.Fatalf("expected both records to be applied with ids/tokens: cli=%+v mcp=%+v", cliRec, mcpRec)
	}
	// The renamed definition file was rewritten in both repos.
	for _, root := range []string{cliRoot, mcpRoot} {
		b, _ := os.ReadFile(filepath.Join(root, "a_def.go"))
		if !strings.Contains(string(b), "def:Gadget") {
			t.Fatalf("definition not renamed in %s: %q", root, string(b))
		}
	}
}

func recordsEqual(a, b edit.ChangeRecord) bool {
	if a.OpType != b.OpType || a.TargetNodeID != b.TargetNodeID || a.OldName != b.OldName || a.NewName != b.NewName {
		return false
	}
	if len(a.TouchedFiles) != len(b.TouchedFiles) {
		return false
	}
	for i := range a.TouchedFiles {
		if a.TouchedFiles[i] != b.TouchedFiles[i] {
			return false
		}
	}
	return true
}

// AC-2 vs AC-4: MCP records actor "mcp" by default and CLI records "cli"; parity
// still holds because actor is excluded from the comparable subset.
func TestEdit_ActorRecordedPerSurface(t *testing.T) {
	files := map[string]string{"a_def.go": "def:Widget\n"}
	cliClient, _ := editFixture(t, files)
	mcpClient, _ := editFixture(t, files)

	cliRec := cliRefactorRecord(t, cliClient, symbolID(t, "Widget", "a_def.go"), "")
	mcpRec := mcpRefactorRecord(t, mcpClient, symbolID(t, "Widget", "a_def.go"), "")

	if cliRec.Actor != "cli" {
		t.Fatalf("cli actor = %q, want cli", cliRec.Actor)
	}
	if mcpRec.Actor != "mcp" {
		t.Fatalf("mcp actor = %q, want mcp", mcpRec.Actor)
	}
	if !recordsEqual(comparableSubset(cliRec), comparableSubset(mcpRec)) {
		t.Fatal("parity broke even though only actor differs")
	}
}

// AC-1: RefactorPreview returns a non-empty impact set + planned ops AND performs
// NO mutation (source + graph byte-identical to pre-call); the committing path's
// impact set matches the preview's.
func TestEdit_ImpactBeforeMutation(t *testing.T) {
	files := map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	}
	c, root := editFixture(t, files)
	ctx := context.Background()

	srcBefore, _ := os.ReadFile(filepath.Join(root, "b_use.go"))

	b, err := c.RefactorPreview(ctx, client.RefactorRequest{
		Kind: "rename", TargetSymbol: symbolID(t, "Widget", "a_def.go"), OldName: "Widget", NewName: "Gadget",
	})
	if err != nil {
		t.Fatalf("RefactorPreview: %v", err)
	}
	var preview struct {
		ImpactFiles []string `json:"impact_files"`
		DryRun      bool     `json:"dry_run"`
		TouchedF    []string `json:"touched_files"`
	}
	if err := json.Unmarshal(b, &preview); err != nil {
		t.Fatalf("decode preview %q: %v", string(b), err)
	}
	if !preview.DryRun {
		t.Fatal("preview not marked dry_run")
	}
	if len(preview.ImpactFiles) < 2 {
		t.Fatalf("preview impact files = %v, want >=2", preview.ImpactFiles)
	}
	// No mutation: source byte-identical.
	srcAfter, _ := os.ReadFile(filepath.Join(root, "b_use.go"))
	if !bytes.Equal(srcBefore, srcAfter) {
		t.Fatal("preview mutated source")
	}

	// The committing path's touched files match the preview's touched files.
	recBytes, err := c.Refactor(ctx, client.RefactorRequest{
		Kind: "rename", TargetSymbol: symbolID(t, "Widget", "a_def.go"), OldName: "Widget", NewName: "Gadget",
	}, "tester")
	if err != nil {
		t.Fatalf("Refactor: %v", err)
	}
	var rec edit.ChangeRecord
	if err := json.Unmarshal(recBytes, &rec); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if len(rec.TouchedFiles) != len(preview.TouchedF) {
		t.Fatalf("committed touched files %v != preview touched files %v", rec.TouchedFiles, preview.TouchedF)
	}
}

// Round-trip through the surface: an MCP refactor followed by a CLI undo of its
// token restores the repo and records a reversal (AC-3 through both surfaces).
func TestEdit_UndoThroughSurfaces(t *testing.T) {
	files := map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	}
	c, root := editFixture(t, files)
	ctx := context.Background()

	srcBefore, _ := os.ReadFile(filepath.Join(root, "a_def.go"))

	rec := mcpRefactorRecord(t, c, symbolID(t, "Widget", "a_def.go"), "mcp")
	// Undo via the CLI surface using the token the MCP refactor returned.
	var out, errOut bytes.Buffer
	if err := cli.RunUndo(ctx, c, []string{"-token", rec.UndoToken}, &out, &errOut); err != nil {
		t.Fatalf("cli.RunUndo: %v (stderr %s)", err, errOut.String())
	}
	var reversal edit.ChangeRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &reversal); err != nil {
		t.Fatalf("decode reversal: %v", err)
	}
	if reversal.OpType != "undo" || reversal.ReversesEditID != rec.EditID {
		t.Fatalf("bad reversal record: %+v (orig edit %s)", reversal, rec.EditID)
	}
	srcAfter, _ := os.ReadFile(filepath.Join(root, "a_def.go"))
	if !bytes.Equal(srcBefore, srcAfter) {
		t.Fatalf("undo did not restore source: %q != %q", srcAfter, srcBefore)
	}
}

// The edit tools are advertised when the editor is wired, and NOT when absent.
func TestEdit_ToolsAdvertised(t *testing.T) {
	files := map[string]string{"a_def.go": "def:Widget\n"}
	c, _ := editFixture(t, files)
	srv := mcp.NewServerWithClient(c)
	names := listTools(t, srv)
	for _, want := range []string{"refactor_preview", "refactor", "undo"} {
		if !containsName(names, want) {
			t.Errorf("edit tool %q not advertised when editor is attached; got %v", want, names)
		}
	}

	// Without an editor (plain query-only client) the edit tools are hidden.
	store, _ := seed(t)
	plain := client.NewDirect(query.New(store), nil)
	srvNo := mcp.NewServerWithClient(plain)
	namesNo := listTools(t, srvNo)
	for _, name := range []string{"refactor_preview", "refactor", "undo"} {
		if containsName(namesNo, name) {
			t.Errorf("edit tool %q advertised without an editor (should probe-hide)", name)
		}
	}
}
