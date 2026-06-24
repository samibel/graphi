package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// TestPHPLink_Resolves: require 'lib/helpers.php' → cross-dir helper() heuristic +
// file→file imports edge.
func TestPHPLink_Resolves(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.php", "app/main.php"),
		mustNode(t, "function", "app.checkout", "app/main.php"),
		mustNode(t, "file", "app/lib/helpers.php", "app/lib/helpers.php"),
		mustNode(t, "function", "lib.helper", "app/lib/helpers.php"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.php",
		Dir:        "app",
		Language:   "php",
		Imports:    []parse.ImportSpec{{Path: "lib/helpers.php"}},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", Name: "helper", Kind: "calls", Line: 3, Selector: false},
		},
	}}
	edges, _, err := New().Link("php", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "lib.helper", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
	mainFile := idOfQN(t, nodes, "app/main.php")
	helpers := idOfQN(t, nodes, "app/lib/helpers.php")
	var foundImport bool
	for _, e := range edges {
		if e.From() == mainFile && e.To() == helpers && e.Kind() == "imports" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("missing imports edge main.php -> lib/helpers.php")
	}
}

// TestLuaLink_Resolves: local m = require('util'); m.helper() resolves in util.lua's
// directory (selector via ambient require dir).
func TestLuaLink_Resolves(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.lua", "app/main.lua"),
		mustNode(t, "function", "app.checkout", "app/main.lua"),
		mustNode(t, "file", "app/util.lua", "app/util.lua"),
		mustNode(t, "function", "app.helper", "app/util.lua"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.lua",
		Dir:        "app",
		Language:   "lua",
		Imports:    []parse.ImportSpec{{Alias: "", Path: "util"}},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", SelectorBase: "util", Name: "helper", Kind: "calls", Line: 2, Selector: true},
		},
	}}
	edges, _, err := New().Link("lua", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "app.helper", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
}

// TestBashLink_Resolves: source ./lib/util.sh → cross-dir helper() heuristic.
func TestBashLink_Resolves(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.sh", "app/main.sh"),
		mustNode(t, "function", "app.checkout", "app/main.sh"),
		mustNode(t, "file", "app/lib/util.sh", "app/lib/util.sh"),
		mustNode(t, "function", "lib.helper", "app/lib/util.sh"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.sh",
		Dir:        "app",
		Language:   "bash",
		Imports:    []parse.ImportSpec{{Path: "./lib/util.sh"}},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", Name: "helper", Kind: "calls", Line: 2, Selector: false},
		},
	}}
	edges, _, err := New().Link("bash", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "lib.helper", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
}

// TestRubyLink_ImportsEdge: require_relative 'util' yields a file→file imports edge
// (the Ruby extractor records the require; bare method calls are not emitted as
// pending at this tier, so no call edge is fabricated).
func TestRubyLink_ImportsEdge(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.rb", "app/main.rb"),
		mustNode(t, "function", "app.checkout", "app/main.rb"),
		mustNode(t, "file", "app/util.rb", "app/util.rb"),
		mustNode(t, "function", "app.helper", "app/util.rb"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.rb",
		Dir:        "app",
		Language:   "ruby",
		Imports:    []parse.ImportSpec{{Path: "util"}},
	}}
	edges, _, err := New().Link("ruby", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertNoPhantomNoConfirmed(t, nodes, edges)
	mainFile := idOfQN(t, nodes, "app/main.rb")
	util := idOfQN(t, nodes, "app/util.rb")
	var foundImport bool
	for _, e := range edges {
		if e.From() == mainFile && e.To() == util && e.Kind() == "imports" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("missing imports edge main.rb -> util.rb")
	}
}

// TestSQLLink_NoOp: the SQL resolver is an honest no-op (nothing is provably
// resolvable at this tier) — it emits no edges and never panics.
func TestSQLLink_NoOp(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "db/schema.sql", "db/schema.sql"),
		mustNode(t, "type", "schema.users", "db/schema.sql"),
	}
	files := []FileRefs{{SourcePath: "db/schema.sql", Dir: "db", Language: "sql"}}
	edges, st, err := New().Link("sql", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("SQL resolver emitted %d edges, want 0", len(edges))
	}
	if st.ResolvedDerived+st.ResolvedHeuristic != 0 {
		t.Errorf("SQL resolver resolved %d edges, want 0", st.ResolvedDerived+st.ResolvedHeuristic)
	}
}
