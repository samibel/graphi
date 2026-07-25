package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/ingestlock"
)

// fabricateSidecar creates a minimal ingest-meta.db with the ingest_semantics
// table, optionally holding the full-pass marker — the on-disk shape the
// ingester leaves behind (engine/ingest).
func fabricateSidecar(t *testing.T, metaDir string, withMarker bool) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(filepath.Join(metaDir, ingestMetaFileName))))
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ingest_semantics (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create ingest_semantics: %v", err)
	}
	if withMarker {
		if _, err := db.Exec("INSERT INTO ingest_semantics(key, value) VALUES(?, ?)", fullPassInProgressKey, "deadbeef"); err != nil {
			t.Fatalf("insert marker: %v", err)
		}
	}
}

// holdIngestLock takes the cross-process lock the way the runtime does.
func holdIngestLock(t *testing.T, metaDir string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(0)", filepath.ToSlash(ingestlock.Path(metaDir))))
	if err != nil {
		t.Fatalf("open lock db: %v", err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open lock conn: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		_ = db.Close()
	}
}

// TestIndexCheck_Matrix pins the status/message/action matrix over the two
// axes the check exists to separate: the full-pass recovery marker and the
// cross-process ingest lock.
func TestIndexCheck_Matrix(t *testing.T) {
	const root = "/work/mono"
	for _, tc := range []struct {
		name        string
		metaDir     bool // false: not inside a repository
		sidecar     bool
		marker      bool
		holdLock    bool
		wantStatus  Status
		wantMessage string
		wantAction  string
	}{
		{
			name:        "outside a repository",
			wantStatus:  StatusInfo,
			wantMessage: "not inside a repository; no index state to inspect",
		},
		{
			name:        "no state yet",
			metaDir:     true,
			wantStatus:  StatusInfo,
			wantMessage: "no index state yet for /work/mono",
			wantAction:  "run `graphi index` to build it",
		},
		{
			name:        "marker present, lock held: index running",
			metaDir:     true,
			sidecar:     true,
			marker:      true,
			holdLock:    true,
			wantStatus:  StatusInfo,
			wantMessage: "a full index of /work/mono is running in another graphi process (in-flight marker present, ingest lock held)",
			wantAction:  "wait for it to finish; do not run `graphi index` concurrently — it would queue behind the same lock",
		},
		{
			name:        "marker present, lock free: crashed index",
			metaDir:     true,
			sidecar:     true,
			marker:      true,
			wantStatus:  StatusWarn,
			wantMessage: "a previous full index of /work/mono did not complete (stale in-flight marker); the next session will re-run the full pass from scratch",
			wantAction:  "run `graphi index` in a terminal to rebuild now with visible progress",
		},
		{
			name:        "no marker, lock held: sync in progress",
			metaDir:     true,
			sidecar:     true,
			holdLock:    true,
			wantStatus:  StatusInfo,
			wantMessage: "another graphi process holds the ingest lock for /work/mono (sync or index in progress)",
			wantAction:  "wait for it to finish",
		},
		{
			name:        "no marker, lock free: consistent",
			metaDir:     true,
			sidecar:     true,
			wantStatus:  StatusPass,
			wantMessage: "index state is consistent; no full pass in flight",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metaDir := ""
			if tc.metaDir {
				metaDir = t.TempDir()
			}
			if tc.sidecar {
				fabricateSidecar(t, metaDir, tc.marker)
			}
			if tc.holdLock {
				release := holdIngestLock(t, metaDir)
				defer release()
			}
			res := IndexCheck(root, metaDir).Run(context.Background(), nil)
			if res.ID != "index" || res.Category != "index" {
				t.Fatalf("id/category = %q/%q, want index/index", res.ID, res.Category)
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (message %q)", res.Status, tc.wantStatus, res.Message)
			}
			if res.Message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", res.Message, tc.wantMessage)
			}
			if res.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q", res.Action, tc.wantAction)
			}
		})
	}
}

// TestIndexCheck_ReadOnly pins that the check never mints state: probing a
// repo that has a sidecar but no lock DB must not create the lock DB, and
// probing an empty meta dir must leave it empty.
func TestIndexCheck_ReadOnly(t *testing.T) {
	metaDir := t.TempDir()
	fabricateSidecar(t, metaDir, false)
	if res := IndexCheck("/work/mono", metaDir).Run(context.Background(), nil); res.Status != StatusPass {
		t.Fatalf("status = %q, want %q", res.Status, StatusPass)
	}
	if state, err := ingestlock.Probe(context.Background(), metaDir); err != nil || state != ingestlock.StateAbsent {
		t.Fatalf("lock DB state after check = %q, %v; want %q (check must not create it)", state, err, ingestlock.StateAbsent)
	}
}
