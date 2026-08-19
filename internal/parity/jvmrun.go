package parity

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// EnvJVMBinder is the environment variable that registers the experimental JVM
// declared-type binder.
//
// IT IS RE-DECLARED HERE RATHER THAN IMPORTED, and that is a deliberate,
// guarded exception to the single-source rule core/profile enjoys two lines
// below in graphiAxis. engine/semantic's EnvJVM is the authority, but
// engine/semantic is a composition point that reaches the ingest machinery, and
// TestSnapshotBoundary_OnlyGraphstoreIsImported exists precisely to stop this
// harness from linking such a package: an instrument that links the subject can
// perturb it. A one-string copy costs nothing and cannot drift silently, because
// TestEnvJVMBinder_MatchesEngineSemantic READS engine/semantic/semantic.go and
// fails the moment the two disagree — the same shape as citing a constant, with
// none of the linkage.
const EnvJVMBinder = "GRAPHI_JVM_TYPERESOLVE"

// ---------------------------------------------------------------------------
// WP-J7: the real-repository parity matrix over the JVM corpus pins (SW-176).
//
// THE AXIS PROBLEM THIS FILE EXISTS TO SOLVE, STATED BEFORE THE CODE.
//
// The JVM declared-type binder is EXPERIMENTAL and DEFAULT-OFF: engine/semantic
// registers it only when GRAPHI_JVM_TYPERESOLVE is set (EnvJVMBinder), and
// flipping it on by default is a separate, later story. A real-repository JVM
// parity row driven at the shipped default therefore exercises JVM parsing,
// tabling, the heuristic linker and the incremental purge/re-link — and NOT ONE
// LINE of the binder that the whole JVM work package is about. Such a row is not
// wrong; it is NARROW, and a matrix that published it without saying so would be
// the definition of a vacuous green.
//
// MEASURED, so the narrowness is a figure and not an adjective. On the okio pin
// at 8b870e8e, one full index each way:
//
//	binder off   4397 nodes   6946 edges   (calls 2890, imports 115)
//	binder on    4397 nodes   7259 edges   (calls 3119, imports 115,
//	                                        references 56, implements 28)
//
// 529 of those calls edges arrive at the CONFIRMED tier (confidence 1.0), of
// which the binder-off graph has exactly zero. On guava at 2214c636 the same
// comparison reads 46352 nodes both ways, +148 confirmed calls, +22 references,
// +2 implements. The binder is doing real work on real pins, and a default-only
// matrix would measure none of it.
//
// SO THE BINDER IS AN AXIS. Every class runs twice: once at the shipped default
// (binder off) and once with the binder live. Both are published. Neither is
// presented as the other.
//
// AND SO IS THE PROFILE, for a reason that is measured rather than assumed:
// core/profile documents that Fast is the only behaviourally distinct rung
// (Balanced and Deep produce identical graphs since ADR 0010), and Fast SKIPS
// THE RESOLVE PASSES ENTIRELY. Crossing binder × profile therefore contains a
// cell — fast × binder-on — where the opt-in is present and cannot take effect.
// Whether that cell is byte-identical to fast × binder-off is a question the
// matrix answers instead of a claim it makes.
// ---------------------------------------------------------------------------

// JVMAxis is one cell of the JVM matrix's axis crossing.
type JVMAxis struct {
	// Binder turns the experimental declared-type binder on for the measured
	// child processes.
	Binder bool
	// Profile is passed to the binary as -profile. Empty means "pass no
	// -profile flag", i.e. measure the product's own resolved default.
	Profile string
}

// jvmAxes is the declared axis crossing.
//
// THE PROFILE AXIS IS NOT THE HERMETIC TABLE'S PROFILE AXIS, and conflating the
// two would be an overclaim. engine/conformance's parityProfiles() runs
// {ingest.New's ZERO-VALUE profile, Balanced}. The zero value is a LIBRARY entry
// point: nothing under cmd/graphi can produce it, because core/profile's
// ResolveProfile maps "no flag, no env" to Balanced. A dispatch harness that
// drives the BUILT BINARY therefore cannot reach it at all. The honest CLI-side
// axis is {the resolved default (Balanced), Fast} — the pair that is actually
// behaviourally distinct — and it is declared here under its own name so no
// reader can mistake it for the hermetic one.
func jvmAxes() []JVMAxis {
	return []JVMAxis{
		{Binder: false, Profile: ""},
		{Binder: false, Profile: string(profile.Fast)},
		{Binder: true, Profile: ""},
		{Binder: true, Profile: string(profile.Fast)},
	}
}

// Suffix renders the axis as the stable wire suffix of a row id.
//
// It is part of the row's FROZEN identity: -verdict-diff and -counts-diff key
// on the id, so two dispatches can only be compared if the same class under the
// same axis carries the same id on both.
func (a JVMAxis) Suffix() string {
	binder := "off"
	if a.Binder {
		binder = "on"
	}
	prof := a.Profile
	if prof == "" {
		prof = "default"
	}
	return fmt.Sprintf("[binder=%s,profile=%s]", binder, prof)
}

// Describe renders the axis for a human.
func (a JVMAxis) Describe() string {
	binder := "the JVM declared-type binder is OFF — the shipped default"
	if a.Binder {
		binder = "the experimental JVM declared-type binder is LIVE (" + EnvJVMBinder + "=1)"
	}
	prof := "the product's own resolved default profile"
	if a.Profile != "" {
		prof = "-profile " + a.Profile
	}
	return binder + "; " + prof
}

// jvmRepos returns the JVM repositories inside the tier cap, ordered by
// (tier, measured source-file count) — the same "smallest exhibiting" walk
// order the Go side uses, so a class lands on the smallest pin that can host it
// and the run stays affordable.
//
// okio is INCLUDED even though corpus/manifest.json records it
// `excluded_from_corpus_scale: true`. That exclusion is SW-173's, and it is
// about the BYTECODE ORACLE: okio does not compile reproducibly, so it can
// contribute no ground-truth recall figure. Parity needs no bytecode — it
// compares graphi against graphi over the same tree — so the oracle's exclusion
// does not reach this matrix. Stating it here rather than leaving a reader to
// wonder why an "excluded" pin appears in a published row.
func jvmRepos(m corpus.Manifest, maxTier int, allowLocal bool) []corpus.Entry {
	var out []corpus.Entry
	for _, e := range m.Entries {
		if e.Tier == 0 {
			e.Tier = 1
		}
		if e.Tier > maxTier || (e.Language != langJava && e.Language != langKotlin) {
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
		si, sj := jvmFileCount(out[i]), jvmFileCount(out[j])
		if si != sj {
			return si < sj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func jvmFileCount(e corpus.Entry) int {
	if e.Measured == nil || e.Measured.SourceFiles == 0 {
		return 1 << 30
	}
	return e.Measured.SourceFiles
}

// RunJVM executes the JVM real-repository matrix and returns the report.
//
// Like Run, it returns an error only for harness-level faults: a FAILING ROW IS
// THE REPORT'S JOB.
func (r *Runner) RunJVM(ctx context.Context, m corpus.Manifest, rows []ClassRow, prov parityreport.Provenance) (parityreport.Report, error) {
	if r.PerClassTimeout <= 0 {
		r.PerClassTimeout = 15 * time.Minute
	}
	r.MaxTier = clampTier(r.MaxTier)
	r.jvmModels = map[string]*JVMModel{}
	r.dirs = map[string]string{}
	r.pristine = map[string]string{}
	if err := os.MkdirAll(r.WorkDir, 0o755); err != nil {
		return parityreport.Report{}, fmt.Errorf("parity: workdir: %w", err)
	}

	prov.RunnerClass = r.RunnerClass
	prov.GoVersion = goVersion()
	prov.OSArch = osArch()
	prov.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	prov.IndexProfile = "PER-ROW AXIS — every class runs under each cell of " +
		"binder{off,on} x profile{resolved default, fast}; the cell is in the row id and in axis_note. " +
		profile.EnvName + " and " + EnvJVMBinder + " are both CLEARED for the child and then set " +
		"explicitly per cell, so an inherited value cannot silently change what was measured."

	axes := jvmAxes()
	rep := parityreport.Report{
		Provenance:   prov,
		Family:       "jvm",
		MaxTier:      r.MaxTier,
		ClassFilter:  r.Classes,
		MatrixSource: ClassesPathJVM,
		NotCompared:  parityreport.DefaultNotCompared(),
	}

	candidates := jvmRepos(m, r.MaxTier, r.AllowLocal)
	byID := JVMSpecByID()

	for _, axis := range axes {
		for _, row := range rows {
			res := parityreport.ClassResult{
				ID: row.ID + axis.Suffix(), Kind: row.Kind, Label: row.Label,
				KnownDefect: row.KnownDefect, AxisNote: axis.Describe(),
			}
			switch {
			case row.HarnessRow == harnessDeferred:
				res.Verdict = parityreport.VerdictDeferred
				res.DeferredTo = row.DeferredTo
				res.Detail = "declared harness_row: deferred in " + ClassesPathJVM + "; owned by " + row.DeferredTo + "."
				rep.Classes = append(rep.Classes, res)
				continue
			case row.Kind != kindChangeClass:
				res.Verdict = parityreport.VerdictDeferred
				res.DeferredTo = row.DeferredTo
				res.Detail = "not a change class; owned by " + row.DeferredTo
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
				res.Detail = ClassesPathJVM + " declares change class " + row.ID +
					" with harness_row: required, but the real-repo JVM class table has no planner for it"
				rep.Classes = append(rep.Classes, res)
				continue
			}
			if spec.Note != "" {
				res.Detail = spec.Note
			}
			r.runJVMClass(ctx, &res, spec, axis, candidates, &rep)
			rep.Classes = append(rep.Classes, res)
		}
	}

	for _, name := range sortedStrings(r.dirs) {
		for _, e := range candidates {
			if e.Name != name {
				continue
			}
			head, _ := gitHead(ctx, r.dirs[name])
			rep.Repos = append(rep.Repos, parityreport.RepoRef{
				Name: e.Name, URL: e.URL, Ref: e.Ref,
				PinnedSHA: e.SHA, HeadSHA: head, Tier: e.Tier,
				SourceFiles: jvmFileCountOrZero(e),
			})
		}
	}

	// THE COMPLETENESS COUNT IS THE CROSSED COUNT, and that is deliberate.
	//
	// Finalize's contract is "every declared row of each kind is decided or
	// deferred". This run's declared row set IS the crossing: 13 declared JVM
	// change classes times len(axes) cells. Passing the un-crossed 13 would let
	// a run that decided 13 of 52 rows certify itself complete, which is the
	// exact self-certification the parameter exists to prevent.
	rep.Finalize(CountChangeClasses(rows)*len(axes), CountCrashConditions(rows)*len(axes))
	rep.GateNote = "WP-J7 / gate G4: the real-repository full-vs-incremental parity matrix over the JVM corpus " +
		"pins, crossed over binder{off,on} x profile{resolved default, fast}. IT IS NOT THE PRD FR-7 GO MATRIX " +
		"and settles none of its rows; the FR-7 row set lives in " + ClassesPath + " and is published separately. " +
		"A PASS here is a PARITY statement — the incrementally-updated graph and a fresh full index of the same " +
		"final tree are byte-identical — and never a correctness statement about any edge, because no ground truth " +
		"for a real repository's JVM edge set exists. Rows on the binder=off cells exercise JVM parse, tabling, the " +
		"heuristic linker and the incremental purge/re-link, and DO NOT exercise the declared-type binder at all; " +
		"the binder=on cells are the only ones that do, and they measure a configuration the product does not ship " +
		"by default. No performance, latency or RSS figure is published here."
	return rep, nil
}

func jvmFileCountOrZero(e corpus.Entry) int {
	if e.Measured == nil {
		return 0
	}
	return e.Measured.SourceFiles
}

// runJVMClass executes one JVM change-class row under one axis cell.
func (r *Runner) runJVMClass(ctx context.Context, res *parityreport.ClassResult, spec JVMClassSpec,
	axis JVMAxis, candidates []corpus.Entry, rep *parityreport.Report) {

	start := time.Now()
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	ctx, cancel := context.WithTimeout(ctx, r.PerClassTimeout)
	defer cancel()

	entry, mut, why, err := r.selectJVMRepo(ctx, spec, candidates)
	if err != nil {
		res.Verdict = parityreport.VerdictError
		res.Detail = err.Error()
		r.logf("parity: %-46s ERROR   %v", res.ID, err)
		return
	}
	if mut == nil {
		res.Verdict = parityreport.VerdictSkipped
		res.Detail = why
		r.logf("parity: %-46s SKIPPED %s", res.ID, why)
		return
	}
	res.Repo = entry.Name
	res.SelectedBecause = why
	res.Mutation = mut.Desc
	repoDir := r.dirs[entry.Name]
	res.RepoHeadSHA, _ = gitHead(ctx, repoDir)

	stateDir := filepath.Join(r.WorkDir, "state", entry.Name, res.ID)
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
		r.logf("parity: %-46s ERROR   %s", res.ID, firstLine(detail))
	}

	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		fail("restore clone to the pinned tree: " + err.Error())
		return
	}
	if out, err := r.graphiAxis(ctx, repoDir, axis, "rebuild", "-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("baseline rebuild: " + err.Error() + "\n" + tail(out))
		return
	}
	if err := applyMutation(repoDir, mut); err != nil {
		fail("apply mutation: " + err.Error())
		return
	}
	if out, err := r.graphiAxis(ctx, repoDir, axis, "sync", "-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("incremental sync: " + err.Error() + "\n" + tail(out))
		return
	}
	if out, err := r.graphiAxis(ctx, repoDir, axis, "rebuild", "-root", repoDir, "-db", fullDB, "-meta", stateDir+"/fullmeta"); err != nil {
		fail("final rebuild: " + err.Error() + "\n" + tail(out))
		return
	}

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

	rep.StoreCounts = append(rep.StoreCounts,
		storeCounts(entry.Name, res.ID, "full", fullGraph),
		storeCounts(entry.Name, res.ID, "incremental", incGraph))

	if bytes.Equal(incBytes, fullBytes) {
		res.Verdict = parityreport.VerdictPass
		r.logf("parity: %-46s PASS    %s (%d nodes / %d edges)", res.ID, entry.Name, res.FullNodes, res.FullEdges)
	} else {
		res.Verdict = parityreport.VerdictFail
		res.Detail = fmt.Sprintf("SNAPSHOT BYTES DIFFER. incremental %d nodes / %d edges (%s); full %d nodes / %d edges (%s). %s",
			res.IncNodes, res.IncEdges, short(res.SnapshotIncSHA256),
			res.FullNodes, res.FullEdges, short(res.SnapshotFullSHA256), res.Detail)
		diag, derr := r.graphiAxis(ctx, repoDir, axis, "compare-branches", "-base", fullDB, "-head", incDB)
		if derr != nil {
			res.Diagnostic = "graphi compare-branches failed: " + derr.Error()
		} else {
			res.Diagnostic = tail(diag)
			if strings.Contains(res.Diagnostic, `"outcome":"empty"`) {
				res.DiagnosticContradiction = true
				res.Detail += " FINDING: the BranchDiffReport reports NO deltas while the snapshot bytes differ."
			}
		}
		r.logf("parity: %-46s FAIL    %s (inc %d/%d vs full %d/%d)", res.ID, entry.Name,
			res.IncNodes, res.IncEdges, res.FullNodes, res.FullEdges)
	}

	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		r.logf("parity: %s: WARNING restoring clone: %v", res.ID, err)
	}
}

// selectJVMRepo resolves the repository a JVM class runs on: the smallest
// in-cap pin whose REAL SOURCE the planner can find a target in, subject to the
// spec's language restriction.
func (r *Runner) selectJVMRepo(ctx context.Context, spec JVMClassSpec, candidates []corpus.Entry) (corpus.Entry, *Mutation, string, error) {
	var tried []string
	for _, e := range candidates {
		if spec.Lang != "" && e.Language != spec.Lang {
			tried = append(tried, e.Name+" (manifest language "+e.Language+"; this class is "+spec.Lang+"-only)")
			continue
		}
		model, err := r.materializeJVM(ctx, e)
		if err != nil {
			if isPinFailure(err) {
				return corpus.Entry{}, nil, "", err
			}
			tried = append(tried, e.Name+" (unusable: "+firstLine(err.Error())+")")
			continue
		}
		mut, perr := spec.Plan(model)
		if perr == errNoTarget {
			tried = append(tried, e.Name)
			continue
		}
		if perr != nil {
			return corpus.Entry{}, nil, "", fmt.Errorf("parity: JVM class %q planner on %s: %w", spec.ID, e.Name, perr)
		}
		why := fmt.Sprintf("smallest in-cap JVM pin whose real source exhibits this class (tier %d, %d source files, manifest language %s)",
			e.Tier, jvmFileCountOrZero(e), e.Language)
		if len(tried) > 0 {
			why += "; not exhibited by " + strings.Join(tried, ", ")
		}
		return e, mut, why, nil
	}
	detail := fmt.Sprintf("no JVM pin within -max-tier %d exhibits the structure this class needs", r.MaxTier)
	if len(tried) > 0 {
		detail += " (examined: " + strings.Join(tried, ", ") + ")"
	}
	return corpus.Entry{}, nil, detail, nil
}

// materializeJVM clones (or reuses) a JVM pin and scans its pristine model.
//
// THE MODEL IS SCANNED FROM THE MATERIALIZED CLONE, never from a worktree the
// harness did not create. SW-175's finding is why that is stated: a smudge/clean
// filter can make a worktree differ from the blob it was checked out from
// without `git status` saying a word, so a model read from anywhere but a fresh
// clone at the verified pin is a model of an unknown tree.
func (r *Runner) materializeJVM(ctx context.Context, e corpus.Entry) (*JVMModel, error) {
	if m, ok := r.jvmModels[e.Name]; ok {
		return m, nil
	}
	dir := filepath.Join(r.WorkDir, "repos", e.Name)
	if e.URL != "" {
		r.logf("parity: cloning %s @ %s", e.Name, e.Ref)
		if err := clone(ctx, e, dir); err != nil {
			return nil, err
		}
	} else {
		src := e.Path
		if !filepath.IsAbs(src) {
			abs, aerr := filepath.Abs(src)
			if aerr != nil {
				return nil, aerr
			}
			src = abs
		}
		if err := copyTree(src, dir); err != nil {
			return nil, err
		}
		r.pristine[e.Name] = src
	}
	head, herr := gitHead(ctx, dir)
	if e.SHA != "" {
		if herr != nil {
			return nil, fmt.Errorf("parity: %s%s: sha pinned but HEAD unreadable: %v", pinFailureMarker, e.Name, herr)
		}
		if !shaMatches(e.SHA, head) {
			return nil, fmt.Errorf("parity: %s%s: HEAD %s does not match pinned sha %s (ref %q moved?)",
				pinFailureMarker, e.Name, head, e.SHA, e.Ref)
		}
	}
	model, err := discoverJVM(dir)
	if err != nil {
		return nil, err
	}
	r.jvmModels[e.Name] = model
	r.dirs[e.Name] = dir
	return model, nil
}

// graphiAxis runs the binary under test with the axis cell's configuration.
//
// BOTH environment variables are CLEARED and then set explicitly, for the same
// reason the Go harness clears GRAPHI_INDEX_PROFILE: an inherited
// GRAPHI_JVM_TYPERESOLVE=1 in an operator's shell would silently make every
// "binder off" row measure the binder, and the report would still say off.
// Clearing is what makes the axis label checkable rather than trusted.
func (r *Runner) graphiAxis(ctx context.Context, cwd string, axis JVMAxis, args ...string) ([]byte, error) {
	env := []string{profile.EnvName + "=", EnvJVMBinder + "="}
	if axis.Binder {
		env[1] = EnvJVMBinder + "=1"
	}
	if axis.Profile != "" && len(args) > 0 && (args[0] == "rebuild" || args[0] == "sync") {
		args = append(args, "-profile", axis.Profile)
	}
	return r.graphiEnv(ctx, cwd, env, args...)
}
