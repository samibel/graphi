package ingest

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/taint"
)

// TestIngestAll_ReadFailureLeavesNoCacheRow pins the walk/parse race contract:
// a file that vanishes between the walk (which hashed it) and the parse
// worker's on-demand read must leave the SAME footprint as a file the walk
// never saw — in particular NO content-cache row. Writing the walk-time hash
// for content that was never committed would permanently mask the file from
// drift detection: when it reappears with identical content, the hashes match
// and it is never indexed.
func TestIngestAll_ReadFailureLeavesNoCacheRow(t *testing.T) {
	const goneSrc = "def gone():\n    return 1\n"
	repo := writeRepoIngest(t, map[string]string{
		"a/gone.py":  goneSrc,
		"b/stays.go": "package b\n\nfunc Stays() int { return 2 }\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	i.SetParseWorkers(2)
	gonePath := filepath.Join(repo, "a", "gone.py")
	i.SetParseScheduleHook(func(relPath string) {
		if relPath == "a/gone.py" {
			if err := os.Remove(gonePath); err != nil {
				t.Errorf("remove mid-pass: %v", err)
			}
		}
	})

	ctx := context.Background()
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll with a mid-pass vanish must not fail the pass: %v", err)
	}

	var rows int
	if err := i.meta.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM file_content_cache WHERE path = ?", "a/gone.py").Scan(&rows); err != nil {
		t.Fatalf("query cache: %v", err)
	}
	if rows != 0 {
		t.Fatalf("read-failed file has %d cache row(s); it must leave no footprint (walk-skip semantics)", rows)
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	var stays bool
	for _, n := range nodes {
		if n.SourcePath() == "b/stays.go" {
			stays = true
		}
		if n.SourcePath() == "a/gone.py" {
			t.Fatalf("read-failed file must commit no nodes, found %s", n.QualifiedName())
		}
	}
	if !stays {
		t.Fatal("the unaffected file must still be committed")
	}

	// The file reappears with IDENTICAL content: drift must surface it as
	// Added so the next pass indexes it — the exact signal a stale cache row
	// would have swallowed.
	i.SetParseScheduleHook(nil)
	if err := os.WriteFile(gonePath, []byte(goneSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := i.DriftDetail(ctx, repo, nil)
	if err != nil {
		t.Fatalf("DriftDetail: %v", err)
	}
	var added bool
	for _, p := range d.Added {
		if p == "a/gone.py" {
			added = true
		}
	}
	if !added {
		t.Fatalf("reappeared file must drift as Added; drift = %+v", d)
	}
}

// TestTyperesolvePass_SkipsOnFailedReread pins the fail-closed contract for
// the typeresolve re-read: when a walked file (here go.mod) cannot be read
// back, the whole pass is skipped rather than run with partial input — a
// missing go.mod blanks the module path while every unit still checks
// "non-degraded", and the stale-confirmed sweep would then delete every
// cross-package confirmed edge in the store.
func TestTyperesolvePass_SkipsOnFailedReread(t *testing.T) {
	t.Setenv(EnvNoTyperesolve, "")
	repo := writeRepoIngest(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// go.mod was walked (it is in units) but does NOT exist on disk — the
	// re-read fails exactly like a mid-pass deletion.
	units := []fileUnit{{relPath: "go.mod"}, {relPath: "main.go"}}
	ids, err := i.typeresolvePass(context.Background(), store, repo, units)
	if err != nil {
		t.Fatalf("a failed re-read must skip the pass, not fail it: %v", err)
	}
	if ids != nil {
		t.Fatalf("skipped pass must put no edges, got %v", ids)
	}
}

// TestIngestAll_FailsClosedOnMidPassTaintConfigEdit pins the config/stamp
// consistency contract: the drain analyzes with the config snapshot loaded
// before the parse, the semantics stamp certifies the config's END-of-pass
// state — so a mid-pass taint.json edit must fail the pass (leaving the
// full-pass recovery marker open for a clean re-index under the new config)
// instead of persisting findings that no longer match the stamp.
func TestIngestAll_FailsClosedOnMidPassTaintConfigEdit(t *testing.T) {
	const cfgV1 = `{
	"sinks": [
		{"id": "render_html", "category": "xss", "name_patterns": ["render.HTML"]}
	]
}`
	const cfgV2 = `{
	"sinks": [
		{"id": "render_html", "category": "xss", "name_patterns": ["render.HTML", "render.Raw"]}
	]
}`
	repo := writeRepoIngest(t, map[string]string{
		"go.mod":                                 "module demo\n\ngo 1.21\n",
		"app/a.go":                               "package app\n\nfunc A() int { return 1 }\n",
		"app/b.py":                               "def b():\n    return 2\n",
		taint.ConfigDir + "/" + taint.ConfigFile: cfgV1,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	i.SetParseWorkers(2)
	var edited atomic.Bool
	cfgPath := filepath.Join(repo, taint.ConfigDir, taint.ConfigFile)
	i.SetParseScheduleHook(func(string) {
		if edited.CompareAndSwap(false, true) {
			if err := os.WriteFile(cfgPath, []byte(cfgV2), 0o600); err != nil {
				t.Errorf("rewrite config mid-pass: %v", err)
			}
		}
	})

	err = i.IngestAll(context.Background(), repo)
	if err == nil {
		t.Fatal("a mid-pass taint config edit must fail the pass closed")
	}
	if got := err.Error(); !containsSubstring(got, "taint config changed during the pass") {
		t.Fatalf("error %q must name the mid-pass config change", got)
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
