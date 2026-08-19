package mcp

// W0.g AC-9 — abstention must be visible on BOTH CLI and MCP, and the two must
// be byte-identical BY CONSTRUCTION rather than by reconciliation.
//
// The structural argument is that both surfaces call the one shared
// composition (surfaces/client.ComposeStrictQuery), which is also what
// `graphi query-strict` writes. Structure can be refactored away silently, so
// this file pins it executably on the abstention path specifically — and pins
// it NON-VACUOUSLY: a byte-equality assertion is satisfied by two identically
// EMPTY documents, so the test additionally asserts that the abstention notice
// is genuinely present in what both surfaces produced.
//
// The fixture is hermetic. MCP deliberately passes no Root/DBPath/MetaDir —
// the composition resolves the server process's own repository — so the test
// redirects the state home and the working directory instead of injecting
// paths the tool has no argument for.

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
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/semantic"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// javaAbstentionRepo writes and indexes a Java repository that FORCES named
// binder skips, into the auto-managed store the composition resolves from the
// process's working directory. Returns the repo root and a client over it.
func javaAbstentionRepo(t *testing.T) (string, client.Client) {
	t.Helper()
	ctx := context.Background()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(semantic.EnvJVM, "1")

	repo := t.TempDir()
	files := map[string]string{
		"com/tax/Rate.java": `package com.tax;
public class Rate {
    public void value() {}
}
`,
		"com/shop/Shop.java": `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        var inferred = param;
        inferred.value();
        param.missing();
        java.util.List<String> ext = null;
        ext.size();
        Unknown u = null;
        u.thing();
    }
}
`,
	}
	for name, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Index into the auto-managed location for this repo, which is exactly
	// where the composition will look when handed no explicit paths.
	paths, err := state.Resolve(repo)
	if err != nil {
		t.Fatalf("state.Resolve: %v", err)
	}
	if err := os.MkdirAll(paths.Meta, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	store, err := graphstore.OpenSQLite(paths.DB)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), paths.Meta)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(ctx, repo); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close ingester: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// The composition resolves the repository from the process's cwd.
	t.Chdir(repo)
	return repo, client.NewDirect(query.New(store), search.New(store))
}

// javaNodeID finds a committed node by qualified name.
func javaNodeID(t *testing.T, c client.Client, qname string) string {
	t.Helper()
	paths, err := state.Resolve(".")
	if err != nil {
		t.Fatalf("state.Resolve: %v", err)
	}
	store, err := graphstore.OpenSQLiteReadOnly(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, n := range nodes {
		if n.QualifiedName() == qname {
			return string(n.ID())
		}
	}
	t.Fatalf("node %q not found", qname)
	return ""
}

// TestStrictQuery_AbstentionIsByteIdenticalOnBothSurfaces is the AC-9 pin.
func TestStrictQuery_AbstentionIsByteIdenticalOnBothSurfaces(t *testing.T) {
	_, c := javaAbstentionRepo(t)
	target := javaNodeID(t, c, "tax.value")
	server := NewServerWithClient(c, WithLabs())

	got := toolText(t, invokeTool(t, server, ToolStrictQuery,
		map[string]any{"operation": "callers", "symbol": target}))
	want, _, _, err := client.ComposeStrictQuery(context.Background(), c,
		client.StrictQueryOptions{Operation: "callers", Symbol: target})
	if err != nil {
		t.Fatalf("shared composition: %v", err)
	}
	if got != string(want) {
		t.Fatalf("MCP text diverged from the shared composition:\nMCP:    %s\nshared: %s", got, want)
	}

	// NON-VACUITY: byte equality is also satisfied by two empty documents, so
	// assert the abstention notice is actually in what both surfaces emitted.
	var env client.StrictEnvelope
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("decode MCP envelope: %v\n%s", err, got)
	}
	var found string
	for _, l := range env.Limitations {
		if strings.Contains(l, "abstention") {
			found = l
		}
	}
	if found == "" {
		t.Fatalf("neither surface carried an abstention notice, so the parity assertion proved nothing: %#v", env.Limitations)
	}
	for _, want := range []string{"java_var_inferred 1", "java_receiver_external 2", "repository-global"} {
		if !strings.Contains(found, want) {
			t.Errorf("the MCP-visible notice does not carry %q: %s", want, found)
		}
	}
}
