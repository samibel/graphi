package watch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		op   fsnotify.Op
		want EventKind
	}{
		{fsnotify.Create, Created},
		{fsnotify.Write, Changed},
		{fsnotify.Remove, Deleted},
		{fsnotify.Rename, Deleted},
		{fsnotify.Create | fsnotify.Write, Created},
		{fsnotify.Write | fsnotify.Remove, Deleted}, // delete dominates
	}
	for _, c := range cases {
		if got := classify(c.op); got != c.want {
			t.Errorf("classify(%v) = %v, want %v", c.op, got, c.want)
		}
	}
}

// TestWatcher_EmitsCreateModifyDelete is part of AC-1: the raw fsnotify watcher
// emits classified events for created/modified/deleted tracked files.
func TestWatcher_EmitsCreateModifyDelete(t *testing.T) {
	root := t.TempDir()

	var (
		mu     sync.Mutex
		events []Event
	)
	w, err := newWatcher(root, func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Close()

	file := filepath.Join(root, "a.go")

	waitFor := func(kind EventKind, rel string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			mu.Lock()
			for _, e := range events {
				if e.Kind == kind && e.RelPath == rel {
					mu.Unlock()
					return
				}
			}
			mu.Unlock()
			select {
			case <-deadline:
				mu.Lock()
				got := append([]Event(nil), events...)
				mu.Unlock()
				t.Fatalf("timed out waiting for %v %q; saw %v", kind, rel, got)
			case <-time.After(15 * time.Millisecond):
			}
		}
	}

	// Create
	if err := os.WriteFile(file, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(Created, "a.go")

	// Modify
	if err := os.WriteFile(file, []byte("package a\n//edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(Changed, "a.go")

	// Delete
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	waitFor(Deleted, "a.go")
}

// TestWatcher_PrunesIgnoredDirs asserts the watcher registers no fsnotify watch
// under the always-pruned directory names (node_modules, vendor, ...) — neither
// for directories present at start nor for ones created while watching. On
// macOS every watched path holds an open kqueue file descriptor, so watching a
// dependency tree the ingest never reads exhausts FDs on large repos.
func TestWatcher_PrunesIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{
		filepath.Join("node_modules", "a", "b"),
		filepath.Join("vendor", "x"),
		"src",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	w, err := newWatcher(root, nil)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Close()

	assertWatchList := func(wantSub, forbidden []string) {
		t.Helper()
		list := w.fsw.WatchList()
		got := make(map[string]bool, len(list))
		for _, p := range list {
			got[p] = true
		}
		for _, sub := range wantSub {
			if !got[filepath.Join(root, sub)] {
				t.Errorf("expected %q to be watched; watch list: %v", sub, list)
			}
		}
		for _, sub := range forbidden {
			full := filepath.Join(root, sub)
			for _, p := range list {
				if p == full || strings.HasPrefix(p, full+string(filepath.Separator)) {
					t.Errorf("ignored dir %q (or child %q) is watched", sub, p)
				}
			}
		}
	}

	assertWatchList([]string{"src"}, []string{"node_modules", "vendor"})
	if !slices.Contains(w.fsw.WatchList(), root) {
		t.Errorf("root itself must be watched; watch list: %v", w.fsw.WatchList())
	}

	// Directories created while watching: a non-ignored name is added, an
	// ignored one is not.
	if err := os.Mkdir(filepath.Join(root, "lib"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for !slices.Contains(w.fsw.WatchList(), filepath.Join(root, "lib")) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for lib/ to be watched; watch list: %v", w.fsw.WatchList())
		case <-time.After(15 * time.Millisecond):
		}
	}
	// lib/ being watched proves the created-dir events were processed; the
	// ignored sibling created before it must still be absent.
	assertWatchList([]string{"lib"}, []string{"__pycache__"})
}

// TestWatcher_AddsNewSubdirOnCreate covers R-4: a newly created subdirectory is
// dynamically watched so files inside it surface as events.
func TestWatcher_AddsNewSubdirOnCreate(t *testing.T) {
	root := t.TempDir()
	var (
		mu   sync.Mutex
		seen bool
	)
	w, err := newWatcher(root, func(e Event) {
		if e.RelPath == "sub/inner.go" {
			mu.Lock()
			seen = true
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Close()

	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	// Give the watcher a moment to register the new dir, then create a file in it.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(sub, "inner.go"), []byte("package sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		ok := seen
		mu.Unlock()
		if ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("did not observe event for file in newly created subdir")
		case <-time.After(15 * time.Millisecond):
		}
	}
}
