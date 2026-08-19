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
// THE WORKING DIRECTORY IS DELIBERATELY WRONG IN EVERY TEST HERE (review round
// 1, Critical 1). Round 1 of this file called t.Chdir(repo) before comparing
// the two surfaces, which made both sides resolve the same store from the same
// ambient cwd — so the parity assertion held by construction and was blind to
// the only way these two surfaces can diverge in production. An MCP server is
// routinely launched from outside the repository it binds (cwd=$HOME is the
// common case; -root exists for exactly that), and MCP was passing no paths at
// all, so it read whatever repository the process happened to stand in. The
// tests below therefore run with cwd set to a DIFFERENT indexed repository, and
// assert both directions of the leak: the bound repository's facts must appear,
// and the cwd repository's facts must not.

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

// javaAbstentionFiles FORCES named binder skips: three distinct reasons beside
// one call that genuinely binds, so the pass produces a confirmed edge and a
// legible abstention record at the same time.
var javaAbstentionFiles = map[string]string{
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

// goControlFiles is a repository whose only registrant has no named-skip
// vocabulary: indexed, live, and carrying ZERO abstention counters. It is the
// other end of every cross-repository assertion here.
var goControlFiles = map[string]string{
	"go.mod":       "module example.com/ctl\n\ngo 1.26\n",
	"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
	"main.go":      "package main\n\nimport \"example.com/ctl/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
}

// indexedRepo writes and indexes a repository into ITS OWN auto-managed state
// location and returns the root, the resolved store paths, and a client over
// it. Every repository built here is a complete, independently addressable
// session — which is what lets a test bind one and stand in another.
func indexedRepo(t *testing.T, files map[string]string) (string, client.Repository, client.Client) {
	t.Helper()
	ctx := context.Background()

	repo := t.TempDir()
	for name, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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

	ro, err := graphstore.OpenSQLiteReadOnly(paths.DB)
	if err != nil {
		t.Fatalf("OpenSQLiteReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return repo, client.Repository{Root: repo, DBPath: paths.DB, MetaDir: paths.Meta},
		client.NewDirect(query.New(ro), search.New(ro))
}

// nodeIDIn finds a committed node by qualified name in a named store.
func nodeIDIn(t *testing.T, dbPath, qname string) string {
	t.Helper()
	store, err := graphstore.OpenSQLiteReadOnly(dbPath)
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

// TestStrictQuery_AbstentionIsByteIdenticalOnBothSurfaces is the AC-9 pin, run
// from the WRONG working directory on purpose: the server is bound to the Java
// repository while the process stands in an unrelated indexed Go repository.
// Byte equality against the shared composition — handed the bound repository's
// paths explicitly — then means the two surfaces agree about WHICH REPOSITORY
// they are describing, not merely about formatting.
func TestStrictQuery_AbstentionIsByteIdenticalOnBothSurfaces(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(semantic.EnvJVM, "1")

	_, javaRepo, javaClient := indexedRepo(t, javaAbstentionFiles)
	goRoot, _, _ := indexedRepo(t, goControlFiles)
	t.Chdir(goRoot) // the process stands somewhere else entirely

	target := nodeIDIn(t, javaRepo.DBPath, "tax.value")
	server := NewServerWithClient(javaClient, WithLabs(), WithRepository(javaRepo))

	got := toolText(t, invokeTool(t, server, ToolStrictQuery,
		map[string]any{"operation": "callers", "symbol": target}))
	want, _, _, err := client.ComposeStrictQuery(context.Background(), javaClient,
		client.StrictQueryOptions{
			Operation: "callers", Symbol: target,
			Root: javaRepo.Root, DBPath: javaRepo.DBPath, MetaDir: javaRepo.MetaDir,
		})
	if err != nil {
		t.Fatalf("shared composition: %v", err)
	}
	if got != string(want) {
		t.Fatalf("MCP text diverged from the shared composition:\nMCP:    %s\nshared: %s", got, want)
	}

	// NON-VACUITY: byte equality is also satisfied by two empty documents, so
	// assert the abstention notice is actually in what both surfaces emitted —
	// and that it is the BOUND repository's, reached from a cwd that is not it.
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

// TestMCP_AbstentionNeverLeaksTheWorkingDirectorysRepository is the Critical-1
// regression pin, in the direction that produces a WRONG ANSWER rather than a
// missing one.
//
// The session is bound to the Go repository, which has no named skips at all,
// while the process stands in the Java repository, which has four. Before the
// fix both tools built their option structs with empty Root/DBPath/MetaDir, so
// the compositions resolved the cwd's store and published the Java
// repository's binder abstention as the Go repository's: graph_health returned
// abstention.languages [{java, total 3...}] for a repository containing no
// Java, and strict_query attached the same fabricated notice to a correct Go
// result. Both surfaces are asserted, because both built the same defect.
func TestMCP_AbstentionNeverLeaksTheWorkingDirectorysRepository(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(semantic.EnvJVM, "1")

	javaRoot, javaRepo, _ := indexedRepo(t, javaAbstentionFiles)
	_, goRepo, goClient := indexedRepo(t, goControlFiles)
	t.Chdir(javaRoot) // the contaminating repository IS the working directory

	// Sanity: the cwd repository really does hold the counters that must not
	// appear. Without this the assertions below could pass because nothing was
	// there to leak.
	leaky, _, _, err := client.TrustReport(context.Background(), client.TrustReportOptions{
		Root: javaRepo.Root, DBPath: javaRepo.DBPath, MetaDir: javaRepo.MetaDir,
	})
	if err != nil {
		t.Fatalf("TrustReport over the java repository: %v", err)
	}
	if !strings.Contains(string(leaky), "java_receiver_external") {
		t.Fatalf("fixture assumption broken: the cwd repository holds no abstention counters to leak:\n%s", leaky)
	}

	server := NewServerWithClient(goClient, WithLabs(), WithRepository(goRepo))

	// graph_health over the BOUND repository.
	var doc struct {
		Abstention client.AbstentionFacts `json:"abstention"`
	}
	health := toolText(t, invokeTool(t, server, ToolGraphHealth, map[string]any{}))
	if err := json.Unmarshal([]byte(health), &doc); err != nil {
		t.Fatalf("decode graph_health: %v\n%s", err, health)
	}
	if !doc.Abstention.Available {
		t.Fatalf("the bound repository's abstention record was unreadable: %s", doc.Abstention.UnavailableReason)
	}
	if len(doc.Abstention.Languages) != 0 {
		t.Errorf("graph_health published another repository's abstention counters: %#v", doc.Abstention.Languages)
	}
	for _, forbidden := range []string{"java_receiver_external", "java_var_inferred", "java_lookup_not_found"} {
		if strings.Contains(health, forbidden) {
			t.Errorf("graph_health leaked %q from the working directory's repository:\n%s", forbidden, health)
		}
	}
	// The record is not merely empty — it says WHO recorded it, which is how a
	// reader tells "the binders ran and abstained from nothing" from "no binder
	// ran". An empty list with no provenance would be indistinguishable.
	if len(doc.Abstention.Registrants) == 0 {
		t.Error("the abstention block names no registrants, so its empty language list is uninterpretable")
	}
	for _, l := range doc.Abstention.Registrants {
		if l == "java" {
			t.Errorf("registrants claim java over a Go repository: %#v", doc.Abstention.Registrants)
		}
	}

	// strict_query over the BOUND repository: a correct Go result must not
	// carry the cwd repository's abstention notice.
	target := nodeIDIn(t, goRepo.DBPath, "util.Answer")
	strict := toolText(t, invokeTool(t, server, ToolStrictQuery,
		map[string]any{"operation": "callers", "symbol": target}))
	var env client.StrictEnvelope
	if err := json.Unmarshal([]byte(strict), &env); err != nil {
		t.Fatalf("decode strict_query: %v\n%s", err, strict)
	}
	if len(env.Result.Edges) == 0 {
		t.Fatalf("fixture assumption broken: the Go query returned no edges, so no found result was tested:\n%s", strict)
	}
	for _, l := range env.Limitations {
		if strings.Contains(l, "abstention") || strings.Contains(l, "java_") {
			t.Errorf("strict_query attached another repository's abstention notice to a Go result: %s", l)
		}
	}
}
