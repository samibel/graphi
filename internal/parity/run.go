package parity

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// Runner executes the real-repository parity matrix against a BUILT graphi
// binary.
//
// Every graph in this harness is produced by that binary running as a
// SUBPROCESS — `graphi rebuild` for the full pass, `graphi sync` for the
// incremental pass. Nothing here calls ingest in-process, so the harness cannot
// perturb the instrument even in principle. That is the property the design
// exists for, and it is asserted mechanically by the denylist test in this
// package.
type Runner struct {
	// Binary is the graphi executable under test.
	Binary string
	// WorkDir holds clones, per-class stores and snapshot envelopes.
	WorkDir string
	// MaxTier caps repository tier. The default is 3 (cobra tier-1 plus
	// uuid/lo/gin/grpc-go tier-3). TIER 4 IS EXCLUDED BY CONSTRUCTION, not by
	// configuration: kubernetes is ~3 minutes to index at ~9 GB peak RSS,
	// belongs to SW-145 with a named machine, and clampTier below refuses to
	// raise the cap past 3 whatever a caller passes.
	MaxTier int
	// Classes, when non-empty, restricts the run to these class ids so a single
	// row can be iterated without a full run. A filtered run is never complete
	// and therefore never publishable.
	Classes []string
	// PerClassTimeout bounds one class row end to end.
	PerClassTimeout time.Duration
	// LifecycleRepeat is how many times each lifecycle journey runs per kill
	// point (0 = DefaultLifecycleRepeat). It exists because ONE PASSING RUN OF A
	// LIFECYCLE ROW IS NOT EVIDENCE: this matrix has already observed a product
	// path whose output varies between otherwise-identical executions, so a row
	// publishes its sample and not a single execution. It cannot be used to
	// retry a row into green — scoreRepetitions FAILS the row if any repetition
	// diverged.
	LifecycleRepeat int
	// RunnerClass names the machine class. Empty makes the run unpublishable.
	RunnerClass string
	// AllowLocal admits manifest entries that point at a LOCAL PATH instead of
	// a pinned clone.
	//
	// It is FALSE for the real-repo matrix, deliberately. The corpus manifest
	// carries tier-1 local fixtures, and a matrix row that runs on a fixture is
	// not evidence — admitting them would let a green run be reported as the
	// §12.3 gate while nothing real was exercised. It is set true ONLY by this
	// package's hermetic tests, which is the same split internal/corpus uses:
	// the harness's own tests stay network-free by using Path entries, and the
	// real run refuses them.
	AllowLocal bool
	// Log receives progress lines (nil = silent).
	Log func(format string, args ...any)

	models map[string]*RepoModel
	// jvmModels is the WP-J7 (SW-176) JVM half's cache. It is a SECOND map
	// rather than a widened first one because the two models share no type:
	// see the header of jvmsource.go for why a "generalised" model would be an
	// almost-empty intersection.
	jvmModels map[string]*JVMModel
	dirs      map[string]string
	pristine  map[string]string
}

// MaxSupportedTier is the hard ceiling. Tier 4 (the kubernetes stress target) is
// SW-145's subject and requires a named machine; PRD §17's 4 GB stop rule is in
// live tension with its ~9 GB peak RSS, and that finding belongs to SW-145's
// decision record. Excluding it BY CONSTRUCTION means no flag, environment
// variable or workflow edit can pull it into this matrix by accident.
const MaxSupportedTier = 3

func clampTier(t int) int {
	if t <= 0 {
		return MaxSupportedTier
	}
	if t > MaxSupportedTier {
		return MaxSupportedTier
	}
	return t
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// Run executes the matrix and returns the report. It returns an error only for
// harness-level faults — a FAILING ROW IS THE REPORT'S JOB, not an error, and
// every mismatch is blocking (Delta PRD :1092): the run's outcome is FAIL and no
// row is skipped, retried or downgraded to a warning.
func (r *Runner) Run(ctx context.Context, m corpus.Manifest, rows []ClassRow, prov parityreport.Provenance) (parityreport.Report, error) {
	if r.PerClassTimeout <= 0 {
		r.PerClassTimeout = 15 * time.Minute
	}
	r.MaxTier = clampTier(r.MaxTier)
	r.models = map[string]*RepoModel{}
	r.dirs = map[string]string{}
	r.pristine = map[string]string{}
	if err := os.MkdirAll(r.WorkDir, 0o755); err != nil {
		return parityreport.Report{}, fmt.Errorf("parity: workdir: %w", err)
	}

	prov.RunnerClass = r.RunnerClass
	prov.GoVersion = goVersion()
	prov.OSArch = osArch()
	prov.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	rep := parityreport.Report{
		Provenance:   prov,
		MaxTier:      r.MaxTier,
		ClassFilter:  r.Classes,
		MatrixSource: ClassesPath,
		NotCompared:  parityreport.DefaultNotCompared(),
	}

	candidates := goRepos(m, r.MaxTier, r.AllowLocal)
	strat := stratification(m)
	byID := SpecByID()

	for _, row := range rows {
		res := parityreport.ClassResult{
			ID: row.ID, Kind: row.Kind, Label: row.Label,
			KnownDefect: row.KnownDefect,
		}
		// LIFECYCLE ROWS FIRST (SW-158). branch_switch and the two crash
		// conditions are declared harness_row: "deferred" in the matrix YAML,
		// and that field is about SW-157's HERMETIC engine/conformance table —
		// where they remain absent, because a t.TempDir() fixture has neither a
		// git history nor a process to kill. This harness delivers them, so the
		// declared id is dispatched to lifecycle.go rather than read as a
		// deferral. The YAML is unchanged in shape: re-declaring these rows
		// harness_row: "required" would demand owner: "SW-157" under the OWNER
		// direction and a row in a table that legitimately has none.
		if lrow, ok := lifecycleRowByID(row.ID); ok {
			if len(r.Classes) > 0 && !contains(r.Classes, row.ID) {
				res.Verdict = parityreport.VerdictSkipped
				res.Detail = "excluded by the -classes filter; a filtered run is never publishable"
				rep.Classes = append(rep.Classes, res)
				continue
			}
			r.runLifecycleRow(ctx, &res, lrow, candidates, &rep)
			rep.Classes = append(rep.Classes, res)
			continue
		}

		switch {
		case row.HarnessRow == harnessDeferred:
			// A DECLARED deferral, read out of the matrix YAML — never a
			// runtime escape.
			res.Verdict = parityreport.VerdictDeferred
			res.DeferredTo = row.DeferredTo
			res.Detail = "declared harness_row: deferred in " + ClassesPath +
				"; owned by " + row.DeferredTo + "."
			rep.Classes = append(rep.Classes, res)
			r.logf("parity: %-24s DEFERRED -> %s", row.ID, row.DeferredTo)
			continue
		case row.Kind != kindChangeClass:
			res.Verdict = parityreport.VerdictDeferred
			res.DeferredTo = row.DeferredTo
			res.Detail = "crash condition, not a change class; owned by " + row.DeferredTo
			rep.Classes = append(rep.Classes, res)
			continue
		case len(r.Classes) > 0 && !contains(r.Classes, row.ID):
			res.Verdict = parityreport.VerdictSkipped
			res.Detail = "excluded by the -classes filter; a filtered run is never publishable"
			rep.Classes = append(rep.Classes, res)
			continue
		}

		spec, ok := byID[row.ID]
		if !ok {
			res.Verdict = parityreport.VerdictError
			res.Detail = ClassesPath + " declares change class " + row.ID +
				" with harness_row: required, but the real-repo class table has no planner for it"
			rep.Classes = append(rep.Classes, res)
			continue
		}
		if spec.Note != "" {
			res.Detail = spec.Note
		}
		r.runClass(ctx, &res, spec, candidates, strat, &rep)
		rep.Classes = append(rep.Classes, res)
	}

	// Record every repository that was actually materialized.
	for _, name := range sortedStrings(r.dirs) {
		for _, e := range candidates {
			if e.Name != name {
				continue
			}
			head, _ := gitHead(ctx, r.dirs[name])
			gf := 0
			if e.Measured != nil {
				gf = e.Measured.GoFiles
			}
			rep.Repos = append(rep.Repos, parityreport.RepoRef{
				Name: e.Name, URL: e.URL, Ref: e.Ref,
				PinnedSHA: e.SHA, HeadSHA: head, Tier: e.Tier, GoFiles: gf,
			})
		}
	}

	rep.Finalize(CountChangeClasses(rows), CountCrashConditions(rows))
	return rep, nil
}

// runClass executes one change-class row end to end.
func (r *Runner) runClass(ctx context.Context, res *parityreport.ClassResult, spec ClassSpec,
	candidates []corpus.Entry, strat map[string]string, rep *parityreport.Report) {

	start := time.Now()
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	ctx, cancel := context.WithTimeout(ctx, r.PerClassTimeout)
	defer cancel()

	entry, model, mut, why, err := r.selectRepo(ctx, spec, candidates, strat)
	if err != nil {
		res.Verdict = parityreport.VerdictError
		res.Detail = err.Error()
		r.logf("parity: %-24s ERROR   %v", spec.ID, err)
		return
	}
	if mut == nil {
		res.Verdict = parityreport.VerdictSkipped
		res.Detail = why
		r.logf("parity: %-24s SKIPPED %s", spec.ID, why)
		return
	}
	res.Repo = entry.Name
	res.SelectedBecause = why
	res.Mutation = mut.Desc
	repoDir := r.dirs[entry.Name]
	res.RepoHeadSHA, _ = gitHead(ctx, repoDir)
	_ = model

	stateDir := filepath.Join(r.WorkDir, "state", entry.Name, spec.ID)
	if err := os.RemoveAll(stateDir); err != nil {
		res.Verdict, res.Detail = parityreport.VerdictError, err.Error()
		return
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		res.Verdict, res.Detail = parityreport.VerdictError, err.Error()
		return
	}
	incDB := filepath.Join(stateDir, "inc.db")
	fullDB := filepath.Join(stateDir, "full.db")
	incSnap := filepath.Join(stateDir, "inc.snapshot")
	fullSnap := filepath.Join(stateDir, "full.snapshot")

	fail := func(detail string) {
		res.Verdict = parityreport.VerdictError
		res.Detail = detail
		r.logf("parity: %-24s ERROR   %s", spec.ID, firstLine(detail))
	}

	// The tree must start pristine: the previous row's edits are reverted before
	// this one's baseline is indexed, or the baseline would not be the pin.
	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		fail("restore clone to the pinned tree: " + err.Error())
		return
	}

	// 1. Baseline full index at the pinned tree — the state the incremental
	//    pass will update FROM.
	if out, err := r.graphi(ctx, repoDir, "rebuild", "-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("baseline rebuild: " + err.Error() + "\n" + tail(out))
		return
	}

	// 2. Apply the change class as a REAL edit to REAL source.
	if err := applyMutation(repoDir, mut); err != nil {
		fail("apply mutation: " + err.Error())
		return
	}

	// 3. Incremental update over the edited tree.
	if out, err := r.graphi(ctx, repoDir, "sync", "-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("incremental sync: " + err.Error() + "\n" + tail(out))
		return
	}

	// 4. Fresh full index of the SAME final tree, into a store that has never
	//    seen the pre-edit state.
	if out, err := r.graphi(ctx, repoDir, "rebuild", "-root", repoDir, "-db", fullDB, "-meta", stateDir+"/fullmeta"); err != nil {
		fail("final rebuild: " + err.Error() + "\n" + tail(out))
		return
	}

	// 5. THE ASSERTION: byte equality of the two portable snapshot envelopes.
	if err := emitSnapshot(incDB, incSnap); err != nil {
		fail(err.Error())
		return
	}
	if err := emitSnapshot(fullDB, fullSnap); err != nil {
		fail(err.Error())
		return
	}
	incGraph, incBytes, err := readGraph(incSnap)
	if err != nil {
		fail(err.Error())
		return
	}
	fullGraph, fullBytes, err := readGraph(fullSnap)
	if err != nil {
		fail(err.Error())
		return
	}
	res.SnapshotIncSHA256 = digest(incBytes)
	res.SnapshotFullSHA256 = digest(fullBytes)
	res.IncNodes, res.IncEdges = len(incGraph.Nodes), len(incGraph.Edges)
	res.FullNodes, res.FullEdges = len(fullGraph.Nodes), len(fullGraph.Edges)

	// 6. The two PRD §12.3 store-level counts, over the REAL repository graph
	//    after this class's change sequence — on BOTH sides.
	//
	//    Counting only the rebuild would leave the incremental graph, which is
	//    the side a defect actually lands on, unmeasured while the report said
	//    "orphaned external nodes = 0" without qualification.
	rep.StoreCounts = append(rep.StoreCounts,
		storeCounts(entry.Name, spec.ID, "full", fullGraph),
		storeCounts(entry.Name, spec.ID, "incremental", incGraph))

	if bytes.Equal(incBytes, fullBytes) {
		res.Verdict = parityreport.VerdictPass
		r.logf("parity: %-24s PASS    %s (%d nodes / %d edges)", spec.ID, entry.Name, res.FullNodes, res.FullEdges)
	} else {
		res.Verdict = parityreport.VerdictFail
		res.Detail = fmt.Sprintf("SNAPSHOT BYTES DIFFER. incremental %d nodes / %d edges (%s); full %d nodes / %d edges (%s). %s",
			res.IncNodes, res.IncEdges, short(res.SnapshotIncSHA256),
			res.FullNodes, res.FullEdges, short(res.SnapshotFullSHA256), res.Detail)
		// 7. DIAGNOSIS ONLY. graphi compare / BranchDiffReport explains a
		//    mismatch and never decides one: it is a Labs surface, and a §12.3
		//    gate must not depend on a Labs analyzer's BranchDiffSchemaVersion.
		//    The row's verdict above was already set from the bytes.
		diag, derr := r.graphi(ctx, repoDir, "compare-branches", "-base", fullDB, "-head", incDB)
		if derr != nil {
			res.Diagnostic = "graphi compare-branches failed: " + derr.Error()
		} else {
			res.Diagnostic = tail(diag)
			// A diff showing no deltas while the snapshot bytes differ is
			// ITSELF a finding, and is recorded as one rather than resolved in
			// either direction.
			if strings.Contains(res.Diagnostic, `"outcome":"empty"`) {
				res.DiagnosticContradiction = true
				res.Detail += " FINDING: the BranchDiffReport reports NO deltas while the " +
					"snapshot bytes differ. The bytes decide this row; the contradiction is " +
					"recorded because it means the Labs diff does not see a difference the " +
					"canonical envelope does."
			}
		}
		r.logf("parity: %-24s FAIL    %s (inc %d/%d vs full %d/%d)", spec.ID, entry.Name,
			res.IncNodes, res.IncEdges, res.FullNodes, res.FullEdges)
	}

	// Leave the clone pinned again for the next row.
	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		r.logf("parity: %s: WARNING restoring clone: %v", spec.ID, err)
	}
}

// selectRepo resolves the repository a class runs on.
//
// AC-6, mechanically. Two selection modes, and the difference matters:
//
//	MANIFEST-PINNED — a class declaring a ManifestProperty takes the repository
//	  the corpus manifest's stratification block attributes that property to.
//	  The manifest is the authority on which repository carries build tags and
//	  which carries generated code; a silent fallback would turn the row into a
//	  claim about a property the substituted repository was never selected for.
//	  If the pinned repository is out of tier, the row SKIPS. It never roams.
//	SMALLEST EXHIBITING — every other class walks the Go repositories in
//	  (tier, measured go-file count) order and takes the first whose REAL SOURCE
//	  the planner can find a target in. Tier leads the ordering so cobra (the
//	  only tier-1 real repository) is preferred, which is also what keeps the
//	  local -max-tier 1 run meaningful.
func (r *Runner) selectRepo(ctx context.Context, spec ClassSpec, candidates []corpus.Entry,
	strat map[string]string) (corpus.Entry, *RepoModel, *Mutation, string, error) {

	pool := candidates
	why := ""
	if spec.ManifestProperty != "" {
		name, ok := strat[spec.ManifestProperty]
		if !ok {
			return corpus.Entry{}, nil, nil, "", fmt.Errorf(
				"parity: class %q requires the manifest stratification property %q, which corpus/manifest.json does not declare",
				spec.ID, spec.ManifestProperty)
		}
		pool = nil
		for _, e := range candidates {
			if e.Name == name {
				pool = append(pool, e)
			}
		}
		if len(pool) == 0 {
			return corpus.Entry{}, nil, nil, fmt.Sprintf(
				"the manifest attributes %q to %q, which is above the tier cap (-max-tier %d) — the class is NOT re-pointed at another repository",
				spec.ManifestProperty, name, r.MaxTier), nil
		}
		why = fmt.Sprintf("manifest stratification attributes %q to %q", spec.ManifestProperty, name)
	}

	var tried []string
	for _, e := range pool {
		dir, model, err := r.materialize(ctx, e)
		if err != nil {
			// A repository that will not materialize is fatal ONLY when it is
			// the manifest-pinned one: for a roaming class it just means this
			// candidate cannot host the row, and the walk continues. A pin
			// failure (a moved tag) is fatal in both cases and is raised by
			// materialize itself.
			if spec.ManifestProperty != "" || isPinFailure(err) {
				return corpus.Entry{}, nil, nil, "", err
			}
			tried = append(tried, e.Name+" (unusable: "+firstLine(err.Error())+")")
			continue
		}
		_ = dir
		mut, perr := spec.Plan(model)
		if perr == errNoTarget {
			tried = append(tried, e.Name)
			continue
		}
		if perr != nil {
			return corpus.Entry{}, nil, nil, "", fmt.Errorf("parity: class %q planner on %s: %w", spec.ID, e.Name, perr)
		}
		if why == "" {
			why = fmt.Sprintf("smallest in-cap repository whose real source exhibits this class (tier %d, %d Go files)",
				e.Tier, goFileCount(e))
			if len(tried) > 0 {
				why += "; not exhibited by " + strings.Join(tried, ", ")
			}
		}
		return e, model, mut, why, nil
	}
	detail := fmt.Sprintf("no repository within -max-tier %d exhibits the structure this class needs", r.MaxTier)
	if len(tried) > 0 {
		detail += " (examined: " + strings.Join(tried, ", ") + ")"
	}
	return corpus.Entry{}, nil, nil, detail, nil
}

// materialize clones (or reuses) a repository and parses its pristine model.
//
// EVERY entry is materialized INTO THE WORKDIR, including local Path entries.
// That is not a convenience: the class planners write real edits to real files,
// and a Path entry points at a checked-in fixture inside this repository. Editing
// it in place would mutate the source tree the run is measuring from.
func (r *Runner) materialize(ctx context.Context, e corpus.Entry) (string, *RepoModel, error) {
	if m, ok := r.models[e.Name]; ok {
		return r.dirs[e.Name], m, nil
	}
	dir := filepath.Join(r.WorkDir, "repos", e.Name)
	if e.URL != "" {
		r.logf("parity: cloning %s @ %s", e.Name, e.Ref)
		if err := clone(ctx, e, dir); err != nil {
			return "", nil, err
		}
	} else {
		src := e.Path
		if !filepath.IsAbs(src) {
			abs, aerr := filepath.Abs(src)
			if aerr != nil {
				return "", nil, aerr
			}
			src = abs
		}
		if err := copyTree(src, dir); err != nil {
			return "", nil, err
		}
		r.pristine[e.Name] = src
	}
	head, herr := gitHead(ctx, dir)
	if e.SHA != "" {
		// FAIL CLOSED on a pin mismatch. A re-pointed upstream tag silently
		// changes what the matrix measured; it must stop the run, not be
		// absorbed into it.
		if herr != nil {
			return "", nil, fmt.Errorf("parity: %s%s: sha pinned but HEAD unreadable: %v", pinFailureMarker, e.Name, herr)
		}
		if !shaMatches(e.SHA, head) {
			return "", nil, fmt.Errorf("parity: %s%s: HEAD %s does not match pinned sha %s (ref %q moved?)",
				pinFailureMarker, e.Name, head, e.SHA, e.Ref)
		}
	}
	model, err := discover(dir)
	if err != nil {
		return "", nil, err
	}
	r.models[e.Name] = model
	r.dirs[e.Name] = dir
	return dir, model, nil
}

// applyMutation writes the class's real edits to the real clone.
func applyMutation(root string, mut *Mutation) error {
	for _, op := range mut.Ops {
		abs := filepath.Join(root, filepath.FromSlash(op.Path))
		switch op.Kind {
		case opWrite:
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(abs, op.Data, 0o644); err != nil {
				return err
			}
		case opDelete:
			if err := os.Remove(abs); err != nil {
				return err
			}
		case opRenameDir:
			dst := filepath.Join(root, filepath.FromSlash(op.NewPath))
			if err := os.Rename(abs, dst); err != nil {
				return err
			}
		default:
			return fmt.Errorf("parity: unknown file op %q", op.Kind)
		}
	}
	return nil
}

// graphi runs the binary under test. cwd is set to the repository so the verbs
// behave exactly as a user's would; the store is nevertheless pinned explicitly
// per row so per-repo session auto-discovery can never point two rows at one
// store.
func (r *Runner) graphi(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	// THE MEASURED PROFILE IS PINNED, NOT INHERITED (ADR 0010 review round 1,
	// finding 7). The harness deliberately passes no -profile so it measures
	// the DEFAULT the product resolves — but an inherited
	// GRAPHI_INDEX_PROFILE=fast in the operator's environment would silently
	// make every row converge on a configuration nobody ships, and the report
	// would still publish "19 of 19 PASS". That is the exact failure mode ADR
	// 0010 exists to close, so the variable is cleared for the child and the
	// resolved profile is recorded in the report's provenance.
	return r.graphiEnv(ctx, cwd, []string{profile.EnvName + "="}, args...)
}

// graphiEnv is graphi with an explicit environment overlay, so the JVM half can
// pin its own axis variables through the same single door.
func (r *Runner) graphiEnv(ctx context.Context, cwd string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if p := panicMarker(out); p != "" {
		return out, fmt.Errorf("graphi %s: panic in output: %s", strings.Join(args, " "), p)
	}
	if err != nil {
		return out, fmt.Errorf("graphi %s: %v", strings.Join(args, " "), err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// git + manifest helpers. clone/shaMatches/gitHead follow
// internal/corpus/run.go:281,300,307 — the proven primitives for exactly this
// job — rather than inventing a second dialect of the same operations.
// ---------------------------------------------------------------------------

func clone(ctx context.Context, e corpus.Entry, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("parity: clean clone dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("parity: clone parent dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", e.Ref, "--single-branch", e.URL, dir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("parity: git clone %s @ %s: %v\n%s", e.URL, e.Ref, err, tail(out))
	}
	return nil
}

// gitRestore returns a clone to its pinned tree, discarding the previous row's
// edits including untracked files and renamed directories.
func gitRestore(ctx context.Context, dir string) error {
	for _, args := range [][]string{
		{"-C", dir, "reset", "--hard", "-q", "HEAD"},
		{"-C", dir, "clean", "-qfdx"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, tail(out))
		}
	}
	return nil
}

// restore returns a materialized repository to its pristine state between rows:
// git for a clone, a re-copy for a local fixture. Each row's baseline MUST be
// the pin, or the "incremental" side would be updating from the previous row's
// edits.
func (r *Runner) restore(ctx context.Context, name, dir string) error {
	if src, ok := r.pristine[name]; ok {
		return copyTree(src, dir)
	}
	return gitRestore(ctx, dir)
}

// copyTree replaces dst with a fresh copy of src, skipping .git.
func copyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			if info.Name() == ".git" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

// pinFailureMarker tags the two errors that must NEVER be softened into "this
// candidate is unusable, try the next one": a moved upstream tag and an
// unreadable HEAD on a pinned entry. Both mean the corpus no longer says what it
// claims, which stops the run rather than re-pointing it.
const pinFailureMarker = "PIN FAILURE: "

func isPinFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), pinFailureMarker)
}

func shaMatches(pinned, head string) bool {
	if len(pinned) > len(head) {
		return false
	}
	return strings.EqualFold(pinned, head[:len(pinned)])
}

func gitHead(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// goRepos returns the Go repositories inside the tier cap, ordered by
// (tier, measured go-file count) — the "smallest exhibiting" walk order.
func goRepos(m corpus.Manifest, maxTier int, allowLocal bool) []corpus.Entry {
	var out []corpus.Entry
	for _, e := range m.Entries {
		if e.Tier == 0 {
			e.Tier = 1
		}
		if e.Tier > maxTier || e.Language != "go" {
			continue
		}
		if e.URL == "" && !allowLocal {
			continue // a matrix row that runs on a fixture is not evidence
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return out[i].Tier < out[j].Tier
		}
		gi, gj := goFileCount(out[i]), goFileCount(out[j])
		if gi != gj {
			return gi < gj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func goFileCount(e corpus.Entry) int {
	if e.Measured == nil {
		return 1 << 30
	}
	return e.Measured.GoFiles
}

// stratification maps each declared manifest property to its repository.
func stratification(m corpus.Manifest) map[string]string {
	out := map[string]string{}
	for _, p := range m.Stratification {
		if !p.Gap && p.Repo != "" {
			out[p.Property] = p.Repo
		}
	}
	return out
}

func goVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func osArch() string { return runtimeGOOS() + "/" + runtimeGOARCH() }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func panicMarker(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "panic:") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func tail(out []byte) string {
	const max = 1200
	s := strings.TrimSpace(string(out))
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
