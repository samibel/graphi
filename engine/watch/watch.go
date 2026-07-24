package watch

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/samibel/graphi/engine/ingest"
)

// EventKind classifies a raw filesystem event into the three logical operations
// the incremental apply cares about.
type EventKind int

const (
	// Created — a tracked path appeared.
	Created EventKind = iota
	// Changed — a tracked path's contents were written.
	Changed
	// Deleted — a tracked path was removed or renamed away.
	Deleted
)

func (k EventKind) String() string {
	switch k {
	case Created:
		return "created"
	case Changed:
		return "changed"
	case Deleted:
		return "deleted"
	}
	return "unknown"
}

// Event is a classified filesystem event with a repo-relative POSIX path.
type Event struct {
	Kind    EventKind
	RelPath string
}

// classify maps a raw fsnotify op-set to an EventKind. fsnotify coalesces
// multiple ops into one Op bitset; we resolve to the most consequential logical
// kind (delete/rename dominate; create over write; otherwise change).
func classify(op fsnotify.Op) EventKind {
	switch {
	case op.Has(fsnotify.Remove) || op.Has(fsnotify.Rename):
		return Deleted
	case op.Has(fsnotify.Create):
		return Created
	default:
		return Changed
	}
}

// watcher is the pure-Go fsnotify watcher. fsnotify is non-recursive, so it
// registers a watch on the root and every subdirectory (walk + add per dir) and
// dynamically adds a watch when a new directory is created. Raw events are
// classified and forwarded to onEvent with a repo-relative path; directory
// events drive dynamic re-registration but are not themselves forwarded as file
// changes (the new dir is walked so its existing children surface as events via
// the reconcile/initial path).
type watcher struct {
	root    string
	fsw     *fsnotify.Watcher
	onEvent func(Event)

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// newWatcher constructs and starts a watcher rooted at root (which must be an
// absolute, cleaned path). onEvent is invoked for every classified file event.
func newWatcher(root string, onEvent func(Event)) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: new fsnotify watcher: %w", err)
	}
	w := &watcher{
		root:    root,
		fsw:     fsw,
		onEvent: onEvent,
		done:    make(chan struct{}),
	}
	if err := w.addTree(root); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// addTree registers a watch on dir and every subdirectory under it. Missing
// directories (a racy rmdir during walk) are tolerated.
func (w *watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not fatal — reconcile covers it.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// Prune the same directory set the ingest walk never descends into
		// (node_modules, .git, vendor, ...). The dir passed to addTree itself is
		// exempt — mirroring walk()'s rel=="." exemption — so an explicitly
		// rooted tree is still watched. Ignored-file events can then no longer
		// arrive at all; ParseFile's pathHasIgnoredDir stays as defense in depth.
		if path != dir && ingest.IsIgnoredDirName(d.Name()) {
			return filepath.SkipDir
		}
		if addErr := w.fsw.Add(path); addErr != nil && !os.IsNotExist(addErr) {
			return fmt.Errorf("watch: add %s: %w", path, addErr)
		}
		return nil
	})
}

// rel converts an absolute event path to a repo-relative POSIX path, returning
// ok=false when the path escapes the root (defense in depth alongside the
// ingest-layer sanitizePath).
func (w *watcher) rel(abs string) (string, bool) {
	r, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", false
	}
	r = filepath.ToSlash(r)
	if r == "." || strings.HasPrefix(r, "..") {
		return "", false
	}
	return r, true
}

func (w *watcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// fsnotify-level errors (e.g. queue overflow) are non-fatal: the
			// periodic reconcile is the authoritative drift-repair backstop.
		}
	}
}

func (w *watcher) handle(ev fsnotify.Event) {
	// A newly created directory must be watched (fsnotify is non-recursive) —
	// unless it is an always-pruned name (a fresh node_modules/.git/vendor/...),
	// which the ingest walk would never read and so must not be watched either.
	if ev.Op.Has(fsnotify.Create) {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			if !ingest.IsIgnoredDirName(filepath.Base(ev.Name)) {
				_ = w.addTree(ev.Name)
			}
			return // directory creation itself is not a file change
		}
	}
	rel, ok := w.rel(ev.Name)
	if !ok {
		return
	}
	// Skip directory write/remove events that are not file changes. For Remove we
	// cannot stat (it's gone); the consumer re-stats and resolves final state, so
	// forwarding a Deleted for a former-dir path is harmless (no cache entry).
	if w.onEvent != nil {
		w.onEvent(Event{Kind: classify(ev.Op), RelPath: rel})
	}
}

// Close stops the watcher and releases the fsnotify resources. It is idempotent.
func (w *watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	err := w.fsw.Close()
	<-w.done
	return err
}
