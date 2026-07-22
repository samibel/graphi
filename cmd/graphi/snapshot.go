package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/gitinfo"
	"github.com/samibel/graphi/internal/state"
)

// compareRefCurrent is the reserved `graphi compare` ref naming the live
// per-repo store rather than a snapshot.
const compareRefCurrent = "current"

// runSnapshot manages named, frozen graph states. Usage:
//
//	graphi snapshot                 list snapshots for the cwd repo
//	graphi snapshot <name>          freeze the checked-out code under <name>
//	graphi snapshot -rm <name>      delete a snapshot
//
// A snapshot is a full one-shot index of the CURRENT worktree into
// <state>/snapshots/<name>.sqlite — check out the code you want frozen first
// (branch names work: feature/login is stored as feature-login).
// `graphi compare <a> <b>` diffs two snapshots (or a snapshot vs `current`).
func runSnapshot(args []string) int {
	return runSnapshotAt(getwd(), args, os.Stdout)
}

func runSnapshotAt(cwd string, args []string, stdout io.Writer) int {
	var rmName, root string
	var profileFlag *string
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		takeVal := func(name string) (string, bool) {
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			if len(a) > len(name)+1 && a[:len(name)+1] == name+"=" {
				return a[len(name)+1:], true
			}
			return "", false
		}
		if v, ok := takeVal("-rm"); ok {
			rmName = v
		} else if v, ok := takeVal("-root"); ok {
			root = v
		} else if v, ok := takeVal("-profile"); ok {
			profileFlag = &v
		} else if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "graphi: snapshot: at most one <name> argument")
		return 1
	}

	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("snapshot")
			return 1
		}
		root = detected
	}
	p, err := state.Resolve(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", err)
		return 1
	}

	if rmName != "" {
		return removeSnapshot(p, rmName, stdout)
	}
	if len(positional) == 0 {
		return listSnapshots(p, stdout)
	}
	return createSnapshot(p, root, positional[0], profileFlag, stdout)
}

// createSnapshot fully indexes the current worktree into an atomic snapshot:
// build into <name>.sqlite.tmp, stamp the sync metadata, then rename over the
// final path — a failed build never corrupts an existing snapshot.
func createSnapshot(p state.Paths, root, rawName string, profileFlag *string, stdout io.Writer) int {
	info, gitOK := gitinfo.Head(root)
	name, err := state.SnapshotName(rawName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", err)
		return 1
	}
	prof, err := profile.ResolveProfile(profileFlag, os.Getenv(profile.EnvName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	if err := state.Ensure(p); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(p.SnapshotsDir(), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", err)
		return 1
	}

	final := state.SnapshotPath(p, name)
	_, replacing := fileExists(final)
	tmp := final + ".tmp"
	removeDBFiles(tmp)

	fmt.Fprintf(stdout, "graphi snapshot: freezing %s as %q\n", repoHeadline(root, info, gitOK), name)
	if replacing {
		fmt.Fprintf(stdout, "(replacing existing snapshot %q)\n", name)
	}

	code := func() int {
		store, err := graphstore.OpenSQLite(tmp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: snapshot: open store: %v\n", err)
			return 1
		}
		defer func() { _ = store.Close() }()
		// In-memory sidecar: a snapshot is a one-shot full pass that is never
		// warm-started, so no ingest-meta.db lands on disk next to it.
		ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: snapshot: ingest: %v\n", err)
			return 1
		}
		defer func() { _ = ing.Close() }()
		ing.WithProfile(prof)
		if isTerminal(os.Stderr) {
			ing.WithHeartbeatMode(ingest.HeartbeatTTY)
		} else {
			ing.WithHeartbeatMode(ingest.HeartbeatNonTTY)
		}
		prog := newIngestProgress(os.Stderr, isTerminal(os.Stderr))
		ing.WithProgress(prog.Handle)

		ierr := rtime.RebuildRepo(context.Background(), ing, store, root)
		prog.Finish(ierr)
		if ierr != nil {
			fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", ierr)
			return 1
		}
		return 0
	}()
	if code != 0 {
		removeDBFiles(tmp)
		return code
	}

	// The store is closed (WAL folded); publish atomically. Remove a Windows
	// rename-over-existing obstacle first — the .tmp still holds the new state
	// if the swap is interrupted between these two steps.
	removeDBFiles(final)
	if err := os.Rename(tmp, final); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: publish: %v\n", err)
		removeDBFiles(tmp)
		return 1
	}
	fmt.Fprintf(stdout, "graphi snapshot: saved %q (%s)\n", name, final)
	return 0
}

// listSnapshots prints the repo's snapshots with their frozen branch/commit
// context (read from each snapshot's own sync metadata, opened read-only).
func listSnapshots(p state.Paths, stdout io.Writer) int {
	entries, err := os.ReadDir(p.SnapshotsDir())
	if err != nil || len(entries) == 0 {
		fmt.Fprintln(stdout, "no snapshots yet — create one with 'graphi snapshot [name]'")
		return 0
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sqlite") {
			names = append(names, strings.TrimSuffix(e.Name(), ".sqlite"))
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "no snapshots yet — create one with 'graphi snapshot [name]'")
		return 0
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintln(stdout, describeSnapshot(p, name))
	}
	return 0
}

// describeSnapshot renders one listing line: name, frozen branch@commit, node
// count, size, and modification time — every field best-effort.
func describeSnapshot(p state.Paths, name string) string {
	path := state.SnapshotPath(p, name)
	line := name
	if store, err := graphstore.OpenSQLiteReadOnly(path); err == nil {
		ctx := context.Background()
		if _, branch, commit, ok := rtime.LastSync(ctx, store); ok {
			if short := lastSyncShort(branch, commit); short != "" {
				line += "  " + short
			}
		}
		if n, err := store.CountNodes(ctx); err == nil {
			line += fmt.Sprintf("  %d nodes", n)
		}
		_ = store.Close()
	}
	if fi, err := os.Stat(path); err == nil {
		line += fmt.Sprintf("  %s  %s", humanBytes(fi.Size()), fi.ModTime().UTC().Format("2006-01-02 15:04 MST"))
	}
	return line
}

func removeSnapshot(p state.Paths, rawName string, stdout io.Writer) int {
	name, err := state.SnapshotName(rawName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: %v\n", err)
		return 1
	}
	path := state.SnapshotPath(p, name)
	if _, ok := fileExists(path); !ok {
		fmt.Fprintf(os.Stderr, "graphi: snapshot: no snapshot named %q%s\n", name, availableSnapshotsSuffix(p))
		return 1
	}
	removeDBFiles(path)
	fmt.Fprintf(stdout, "graphi snapshot: removed %q\n", name)
	return 0
}

// runCompare diffs two frozen graph states by name. Usage:
//
//	graphi compare <a> <b>
//
// Each ref is a snapshot name (see `graphi snapshot`) or the reserved
// `current` for the live per-repo store. The diff itself is the existing
// compare-branches engine pass, byte-identical to
// `graphi compare-branches -base <a.sqlite> -head <b.sqlite>`.
func runCompare(args []string) int {
	return runCompareAt(getwd(), args)
}

func runCompareAt(cwd string, args []string) int {
	var root string
	var refs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-root" && i+1 < len(args) {
			i++
			root = args[i]
		} else if strings.HasPrefix(a, "-root=") {
			root = a[len("-root="):]
		} else if !strings.HasPrefix(a, "-") {
			refs = append(refs, a)
		}
	}
	if len(refs) != 2 {
		fmt.Fprintln(os.Stderr, "graphi: compare: usage: graphi compare <base> <head> (snapshot names, or 'current')")
		return 1
	}
	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("compare")
			return 1
		}
		root = detected
	}
	p, err := state.Resolve(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: compare: %v\n", err)
		return 1
	}

	paths := make([]string, 2)
	for i, ref := range refs {
		path, rerr := resolveCompareRef(p, ref)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "graphi: compare: %v\n", rerr)
			return 1
		}
		paths[i] = path
	}
	// Context preamble on stderr only — stdout stays the engine's canonical
	// diff bytes.
	fmt.Fprintf(os.Stderr, "comparing %q vs %q\n", refs[0], refs[1])
	return runCompareBranches([]string{"-base", paths[0], "-head", paths[1]})
}

// resolveCompareRef maps a compare ref to an on-disk graph state. A missing
// target is an ERROR (with the available names) — never the empty-store
// fallback compare-branches applies to raw paths, which would render a
// misleading "everything added/removed" diff.
func resolveCompareRef(p state.Paths, ref string) (string, error) {
	if ref == compareRefCurrent {
		if _, ok := fileExists(p.DB); !ok {
			return "", fmt.Errorf("no graph yet for %q — run 'graphi sync' first", compareRefCurrent)
		}
		return p.DB, nil
	}
	name, err := state.SnapshotName(ref)
	if err != nil {
		return "", err
	}
	path := state.SnapshotPath(p, name)
	if _, ok := fileExists(path); !ok {
		return "", fmt.Errorf("no snapshot named %q%s (create it with 'graphi snapshot %s')", name, availableSnapshotsSuffix(p), name)
	}
	return path, nil
}

// availableSnapshotsSuffix renders " — available: a, b" (or "" when none).
func availableSnapshotsSuffix(p state.Paths) string {
	entries, err := os.ReadDir(p.SnapshotsDir())
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sqlite") {
			names = append(names, strings.TrimSuffix(e.Name(), ".sqlite"))
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return " — available: " + strings.Join(names, ", ")
}

// fileExists reports a path's FileInfo when it exists as a regular file.
func fileExists(path string) (os.FileInfo, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	return fi, true
}

// removeDBFiles removes a SQLite database file and its WAL sidecars.
func removeDBFiles(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
