package ingest_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// hashDir maps every database file under dir to its content hash, so a test
// can prove a read-only pass left the stored content byte-identical. The
// transient WAL coordination sidecars (-wal/-shm) are excluded: SQLite creates
// them (empty) even for mode=ro reads of a WAL database — see the
// OpenSQLiteReadOnly doc comment.
func hashDir(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := make(map[string][32]byte)
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", dir, err)
	}
	return out
}

// requireEmptyWALs asserts that any WAL sidecar present under dir is empty —
// i.e. the read-only pass coordinated through WAL but never committed a frame.
func requireEmptyWALs(t *testing.T, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, "-wal") && fi.Size() != 0 {
			t.Errorf("read-only pass wrote %d bytes to %s", fi.Size(), p)
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}

// TestNewReadOnly_ObservesWithoutWriting pins the `graphi status` machinery: a
// read-only Ingester over a store+sidecar built by a full pass can warm-start
// probe and classify drift, refuses every mutation, and leaves both database
// files byte-identical.
func TestNewReadOnly_ObservesWithoutWriting(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "db.sqlite")
	metaDir := filepath.Join(stateDir, "meta")

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package a\n\nuse:a.go\n",
		"c.go": "package a\n",
	})

	// Build the store the way `graphi sync` would.
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ing, err := ingest.New(store, &stubParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close ingester: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	before := hashDir(t, stateDir)

	roStore, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteReadOnly: %v", err)
	}
	defer roStore.Close()
	ro, err := ingest.NewReadOnly(roStore, &stubParser{}, metaDir)
	if err != nil {
		t.Fatalf("NewReadOnly: %v", err)
	}
	defer ro.Close()

	if files, ok, err := ro.CanWarmStart(ctx, repo); err != nil || !ok || files != 3 {
		t.Fatalf("CanWarmStart = (%d, %v, %v), want (3, true, nil)", files, ok, err)
	}
	if d, err := ro.DriftDetail(ctx, repo, nil); err != nil || d.Total() != 0 {
		t.Fatalf("clean DriftDetail = (%+v, %v), want empty", d, err)
	}

	// added / modified / deleted, one of each.
	writeRaw := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeRaw("new.go", "package a\n")
	writeRaw("b.go", "package a\n// edited\n")
	if err := os.Remove(filepath.Join(repo, "c.go")); err != nil {
		t.Fatalf("remove c.go: %v", err)
	}

	d, err := ro.DriftDetail(ctx, repo, nil)
	if err != nil {
		t.Fatalf("DriftDetail: %v", err)
	}
	want := ingest.Drift{Added: []string{"new.go"}, Modified: []string{"b.go"}, Deleted: []string{"c.go"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("DriftDetail = %+v, want %+v", d, want)
	}

	// DriftSetWithProgress parity: changed = sorted(Added ∪ Modified).
	changed, deleted, err := ro.DriftSetWithProgress(ctx, repo, nil)
	if err != nil {
		t.Fatalf("DriftSetWithProgress: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"b.go", "new.go"}) || !reflect.DeepEqual(deleted, []string{"c.go"}) {
		t.Fatalf("DriftSetWithProgress = (%v, %v), want ([b.go new.go], [c.go])", changed, deleted)
	}

	// Every mutating entry point must refuse.
	if err := ro.IngestAll(ctx, repo); !errors.Is(err, ingest.ErrReadOnly) {
		t.Fatalf("IngestAll = %v, want ErrReadOnly", err)
	}
	if err := ro.IngestChanged(ctx, repo, []string{"b.go"}); !errors.Is(err, ingest.ErrReadOnly) {
		t.Fatalf("IngestChanged = %v, want ErrReadOnly", err)
	}
	if err := ro.RecoverWithRoot(ctx, repo); !errors.Is(err, ingest.ErrReadOnly) {
		t.Fatalf("RecoverWithRoot = %v, want ErrReadOnly", err)
	}
	if err := ro.Recover(ctx); !errors.Is(err, ingest.ErrReadOnly) {
		t.Fatalf("Recover = %v, want ErrReadOnly", err)
	}

	if after := hashDir(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only observation changed the stored content")
	}
	requireEmptyWALs(t, stateDir)
}

// TestNewReadOnly_MissingSidecar pins that a read-only observer never creates
// the sidecar it is meant to observe.
func TestNewReadOnly_MissingSidecar(t *testing.T) {
	metaDir := filepath.Join(t.TempDir(), "meta")
	if _, err := ingest.NewReadOnly(graphstore.NewMemStore(), &stubParser{}, metaDir); err == nil {
		t.Fatal("NewReadOnly over a missing sidecar succeeded, want error")
	}
	if _, err := os.Stat(metaDir); !os.IsNotExist(err) {
		t.Fatalf("NewReadOnly created %s", metaDir)
	}
	if _, err := ingest.NewReadOnly(graphstore.NewMemStore(), &stubParser{}, ""); err == nil {
		t.Fatal("NewReadOnly with empty metaDir succeeded, want error")
	}
}
