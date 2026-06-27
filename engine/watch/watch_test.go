package watch

import (
	"os"
	"path/filepath"
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
