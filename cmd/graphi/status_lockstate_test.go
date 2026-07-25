package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/state"
)

// statusTestMetaDir resolves the seeded repo's meta sidecar dir exactly the
// way runStatusAt does (DetectRepo → Resolve).
func statusTestMetaDir(t *testing.T, repo string) string {
	t.Helper()
	root, ok := state.DetectRepo(repo)
	if !ok {
		t.Fatalf("fixture repo %q not detectable", repo)
	}
	p, err := state.Resolve(root)
	if err != nil {
		t.Fatalf("resolve state paths: %v", err)
	}
	return p.Meta
}

// reopenFullPassMarker simulates a crashed/running full pass on a certified
// store by re-inserting the recovery marker the ingester clears on success.
func reopenFullPassMarker(t *testing.T, metaDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(filepath.Join(metaDir, "ingest-meta.db"))))
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"INSERT INTO ingest_semantics(key, value) VALUES('full_pass_in_progress', 'deadbeef') ON CONFLICT(key) DO UPDATE SET value = excluded.value"); err != nil {
		t.Fatalf("reopen marker: %v", err)
	}
}

// TestStatus_DistinguishesRunningIndexFromCrashedIndex pins the split of the
// old catch-all "index needs a rebuild": with the full-pass marker open, a
// held ingest lock means an index is running RIGHT NOW (wait), a free lock
// means the previous index died (rebuild). Both are actionable (exit 1) and
// both surface in the JSON document; status itself stays a pure observer.
func TestStatus_DistinguishesRunningIndexFromCrashedIndex(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}
	metaDir := statusTestMetaDir(t, repo)
	reopenFullPassMarker(t, metaDir)

	// Crashed: marker open, lock free.
	before := hashStateDir(t, stateHome)
	var out bytes.Buffer
	if code := runStatusAt(repo, nil, &out); code != 1 {
		t.Fatalf("crashed-index status exit = %d, want 1 (output: %s)", code, out.String())
	}
	for _, want := range []string{
		"status:  previous index did not complete — the next sync will re-run the full pass",
		"hint:    run 'graphi index' to rebuild now with visible progress (the previous index did not complete)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("crashed-index status output missing %q, got: %s", want, out.String())
		}
	}
	if after := hashStateDir(t, stateHome); len(after) != len(before) {
		t.Fatalf("status changed the state dir file set: %d -> %d files", len(before), len(after))
	} else {
		for p, h := range before {
			if after[p] != h {
				t.Fatalf("status modified %s", p)
			}
		}
	}

	out.Reset()
	if code := runStatusAt(repo, []string{"--json"}, &out); code != 1 {
		t.Fatalf("crashed-index status --json exit = %d, want 1", code)
	}
	var doc struct {
		Index struct {
			FullPassInProgress bool `json:"full_pass_in_progress"`
			LockHeld           bool `json:"lock_held"`
		} `json:"index"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("status --json invalid: %v\n%s", err, out.String())
	}
	if !doc.Index.FullPassInProgress || doc.Index.LockHeld {
		t.Fatalf("crashed-index JSON = %+v, want marker=true lock=false", doc.Index)
	}

	// Running: marker open, lock held by "another process" (this test).
	lockDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(0)", filepath.ToSlash(ingestlock.Path(metaDir))))
	if err != nil {
		t.Fatalf("open lock db: %v", err)
	}
	defer lockDB.Close()
	conn, err := lockDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("lock conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	out.Reset()
	if code := runStatusAt(repo, nil, &out); code != 1 {
		t.Fatalf("running-index status exit = %d, want 1 (output: %s)", code, out.String())
	}
	for _, want := range []string{
		"status:  indexing in progress — another graphi process is building this index",
		"hint:    wait for the running index to finish — another graphi process is building it",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("running-index status output missing %q, got: %s", want, out.String())
		}
	}

	out.Reset()
	if code := runStatusAt(repo, []string{"--json"}, &out); code != 1 {
		t.Fatalf("running-index status --json exit = %d, want 1", code)
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("status --json invalid: %v\n%s", err, out.String())
	}
	if !doc.Index.FullPassInProgress || !doc.Index.LockHeld {
		t.Fatalf("running-index JSON = %+v, want marker=true lock=true", doc.Index)
	}
}
