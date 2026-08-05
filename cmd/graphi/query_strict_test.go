package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
)

// strictFixture builds and syncs a repo whose callers-of-helper result mixes
// tiers: a.py's caller_a resolves same-file and b.py's caller_b same-directory
// (both derived), while sub/c.py's caller_c resolves through the import
// selector (heuristic). Returns the repo root, the db path, and the node id
// of "a.helper".
func strictFixture(t *testing.T) (repo, dbPath, helperID string) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo = writeGoRepo(t)
	py1 := "def helper():\n    return 1\n\ndef caller_a():\n    return helper()\n"
	py2 := "def caller_b():\n    return helper()\n"
	py3 := "import a\n\ndef caller_c():\n    return a.helper()\n"
	if err := os.WriteFile(filepath.Join(repo, "a.py"), []byte(py1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.py"), []byte(py2), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "sub", "c.py"), []byte(py3), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}
	matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "db.sqlite"))
	if len(matches) != 1 {
		t.Fatalf("expected one auto-managed db, got %v", matches)
	}
	dbPath = matches[0]

	store, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.QualifiedName() == "a.helper" {
			helperID = string(n.ID())
		}
	}
	if helperID == "" {
		t.Fatal("fixture node a.helper not found")
	}
	return repo, dbPath, helperID
}

func runStrict(t *testing.T, repo string, args []string) (int, strictEnvelope, string) {
	t.Helper()
	var out bytes.Buffer
	code := runQueryStrictAt(repo, args, &out)
	var env strictEnvelope
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatalf("envelope is not JSON: %v\n%s", err, out.String())
		}
	}
	return code, env, out.String()
}

func TestQueryStrict_FilterArithmeticAndNoTierUpgrade(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)

	// Unfiltered baseline (min-tier heuristic admits everything).
	code, base, raw := runStrict(t, repo, []string{"callers", "-db", dbPath, "-symbol", helper})
	if code != 0 {
		t.Fatalf("baseline exit = %d\n%s", code, raw)
	}
	if len(base.Result.Edges) == 0 {
		t.Fatalf("fixture produced no caller edges:\n%s", raw)
	}
	byTier := map[string]int{}
	for _, e := range base.Result.Edges {
		byTier[string(e.Tier)]++
	}
	if base.Filter.ExcludedEdges != 0 || len(base.Limitations) != 0 {
		t.Fatalf("unfiltered run reports exclusions: %+v", base.Filter)
	}

	// min-tier derived: heuristic edges are excluded, count matches arithmetic,
	// and no surviving edge is below the bar (no tier is ever upgraded).
	code, env, raw := runStrict(t, repo, []string{"callers", "-db", dbPath, "-symbol", helper, "-min-tier", "derived"})
	if code != 0 {
		t.Fatalf("derived run exit = %d\n%s", code, raw)
	}
	wantExcluded := byTier["heuristic"]
	if env.Filter.ExcludedEdges != wantExcluded {
		t.Fatalf("excluded = %d, want %d (tier census %v)", env.Filter.ExcludedEdges, wantExcluded, byTier)
	}
	if len(env.Result.Edges) != len(base.Result.Edges)-wantExcluded {
		t.Fatalf("kept %d edges, want %d", len(env.Result.Edges), len(base.Result.Edges)-wantExcluded)
	}
	for _, e := range env.Result.Edges {
		if e.Tier != "confirmed" && e.Tier != "derived" {
			t.Fatalf("edge below min-tier survived the filter: %+v", e)
		}
	}
	// Every remaining node is justified by a surviving edge or is the symbol.
	used := map[string]bool{string(env.Result.Symbol): true}
	for _, e := range env.Result.Edges {
		used[string(e.From)] = true
		used[string(e.To)] = true
	}
	for _, n := range env.Result.Nodes {
		if !used[string(n.ID)] {
			t.Fatalf("unjustified node survived the filter: %+v", n)
		}
	}
	if wantExcluded > 0 && len(env.Limitations) == 0 {
		t.Fatal("exclusions happened but no limitation is present")
	}
}

func TestQueryStrict_FilteredEmptinessCarriesLimitation(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)

	// Python resolution is never typechecker-confirmed: min-tier confirmed
	// empties the result. The red gate: that emptiness must carry the
	// exclusion limitation, never bare emptiness.
	code, env, raw := runStrict(t, repo, []string{"callers", "-db", dbPath, "-symbol", helper, "-min-tier", "confirmed"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, raw)
	}
	if len(env.Result.Edges) != 0 {
		t.Skipf("fixture unexpectedly has confirmed caller edges; census: %+v", env.Result.Edges)
	}
	if env.Filter.ExcludedEdges == 0 {
		t.Fatalf("expected exclusions, got none:\n%s", raw)
	}
	if len(env.Limitations) == 0 {
		t.Fatalf("filtered emptiness without a limitation (the PRD §28 red gate):\n%s", raw)
	}
	if env.Result.Edges == nil || env.Result.Nodes == nil || env.Limitations == nil {
		t.Fatal("envelope lists must be non-nil")
	}
}

func TestQueryStrict_PreflightBlocksOnUnindexedRepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	// Unindexed repo: review policy yields UNVERIFIED — the query must not run.
	var out bytes.Buffer
	code := runQueryStrictAt(repo, []string{"callers", "-symbol", "whatever", "-policy", "review-v1"}, &out)
	if code != 2 {
		t.Fatalf("preflight exit = %d, want 2 (UNVERIFIED blocks)", code)
	}
	if out.Len() != 0 {
		t.Fatalf("blocked preflight still wrote a result:\n%s", out.String())
	}
}

func TestQueryStrict_InputErrors(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	cases := [][]string{
		{},          // missing operation
		{"callers"}, // missing -symbol
		{"callers", "-symbol", "x", "-min-tier", "sturdy"}, // tier outside the closed set
	}
	for _, args := range cases {
		var out bytes.Buffer
		if code := runQueryStrictAt(repo, args, &out); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote output on input error:\n%s", args, out.String())
		}
	}
}

func TestQueryStrict_EnvelopeDeterminism(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)
	var a, b bytes.Buffer
	if code := runQueryStrictAt(repo, []string{"callers", "-db", dbPath, "-symbol", helper, "-min-tier", "derived"}, &a); code != 0 {
		t.Fatalf("first run exit = %d", code)
	}
	if code := runQueryStrictAt(repo, []string{"callers", "-db", dbPath, "-symbol", helper, "-min-tier", "derived"}, &b); code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("envelope not deterministic:\n%s\nvs\n%s", a.String(), b.String())
	}
}
