package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/gitinfo"
	"github.com/samibel/graphi/internal/state"
)

// errNotARepo marks an ingest verb run outside any detectable repository.
var errNotARepo = errors.New("not a repository")

// ingestTarget is the resolved (root, store, sidecar) triple an ingest verb
// operates on. autoManaged marks the zero-flag path: the per-repo state layout
// under ~/.graphi (SW-068) rather than caller-supplied paths. detected marks a
// root that came from walking UP from cwd (state.DetectRepo) rather than an
// explicit -root flag — the case where the verb may silently bind a much
// larger tree than the user is standing in.
type ingestTarget struct {
	root, dbPath, metaDir string
	autoManaged           bool
	detected              bool
}

// resolveIngestTarget picks the repository root and store/meta paths for an
// ingest verb. An empty root is detected from cwd (errNotARepo when none).
// When both dbPath and metaDir are empty the auto-managed per-repo state paths
// are derived and ensured — unless legacyStore is set AND root was explicit,
// which preserves `graphi index -root X`'s historical in-memory default.
func resolveIngestTarget(cwd, root, dbPath, metaDir string, legacyStore bool) (ingestTarget, error) {
	explicit := root != ""
	if !explicit {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			return ingestTarget{}, errNotARepo
		}
		root = detected
	}
	t := ingestTarget{root: root, dbPath: dbPath, metaDir: metaDir, detected: !explicit}
	if dbPath != "" || metaDir != "" || (explicit && legacyStore) {
		return t, nil
	}
	p, err := state.Resolve(root)
	if err != nil {
		return ingestTarget{}, err
	}
	if err := state.Ensure(p); err != nil {
		return ingestTarget{}, err
	}
	t.dbPath, t.metaDir, t.autoManaged = p.DB, p.Meta, true
	return t, nil
}

// openIngestSession opens the store + ingester + progress renderer exactly the
// way `graphi index` always has (factored from runIndex so sync/rebuild/index
// stay byte-identical in their wiring).
func openIngestSession(dbPath, metaDir string, prof profile.Profile) (graphstore.Graphstore, *ingest.Ingester, *ingestProgress, func(), error) {
	store, err := openStore(dbPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open store: %w", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		_ = store.Close()
		return nil, nil, nil, nil, fmt.Errorf("ingest: %w", err)
	}
	ing.WithProfile(prof)
	if isTerminal(os.Stderr) {
		ing.WithHeartbeatMode(ingest.HeartbeatTTY)
	} else {
		ing.WithHeartbeatMode(ingest.HeartbeatNonTTY)
	}
	prog := newIngestProgress(os.Stderr, isTerminal(os.Stderr))
	ing.WithProgress(prog.Handle)
	cleanup := func() { _ = ing.Close(); _ = store.Close() }
	return store, ing, prog, cleanup, nil
}

// parseIngestVerbFlags parses the shared advanced flags of sync/rebuild:
// -root/-db/-meta/-profile, each space- or =-separated, any position, unknown
// tokens ignored — the exact conventions of `graphi index`.
func parseIngestVerbFlags(args []string) (root, dbPath, metaDir string, profileFlag *string) {
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
		if v, ok := takeVal("-root"); ok {
			root = v
		} else if v, ok := takeVal("-db"); ok {
			dbPath = v
		} else if v, ok := takeVal("-meta"); ok {
			metaDir = v
		} else if v, ok := takeVal("-profile"); ok {
			profileFlag = &v
		}
	}
	return root, dbPath, metaDir, profileFlag
}

// detectedRootNotice is the one-line stderr notice printed BEFORE an ingest
// verb runs against a root it detected by walking UP from cwd: the nearest
// enclosing .git/go.work/go.mod may sit far above where the user is standing
// (classic footgun: a git-tracked $HOME), and the verb would then silently
// full-index that entire tree. Deliberately NOT TTY-gated — the scripted case
// is exactly where the surprise index bites hardest. Empty string when cwd
// IS the root (nothing surprising) or the root was explicit.
func detectedRootNotice(t ingestTarget, cwd string) string {
	if !t.detected || filepath.Clean(t.root) == filepath.Clean(cwd) {
		return ""
	}
	return fmt.Sprintf("graphi: indexing repository root %s (detected from %s; pass -root to override)", t.root, cwd)
}

// printDetectedRootNotice writes detectedRootNotice to stderr when non-empty.
func printDetectedRootNotice(t ingestTarget, cwd string) {
	if notice := detectedRootNotice(t, cwd); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
}

// indexHintLine is the one-line stderr nudge printed after a successful
// explicit-root `graphi index` run. TTY-gated so CI logs and scripted runs
// stay clean; empty string means "print nothing".
func indexHintLine(explicitRoot, tty bool) string {
	if !explicitRoot || !tty {
		return ""
	}
	return "tip: `graphi sync` (incremental) and `graphi rebuild` (full re-index) do this with no flags"
}

// printNotARepo explains the repo requirement in the zero-config voice.
func printNotARepo(verb string) {
	fmt.Fprintln(os.Stderr, "graphi: this directory doesn't look like a code repository.")
	fmt.Fprintf(os.Stderr, "  graphi %s needs a repo (a directory with a .git, go.work, or go.mod marker).\n", verb)
	fmt.Fprintln(os.Stderr, "  cd into a code repository and try again, or pass -root <repo>.")
}

// repoHeadline renders "<root> (main @ 1a2b3c4)" — or just the root when the
// target is not a git checkout.
func repoHeadline(root string, info gitinfo.Info, gitOK bool) string {
	if !gitOK || info.Short() == "" {
		return root
	}
	return root + " (" + info.Short() + ")"
}

// printBranchSwitch announces a branch change since the last recorded sync
// ("Branch switch detected: main → feature/login"). Silent when either side is
// unknown — detached HEADs, non-git roots, and never-stamped stores degrade to
// no message rather than a wrong one.
func printBranchSwitch(ctx context.Context, w io.Writer, store graphstore.Graphstore, info gitinfo.Info, gitOK bool) {
	if !gitOK || info.Branch == "" {
		return
	}
	_, prevBranch, _, ok := rtime.LastSync(ctx, store)
	if ok && prevBranch != "" && prevBranch != info.Branch {
		fmt.Fprintf(w, "Branch switch detected: %s → %s\n", prevBranch, info.Branch)
	}
}

// lastSyncShort renders a stored (branch, commit) sync stamp the way
// gitinfo.Short renders a live HEAD. The stamp does not persist detachedness,
// so a commit without a branch is rendered as detached.
func lastSyncShort(branch, commit string) string {
	return gitinfo.Info{Branch: branch, Commit: commit, Detached: branch == "" && commit != ""}.Short()
}

// syncSummary is the durable stdout line describing what `graphi sync` did.
func syncSummary(s rtime.SyncStats) string {
	switch {
	case s.Full:
		return "graphi sync: full re-index complete"
	case s.Added+s.Changed+s.Removed == 0:
		return fmt.Sprintf("graphi sync: up to date (checked %d %s)", s.Checked, filesWord(s.Checked))
	default:
		return fmt.Sprintf("graphi sync: %d added, %d changed, %d removed", s.Added, s.Changed, s.Removed)
	}
}

// runSync is the flagless "bring the graph up to date" verb. Usage:
//
//	graphi sync [-root <repo>] [-db path] [-meta dir] [-profile name]
//
// It detects the repo from cwd, opens the auto-managed per-repo store, and
// runs the same warm-or-full pass bare `graphi` and the MCP session use —
// without starting any server. Incremental by design: `graphi rebuild` is the
// explicit full re-index.
func runSync(args []string) int {
	return runSyncAt(getwd(), args, os.Stdout)
}

func runSyncAt(cwd string, args []string, stdout io.Writer) int {
	root, dbPath, metaDir, profileFlag := parseIngestVerbFlags(args)
	target, err := resolveIngestTarget(cwd, root, dbPath, metaDir, false)
	if errors.Is(err, errNotARepo) {
		printNotARepo("sync")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: sync: %v\n", err)
		return 1
	}
	printDetectedRootNotice(target, cwd)
	prof, err := profile.ResolveProfile(profileFlag, os.Getenv(profile.EnvName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	store, ing, prog, cleanup, err := openIngestSession(target.dbPath, target.metaDir, prof)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: sync: %v\n", err)
		return 1
	}
	defer cleanup()

	ctx := context.Background()
	info, gitOK := gitinfo.Head(target.root)
	fmt.Fprintf(stdout, "graphi sync: %s\n", repoHeadline(target.root, info, gitOK))
	printBranchSwitch(ctx, stdout, store, info, gitOK)

	stats, serr := rtime.SyncRepo(ctx, ing, store, target.root, prog.Handle)
	prog.Finish(serr)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "graphi: sync: %v\n", serr)
		return 1
	}
	fmt.Fprintln(stdout, syncSummary(stats))
	return 0
}

// runRebuild is the explicit full re-index verb. Usage:
//
//	graphi rebuild [-root <repo>] [-db path] [-meta dir] [-profile name]
//
// Unlike `graphi sync` it never warm-starts: the whole repo is re-parsed and
// the store re-certified — the recovery path for a damaged index, after an
// upgrade, or when `graphi doctor` recommends it.
func runRebuild(args []string) int {
	return runRebuildAt(getwd(), args, os.Stdout)
}

func runRebuildAt(cwd string, args []string, stdout io.Writer) int {
	root, dbPath, metaDir, profileFlag := parseIngestVerbFlags(args)
	target, err := resolveIngestTarget(cwd, root, dbPath, metaDir, false)
	if errors.Is(err, errNotARepo) {
		printNotARepo("rebuild")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: rebuild: %v\n", err)
		return 1
	}
	printDetectedRootNotice(target, cwd)
	prof, err := profile.ResolveProfile(profileFlag, os.Getenv(profile.EnvName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	store, ing, prog, cleanup, err := openIngestSession(target.dbPath, target.metaDir, prof)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: rebuild: %v\n", err)
		return 1
	}
	defer cleanup()

	ctx := context.Background()
	info, gitOK := gitinfo.Head(target.root)
	fmt.Fprintf(stdout, "graphi rebuild: re-indexing %s\n", repoHeadline(target.root, info, gitOK))

	rerr := rtime.RebuildRepo(ctx, ing, store, target.root)
	prog.Finish(rerr)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "graphi: rebuild: %v\n", rerr)
		return 1
	}
	fmt.Fprintln(stdout, "graphi rebuild: done")
	return 0
}
