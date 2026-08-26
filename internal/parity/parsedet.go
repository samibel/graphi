package parity

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// ---------------------------------------------------------------------------
// W5.n / gate G4: the REAL-REPOSITORY PARSE-DETERMINISM matrix for the six
// intra/parse residual languages (css, hcl, json, markdown, toml, yaml).
//
// THE WITNESS SHAPE IS PARSE-DETERMINISM, NOT CROSS-FILE PARITY, and that is
// SW-199's ruling, not this file's. Those six languages define no cross-file
// change class graphi extracts (docs/rc/parity-classes-<lang>.yaml §5.5), so
// there is no cross-file contract for a real-repo row to assert. What a row
// asserts instead is the property the hermetic table pins — the SAME INPUT
// BYTES PRODUCE THE SAME AST, and the AST's serialisation is byte-stable —
// scaled from a t.TempDir() fixture to a real repository:
//
//	pass A   `graphi rebuild` over the final tree into a fresh store
//	pass B   `graphi rebuild` over the SAME final tree into a SECOND fresh store
//	inc      `graphi rebuild` at the pin, the class's real edit, `graphi sync`
//
// and requires envelope(A) == envelope(B) (parse determinism) AND
// envelope(inc) == envelope(A) (intra-file parity). Both comparisons are BYTE
// equality of the portable snapshot envelope, never node identity: an
// identity-stable but byte-different serialisation is exactly the per-dispatch
// variance this row exists to catch, and a node-identity assertion would let it
// through.
//
// THE COUNTERS ARE NOT THE PARSER'S OWN (SW-175 FINDING B-2). A row's verdict
// is decided by sha256 over bytes graphi wrote and this harness read back
// through core/graphstore, plus a NON-VACUITY WITNESS counted from the decoded
// envelope. Nothing in the decision path asks the parser how many symbols it
// thinks it produced.
//
// ---------------------------------------------------------------------------
// WHAT THIS FAMILY CANNOT DO ON THIS TREE. Stated here, in the instrument,
// because a reader who finds the code before the story must not infer that a
// green run is available and simply was not taken. Three refusals stand
// between this harness and a publishable G4 row, and NONE of them is a defect
// of this file:
//
//  1. NO REAL-REPO PIN. corpus/manifest.json on main carries no tier>=2 entry
//     for any of the six languages — SW-201's pins live on an unmerged branch
//     and json has no pin even there (an honest no_pin abstention). Every row
//     therefore SKIPS with the reason named, and a skipped row costs
//     publishability by construction (parityreport.Report.Finalize).
//
//  2. THE STORE AXIS IS HERMETIC-ONLY. The hermetic table crosses
//     {mem, sqlite} x {zero-value profile, balanced}. A DISPATCH harness can
//     reach neither half of the first pair's mem side: every CLI path that
//     persists a graph opens SQLite (cmd/graphi.openStore treats an empty -db
//     as an in-process MemStore whose contents die with the process, and
//     `graphi snapshot create` hardcodes OpenSQLite), so no built-binary run
//     can emit a MemStore envelope at all. parseDetAxes() therefore declares
//     the two cells that ARE reachable and says so, rather than relabelling a
//     second SQLite configuration as "the other store" — which would be the
//     LANGHONEST-001 shape one axis over.
//
//  3. THE COMPLETENESS FLOOR. parityreport.Report.Finalize requires
//     declaredChangeClasses >= parityreport.FR7ChangeClasses (15). Six declared
//     classes crossed over the two reachable axes is TWELVE, so this family is
//     structurally incomplete and therefore unpublishable EVEN WITH PINS AND A
//     MATCHING CANDIDATE. That is filed as PARITY-011; it is not worked around
//     here, and no axis was invented to clear the floor.
//
// A fourth refusal is inherited rather than owned: while the product binary at
// HEAD differs from parityreport.CandidateSHA, CollectProvenance fails closed
// and -verdict-diff / -counts-diff exit 2 on "publication refused" (D7DEBT-001).
// ---------------------------------------------------------------------------

// ParseDetLanguages are the six intra/parse residual languages this family
// serves, in the story's letter order (a..f). The list is CLOSED: adding a
// language here without its docs/rc/parity-classes-<lang>.yaml twin fails
// TestParseDet_BindsToDeclaredMatrix.
func ParseDetLanguages() []string {
	return []string{"css", "hcl", "json", "markdown", "toml", "yaml"}
}

// IsParseDetLanguage reports whether name is one of the six.
func IsParseDetLanguage(name string) bool { return contains(ParseDetLanguages(), name) }

// ParseDetClassesPath is the language's declared class table — SW-199's, and
// the only authority on which classes exist. This harness binds to the same
// file the hermetic twin binds to, so the two can never disagree about the row
// set.
func ParseDetClassesPath(lang string) string {
	return "docs/rc/parity-classes-" + lang + ".yaml"
}

// FamilyParseDet renders the Report.Family value for one language.
func FamilyParseDet(lang string) string { return "parsedet-" + lang }

// ---------------------------------------------------------------------------
// The axis
// ---------------------------------------------------------------------------

// ParseDetAxis is one cell of the crossing. Store is carried explicitly, and
// carries exactly one value, so the report states WHICH store was measured
// instead of leaving a reader to assume both were.
type ParseDetAxis struct {
	// Store names the graphstore backend the measured child process used.
	Store string
	// Profile is passed to the binary as -profile. Empty means "pass no
	// -profile flag", i.e. measure the product's own resolved default.
	Profile string
}

// ParseDetStoreSQLite is the only backend a dispatch-driven full index can
// produce an envelope from. See refusal (2) in this file's header.
const ParseDetStoreSQLite = "sqlite"

// parseDetAxes is the declared axis crossing: the ONE reachable store crossed
// with the two behaviourally distinct CLI profiles.
//
// The profile pair follows jvmAxes()'s reasoning verbatim and for the same
// reason: core/profile.ResolveProfile maps "no flag, no env" to Balanced, so a
// dispatch harness cannot reach ingest.New's zero value at all, and Balanced
// and Deep produce identical graphs since ADR 0010. The honest CLI-side pair is
// therefore {the resolved default, Fast} — Fast being the only rung that skips
// the resolve passes.
func parseDetAxes() []ParseDetAxis {
	return []ParseDetAxis{
		{Store: ParseDetStoreSQLite, Profile: ""},
		{Store: ParseDetStoreSQLite, Profile: string(profile.Fast)},
	}
}

// Suffix renders the axis as the stable wire suffix of a row id. It is part of
// the row's FROZEN identity: -verdict-diff and -counts-diff key on the id, so
// two dispatches can only be compared if the same class under the same axis
// carries the same id on both.
func (a ParseDetAxis) Suffix() string {
	prof := a.Profile
	if prof == "" {
		prof = "default"
	}
	return fmt.Sprintf("[store=%s,profile=%s]", a.Store, prof)
}

// Describe renders the axis for a human, and names the axis that is NOT here.
func (a ParseDetAxis) Describe() string {
	prof := "the product's own resolved default profile"
	if a.Profile != "" {
		prof = "-profile " + a.Profile
	}
	return "graphstore backend " + a.Store + " (the only backend a dispatch-driven full index can emit an " +
		"envelope from — the hermetic table's mem backend is unreachable through the built binary); " + prof
}

// ---------------------------------------------------------------------------
// The per-language model
// ---------------------------------------------------------------------------

// ParseDetFile is one source file of the measured tree, read once at
// materialization so a planner works from bytes rather than re-reading disk.
type ParseDetFile struct {
	// Rel is the repo-relative, slash-separated path.
	Rel string
	// Data is the file's bytes at the pinned tree.
	Data []byte
}

// ParseDetModel is the language's view of a materialized tree: every tracked
// source file of the language's extensions, in a deterministic order.
//
// It is a THIRD model type beside RepoModel and JVMModel rather than a widening
// of either, for the reason jvmsource.go's header already gives: the three
// share almost no structure, and a "generalised" model would be an
// almost-empty intersection carrying a union of language-specific fields.
type ParseDetModel struct {
	Root  string
	Lang  string
	Files []ParseDetFile
}

// parseDetSkipDir refuses directories whose contents are not the repository's
// own source. It is deliberately the same shape as the Go model's skip set:
// under-skipping costs run time, over-skipping costs rows, and both are
// preferable to measuring a vendored tree as if it were the pin's.
func parseDetSkipDir(base string) bool {
	switch base {
	case ".git", "vendor", "node_modules", "third_party", "testdata", "dist", "build":
		return true
	}
	return strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")
}

// discoverParseDet walks a materialized tree and collects the language's source
// files. The order is lexicographic on Rel, so a planner's choice of target is
// reproducible across dispatches — a planner that picked "the first file the
// walk yielded" would make the ROW ITSELF non-deterministic and would then
// report that as a product finding.
func discoverParseDet(root, lang string, exts []string) (*ParseDetModel, error) {
	m := &ParseDetModel{Root: root, Lang: lang}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			if rel != "." && parseDetSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if !hasAnyExt(rel, exts) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		m.Files = append(m.Files, ParseDetFile{Rel: rel, Data: b})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Rel < m.Files[j].Rel })
	return m, nil
}

func hasAnyExt(rel string, exts []string) bool {
	for _, e := range exts {
		if strings.HasSuffix(rel, e) {
			return true
		}
	}
	return false
}

// Dirs returns the distinct directories holding this language's files, sorted.
func (m *ParseDetModel) Dirs() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range m.Files {
		d := path.Dir(f.Rel)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The per-language specification
// ---------------------------------------------------------------------------

// ParseDetPlanner locates a real edit target in a real tree and returns the
// mutation. It returns errNoTarget when the tree does not exhibit the structure
// the class needs — the same walk-on signal the Go planners use.
type ParseDetPlanner func(m *ParseDetModel) (*Mutation, error)

// ParseDetSpec binds one declared class id to the real-source edit that
// exercises it and to the non-vacuity witness that stops the row passing over
// an empty graph.
type ParseDetSpec struct {
	ID   string
	Plan ParseDetPlanner
	// Note is carried into the report row, stating what the row does and does
	// not prove.
	Note string
}

// ParseDetLanguage is one language's whole binding: what its files look like,
// how each class edits them, and what a non-vacuous graph over them contains.
type ParseDetLanguage struct {
	// Name is the manifest `language` value AND the -family value.
	Name string
	// Exts are the file extensions the language's parser claims.
	Exts []string
	// Specs is the class table. Every id here must appear in the language's
	// declared YAML (the PHANTOM direction) and every declared, non-deferred
	// change class must appear here (the MISSING direction); both are asserted
	// by TestParseDet_BindsToDeclaredMatrix.
	Specs []ParseDetSpec
	// MinSymbolKinds are node kinds the language's extractor is expected to
	// mint on a non-empty tree. A row whose measured graph carries NONE of them
	// is VACUOUS and fails, whatever the byte comparison said: two empty
	// graphs are byte-identical, and a family that scored that as PASS would be
	// the cheapest false green in this repository.
	//
	// It is EMPTY for a ParseOnly language, and that is not an omission — see
	// ParseOnly.
	MinSymbolKinds []string
	// ParseOnly marks a language whose documents mint NO node at all (json;
	// docs/rc/parity-classes-json.yaml:63). For such a language the correct
	// graph over a changed document is the UNCHANGED graph, so a graph-level
	// vacuity check cannot distinguish "graphi abstained correctly" from
	// "graphi did nothing" — and MinSymbolKinds would be a claim the language
	// does not support. The witness moves to the PARSE BOUNDARY instead: the
	// edited document is read back out of the materialized tree and must be
	// exactly what the mutation said it would be.
	ParseOnly bool
}

// parseDetLanguages is the closed registry.
func parseDetLanguages() map[string]ParseDetLanguage {
	out := map[string]ParseDetLanguage{}
	for _, l := range []ParseDetLanguage{
		parseDetCSS(), parseDetHCL(), parseDetJSON(),
		parseDetMarkdown(), parseDetTOML(), parseDetYAML(),
	} {
		out[l.Name] = l
	}
	return out
}

// ParseDetLanguageFor returns the binding for one language.
func ParseDetLanguageFor(name string) (ParseDetLanguage, bool) {
	l, ok := parseDetLanguages()[name]
	return l, ok
}

// SpecByID indexes a language's class table.
func (l ParseDetLanguage) SpecByID() map[string]ParseDetSpec {
	out := map[string]ParseDetSpec{}
	for _, s := range l.Specs {
		out[s.ID] = s
	}
	return out
}

// ---------------------------------------------------------------------------
// The runner
// ---------------------------------------------------------------------------

// parseDetRepos returns the language's manifest entries inside the tier cap,
// ordered by (tier, measured source-file count) — the same "smallest
// exhibiting" walk order the Go and JVM halves use.
//
// A no_pin ABSTENTION ENTRY IS NOT A CANDIDATE. corpus/manifest.json carries
// honest `no_pin: true` rows (SW-196's bash and sql shape) whose whole point is
// to declare that no pin exists; admitting one here would turn a declared
// absence into a row that looks like it ran.
func parseDetRepos(m corpus.Manifest, lang string, maxTier int, allowLocal bool) []corpus.Entry {
	var out []corpus.Entry
	for _, e := range m.Entries {
		if e.Tier == 0 {
			e.Tier = 1
		}
		if e.Tier > maxTier || e.Language != lang || e.NoPin {
			continue
		}
		if e.URL == "" && !allowLocal {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return out[i].Tier < out[j].Tier
		}
		return parseDetFileCount(out[i]) < parseDetFileCount(out[j])
	})
	return out
}

func parseDetFileCount(e corpus.Entry) int {
	if e.Measured == nil {
		return 0
	}
	return e.Measured.SourceFiles
}

// RunParseDeterminism executes the parse-determinism matrix for r.ParseDetLang
// and returns the report. It returns an error only for harness-level faults — a
// FAILING ROW IS THE REPORT'S JOB.
func (r *Runner) RunParseDeterminism(ctx context.Context, m corpus.Manifest, rows []ClassRow,
	prov parityreport.Provenance) (parityreport.Report, error) {

	lang, ok := ParseDetLanguageFor(r.ParseDetLang)
	if !ok {
		return parityreport.Report{}, fmt.Errorf(
			"parity: %q is not an intra/parse residual language; the closed set is %s",
			r.ParseDetLang, strings.Join(ParseDetLanguages(), ", "))
	}
	if r.PerClassTimeout <= 0 {
		r.PerClassTimeout = 15 * time.Minute
	}
	r.MaxTier = clampTier(r.MaxTier)
	r.parseDetModels = map[string]*ParseDetModel{}
	r.dirs = map[string]string{}
	r.pristine = map[string]string{}
	if err := os.MkdirAll(r.WorkDir, 0o755); err != nil {
		return parityreport.Report{}, fmt.Errorf("parity: workdir: %w", err)
	}

	classesPath := ParseDetClassesPath(lang.Name)
	axes := parseDetAxes()

	prov.RunnerClass = r.RunnerClass
	prov.GoVersion = goVersion()
	prov.OSArch = osArch()
	prov.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	prov.IndexProfile = "PER-ROW AXIS — every class runs under each cell of store{" + ParseDetStoreSQLite +
		"} x profile{resolved default, fast}; the cell is in the row id and in axis_note. " +
		profile.EnvName + " is CLEARED for the child and then set explicitly per cell, so an inherited " +
		"value cannot silently change what was measured. The hermetic table's mem-store half is NOT here " +
		"and is not claimed: no dispatch-driven full index can emit a MemStore envelope."

	rep := parityreport.Report{
		Provenance:   prov,
		Family:       FamilyParseDet(lang.Name),
		MaxTier:      r.MaxTier,
		ClassFilter:  r.Classes,
		MatrixSource: classesPath,
		NotCompared:  parityreport.DefaultNotCompared(),
	}

	candidates := parseDetRepos(m, lang.Name, r.MaxTier, r.AllowLocal)
	byID := lang.SpecByID()

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
				res.Detail = "declared harness_row: deferred in " + classesPath + "; owned by " + row.DeferredTo + "."
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
				res.Detail = classesPath + " declares change class " + row.ID +
					" with harness_row: required, but the " + lang.Name +
					" real-repo parse-determinism table has no planner for it"
				rep.Classes = append(rep.Classes, res)
				continue
			}
			if spec.Note != "" {
				res.Detail = spec.Note
			}
			r.runParseDetClass(ctx, &res, lang, spec, axis, candidates, &rep)
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
				SourceFiles: parseDetFileCount(e),
			})
		}
	}

	// THE COMPLETENESS COUNT IS THE CROSSED COUNT, for the reason RunJVM states:
	// passing the un-crossed six would let a run that decided six of twelve rows
	// certify itself complete.
	//
	// It is ALSO, on this family, arithmetically below Finalize's FR-7 floor
	// (12 < parityreport.FR7ChangeClasses). That is not corrected here. The
	// floor is a real property of the report contract and this family really
	// does declare fewer rows than FR-7's Go matrix; the honest outcome is an
	// incomplete, unpublishable run with the arithmetic legible in the refusal
	// string, and the contract question is filed as PARITY-011.
	rep.Finalize(CountChangeClasses(rows)*len(axes), CountCrashConditions(rows)*len(axes))
	rep.GateNote = "W5.n / gate GA-LANG-" + lang.Name + "-G4: the real-repository PARSE-DETERMINISM matrix for " +
		lang.Name + ", crossed over store{" + ParseDetStoreSQLite + "} x profile{resolved default, fast}. " +
		"IT IS NOT THE PRD FR-7 GO MATRIX and settles none of its rows. A PASS here is a DETERMINISM AND " +
		"INTRA-FILE PARITY statement — two independent full passes over identical bytes serialise identically, " +
		"and the incrementally-updated graph is byte-identical to a fresh full index of the same final tree — " +
		"and never a CORRECTNESS statement about any symbol or edge, because parity compares two passes of the " +
		"same rule. Correctness evidence for the extractor lives in core/parse/parser_" + lang.Name + "_test.go. " +
		"No performance, latency or RSS figure is published here. This family cannot reach a publishable state " +
		"on a tree whose declared class count crossed over the reachable axes is below " +
		fmt.Sprintf("%d", parityreport.FR7ChangeClasses) + " (PARITY-011), nor while the product binary differs " +
		"from the candidate (D7DEBT-001)."
	return rep, nil
}

// runParseDetClass runs one (class, axis) cell.
func (r *Runner) runParseDetClass(ctx context.Context, res *parityreport.ClassResult,
	lang ParseDetLanguage, spec ParseDetSpec, axis ParseDetAxis,
	candidates []corpus.Entry, rep *parityreport.Report) {

	start := time.Now()
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	ctx, cancel := context.WithTimeout(ctx, r.PerClassTimeout)
	defer cancel()

	entry, model, mut, why, err := r.selectParseDetRepo(ctx, lang, spec, candidates)
	if err != nil {
		res.Verdict = parityreport.VerdictError
		res.Detail = err.Error()
		r.logf("parity: %-44s ERROR   %v", res.ID, err)
		return
	}
	if mut == nil {
		res.Verdict = parityreport.VerdictSkipped
		res.Detail = why
		r.logf("parity: %-44s SKIPPED %s", res.ID, why)
		return
	}
	res.Repo = entry.Name
	res.SelectedBecause = why
	res.Mutation = mut.Desc
	repoDir := r.dirs[entry.Name]
	res.RepoHeadSHA, _ = gitHead(ctx, repoDir)
	_ = model

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
	fullADB := filepath.Join(stateDir, "full-a.db")
	fullBDB := filepath.Join(stateDir, "full-b.db")
	incSnap := filepath.Join(stateDir, "inc.snapshot")
	fullASnap := filepath.Join(stateDir, "full-a.snapshot")
	fullBSnap := filepath.Join(stateDir, "full-b.snapshot")

	fail := func(detail string) {
		res.Verdict = parityreport.VerdictError
		res.Detail = detail
		r.logf("parity: %-44s ERROR   %s", res.ID, firstLine(detail))
	}

	// The tree must start pristine: the previous row's edits are reverted before
	// this one's baseline is indexed, or the "incremental" side would be
	// updating from the previous row's state rather than from the pin.
	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		fail("restore clone to the pinned tree: " + err.Error())
		return
	}

	// 1. Baseline full index at the pinned tree — the state the incremental pass
	//    will update FROM.
	if out, err := r.graphiAxisParseDet(ctx, repoDir, axis, "rebuild",
		"-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("baseline rebuild: " + err.Error() + "\n" + tail(out))
		return
	}

	// 2. Apply the change class as a REAL edit to REAL source.
	if err := applyMutation(repoDir, mut); err != nil {
		fail("apply mutation: " + err.Error())
		return
	}

	// 3. Incremental update over the edited tree.
	if out, err := r.graphiAxisParseDet(ctx, repoDir, axis, "sync",
		"-root", repoDir, "-db", incDB, "-meta", stateDir+"/incmeta"); err != nil {
		fail("incremental sync: " + err.Error() + "\n" + tail(out))
		return
	}

	// 4. TWO independent full indexes of the SAME final tree, into two stores
	//    that have never seen the pre-edit state. Pass B is what makes this the
	//    PARSE-DETERMINISM row rather than the intra-file-parity row: A vs B is
	//    the same bytes twice, with no incremental machinery between them.
	if out, err := r.graphiAxisParseDet(ctx, repoDir, axis, "rebuild",
		"-root", repoDir, "-db", fullADB, "-meta", stateDir+"/fullameta"); err != nil {
		fail("full pass A: " + err.Error() + "\n" + tail(out))
		return
	}
	if out, err := r.graphiAxisParseDet(ctx, repoDir, axis, "rebuild",
		"-root", repoDir, "-db", fullBDB, "-meta", stateDir+"/fullbmeta"); err != nil {
		fail("full pass B: " + err.Error() + "\n" + tail(out))
		return
	}

	// 5. THE ASSERTIONS, over the portable snapshot envelope's BYTES.
	for _, pair := range [][2]string{{incDB, incSnap}, {fullADB, fullASnap}, {fullBDB, fullBSnap}} {
		if err := emitSnapshot(pair[0], pair[1]); err != nil {
			fail(err.Error())
			return
		}
	}
	incGraph, incBytes, err := readGraph(incSnap)
	if err != nil {
		fail(err.Error())
		return
	}
	fullAGraph, fullABytes, err := readGraph(fullASnap)
	if err != nil {
		fail(err.Error())
		return
	}
	_, fullBBytes, err := readGraph(fullBSnap)
	if err != nil {
		fail(err.Error())
		return
	}

	incSHA, fullASHA, fullBSHA := digest(incBytes), digest(fullABytes), digest(fullBBytes)
	res.SnapshotIncSHA256 = incSHA
	res.SnapshotFullSHA256 = fullASHA
	res.IncNodes, res.IncEdges = len(incGraph.Nodes), len(incGraph.Edges)
	res.FullNodes, res.FullEdges = len(fullAGraph.Nodes), len(fullAGraph.Edges)

	// 6. The two PRD §12.3 store-level counts, on both compared sides.
	rep.StoreCounts = append(rep.StoreCounts,
		storeCounts(entry.Name, res.ID, "inc", incGraph),
		storeCounts(entry.Name, res.ID, "full", fullAGraph))

	// 7. NON-VACUITY, BEFORE the byte comparison decides anything. Two empty
	//    graphs are byte-identical; scoring that as PASS would certify a parser
	//    that indexed nothing. The witness counts kinds out of the DECODED
	//    ENVELOPE — bytes graphi wrote and this harness read back — never out of
	//    a figure the parser reported about itself (SW-175 FINDING B-2).
	if why := parseDetVacuity(lang, fullAGraph, mut, repoDir); why != "" {
		res.Verdict = parityreport.VerdictFail
		res.Detail = "VACUOUS: " + why + " — the byte comparison is not consulted, because two empty " +
			"graphs are byte-identical and a PASS here would certify that nothing was indexed."
		r.logf("parity: %-44s FAIL    %s", res.ID, firstLine(res.Detail))
		return
	}

	switch {
	case fullASHA != fullBSHA:
		res.Verdict = parityreport.VerdictFail
		res.Detail = "PARSE DETERMINISM FAILED: two independent full passes over the IDENTICAL final tree " +
			"serialised differently (A " + short(fullASHA) + ", B " + short(fullBSHA) + "). This is the " +
			"row's own property and it is a product finding, not a harness fault."
	case incSHA != fullASHA:
		res.Verdict = parityreport.VerdictFail
		res.Detail = "INTRA-FILE PARITY FAILED: the incrementally-updated graph (" + short(incSHA) +
			") is not byte-identical to a fresh full index of the same final tree (" + short(fullASHA) +
			"). Parse determinism HELD across the two full passes, so the divergence is in the " +
			"incremental path, not in the parser."
	default:
		res.Verdict = parityreport.VerdictPass
		res.Detail = strings.TrimSpace(res.Detail + " Two independent full passes over the identical final tree " +
			"serialised to the same bytes (" + short(fullASHA) + "), and the incrementally-updated graph is " +
			"byte-identical to them.")
	}
	r.logf("parity: %-44s %-7s %s", res.ID, res.Verdict, entry.Name)
}

// parseDetVacuity returns "" when the row is non-vacuous, and the reason it is
// not otherwise.
//
// For a ParseOnly language the check is the PARSE BOUNDARY, read back out of
// the materialized tree; for every other language it is the graph. The two are
// not interchangeable and the branch is explicit rather than a fallback, so no
// future edit can quietly demote a graph language to the weaker witness.
func parseDetVacuity(lang ParseDetLanguage, g graphPayload, mut *Mutation, repoDir string) string {
	if lang.ParseOnly {
		return parseDetParseBoundary(mut, repoDir)
	}
	if len(g.Nodes) == 0 {
		return "the measured graph carries no nodes at all"
	}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kinds[n.Kind]++
	}
	found := false
	for _, k := range lang.MinSymbolKinds {
		if kinds[k] > 0 {
			found = true
			break
		}
	}
	if !found {
		return "the measured graph carries none of " + lang.Name + "'s expected node kinds (" +
			strings.Join(lang.MinSymbolKinds, ", ") + "); kinds present: " + renderKinds(kinds)
	}
	// The edited path must be represented, or the row would pass on a graph
	// that never saw the class's own change.
	for _, op := range mut.Ops {
		if op.Kind == opDelete {
			continue
		}
		for _, n := range g.Nodes {
			if n.SourcePath == op.Path || strings.HasSuffix(n.SourcePath, "/"+op.Path) {
				return ""
			}
		}
		return "no node of the measured graph is anchored in " + op.Path +
			", the file this class actually edited"
	}
	return ""
}

// parseDetParseBoundary is the ParseOnly witness: after apply, every write op's
// bytes are on disk exactly as the mutation planned them, and every delete op's
// path is gone. An apply that did nothing fails here.
//
// It reads the TREE, never the harness's own copy of the intended bytes as if
// it were evidence: the comparison is between what the planner said it would
// write and what the filesystem now holds.
func parseDetParseBoundary(mut *Mutation, repoDir string) string {
	for _, op := range mut.Ops {
		abs := filepath.Join(repoDir, filepath.FromSlash(op.Path))
		switch op.Kind {
		case opDelete:
			if _, err := os.Stat(abs); err == nil {
				return "the parse boundary says the delete did not happen: " + op.Path + " is still in the tree"
			}
		case opWrite:
			got, err := os.ReadFile(abs)
			if err != nil {
				return "the parse boundary cannot read " + op.Path + " back out of the tree: " + err.Error()
			}
			if string(got) != string(op.Data) {
				return "the parse boundary says the write did not land: " + op.Path +
					" holds bytes that are not the ones this class planned"
			}
		}
	}
	return ""
}

func renderKinds(kinds map[string]int) string {
	keys := make([]string, 0, len(kinds))
	for k := range kinds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, kinds[k]))
	}
	return strings.Join(parts, " ")
}

// selectParseDetRepo walks the candidate pool for the smallest tree that
// exhibits the class's structure.
func (r *Runner) selectParseDetRepo(ctx context.Context, lang ParseDetLanguage, spec ParseDetSpec,
	candidates []corpus.Entry) (corpus.Entry, *ParseDetModel, *Mutation, string, error) {

	if len(candidates) == 0 {
		return corpus.Entry{}, nil, nil, parseDetNoPinReason(lang.Name, r.MaxTier, r.AllowLocal), nil
	}
	var tried []string
	for _, e := range candidates {
		dir, model, err := r.materializeParseDet(ctx, e, lang)
		if err != nil {
			if isPinFailure(err) {
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
		why := fmt.Sprintf("smallest in-cap %s entry whose real source exhibits this class (tier %d, %d source files)",
			lang.Name, e.Tier, len(model.Files))
		if len(tried) > 0 {
			why += "; not exhibited by " + strings.Join(tried, ", ")
		}
		return e, model, mut, why, nil
	}
	detail := fmt.Sprintf("no %s entry within -max-tier %d exhibits the structure this class needs",
		lang.Name, r.MaxTier)
	if len(tried) > 0 {
		detail += " (examined: " + strings.Join(tried, ", ") + ")"
	}
	return corpus.Entry{}, nil, nil, detail, nil
}

// parseDetNoPinReason is the SW-200-specific abstention sentence. It names the
// mechanism rather than saying "blocked": which check refuses, on what evidence,
// and what would lift it.
func parseDetNoPinReason(lang string, maxTier int, allowLocal bool) string {
	s := "NO CORPUS ENTRY: corpus/manifest.json declares no " + lang + " entry at tier <= " +
		fmt.Sprintf("%d", maxTier)
	if !allowLocal {
		s += " that is a pinned clone (local-path fixtures are refused by default: a matrix row that ran " +
			"on a fixture is not real-repository evidence)"
	}
	return s + ". The G4 row therefore stays UNKNOWN with this reason named; it is NOT re-pointed at " +
		"another language's pin and no fixture is substituted. What lifts it: SW-201's v3 pins for the " +
		"intra/parse residual landing on main (they exist only on branch " +
		"sw-201-w5o-corpus-pins-v3-intra-parse, which is not merged), and — for json specifically — a pin " +
		"existing at all, since SW-201 filed json as an honest no_pin abstention."
}

// materializeParseDet clones (or copies) an entry and builds the language model.
//
// EVERY entry is materialized INTO THE WORKDIR, including local Path entries,
// for the reason materialize() states: the planners write real edits to real
// files, and a Path entry points at a checked-in fixture inside this repository.
func (r *Runner) materializeParseDet(ctx context.Context, e corpus.Entry, lang ParseDetLanguage) (string, *ParseDetModel, error) {
	if m, ok := r.parseDetModels[e.Name]; ok {
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
		// FAIL CLOSED on a pin mismatch, exactly as materialize() does: a
		// re-pointed upstream tag silently changes what the matrix measured.
		if herr != nil {
			return "", nil, fmt.Errorf("parity: %s%s: sha pinned but HEAD unreadable: %v", pinFailureMarker, e.Name, herr)
		}
		if !shaMatches(e.SHA, head) {
			return "", nil, fmt.Errorf("parity: %s%s: HEAD %s does not match pinned sha %s (ref %q moved?)",
				pinFailureMarker, e.Name, head, e.SHA, e.Ref)
		}
	}
	model, err := discoverParseDet(dir, lang.Name, lang.Exts)
	if err != nil {
		return "", nil, err
	}
	if len(model.Files) == 0 {
		return "", nil, fmt.Errorf("parity: %s declares language %s but its tree holds no %s file",
			e.Name, lang.Name, strings.Join(lang.Exts, "/"))
	}
	r.parseDetModels[e.Name] = model
	r.dirs[e.Name] = dir
	return dir, model, nil
}

// graphiAxisParseDet runs the built binary with the axis's profile, having
// CLEARED the profile environment variable first so an inherited value cannot
// silently change what was measured.
func (r *Runner) graphiAxisParseDet(ctx context.Context, cwd string, axis ParseDetAxis, args ...string) ([]byte, error) {
	env := []string{profile.EnvName + "="}
	full := args
	if axis.Profile != "" {
		full = append(append([]string{}, args...), "-profile", axis.Profile)
	}
	return r.graphiEnv(ctx, cwd, env, full...)
}

// ---------------------------------------------------------------------------
// The six generic planners
//
// They are generic ON PURPOSE. Six languages times six classes is thirty-six
// planners, and thirty-six hand-written ones would be thirty-six places for the
// edit shape to drift apart — at which point "css_modify_file" and
// "toml_modify_file" would no longer be the same class measured on two
// languages. The LANGUAGE-SPECIFIC half is isolated in ParseDetShape, which is
// the only thing each internal/parity/<lang>.go supplies.
// ---------------------------------------------------------------------------

// ParseDetShape is the language-specific half of the generic planners. Every
// function returns errNoTarget when the source it is handed does not exhibit
// the structure it needs; that is the walk-on signal, never a silent no-op.
type ParseDetShape struct {
	// Ext is the extension a synthesised file gets.
	Ext string
	// NewFile renders a complete, valid source file that mints at least one
	// symbol of this language.
	NewFile func(seed string) []byte
	// Append renders src with ONE new top-level construct appended and every
	// existing construct's identity untouched.
	Append func(src []byte, seed string) ([]byte, error)
	// Rename renders src with the LAST top-level construct's NAME changed to a
	// seed-derived one, and every other construct untouched. It is a
	// delete-plus-add inside one file wherever the name IS the symbol identity.
	Rename func(src []byte, seed string) ([]byte, error)
	// Reorder renders src with its top-level constructs permuted and NO
	// textual change to any construct body — the sharpest test of the
	// canonical-ordering discipline.
	Reorder func(src []byte) ([]byte, error)
}

const parseDetSeed = "graphi_parity_w5n"

// parseDetAddedDir is the new directory the add_file class creates. It is
// namespaced so it can never collide with a real path in a real pin, and it is
// the same on every language so a reader of two reports can compare them.
const parseDetAddedDir = "graphi-parity-w5n-added"

// largestFile is the deterministic edit target for the classes that need "a
// file with something in it". Largest-then-lexicographic, never "the first the
// walk yielded": a planner whose target depended on walk order would make the
// ROW non-deterministic and the harness would then report its own instability
// as a product finding.
func largestFile(m *ParseDetModel) (ParseDetFile, error) {
	best := ParseDetFile{}
	for _, f := range m.Files {
		if len(f.Data) > len(best.Data) || (len(f.Data) == len(best.Data) && best.Rel != "" && f.Rel < best.Rel) {
			best = f
		}
	}
	if best.Rel == "" {
		return ParseDetFile{}, errNoTarget
	}
	return best, nil
}

// smallestFile is the delete target: deleting the smallest file keeps the most
// of the tree indexed, so the row's "the sibling's symbols survive" half has
// something to survive on.
func smallestFile(m *ParseDetModel) (ParseDetFile, error) {
	if len(m.Files) < 2 {
		return ParseDetFile{}, errNoTarget
	}
	best := m.Files[0]
	for _, f := range m.Files[1:] {
		if len(f.Data) < len(best.Data) || (len(f.Data) == len(best.Data) && f.Rel < best.Rel) {
			best = f
		}
	}
	return best, nil
}

func planParseDetAddFile(sh ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		rel := parseDetAddedDir + "/added" + sh.Ext
		return &Mutation{
			Desc: "add a new " + m.Lang + " source file at " + rel + " in a NEW directory (the pure add path)",
			Ops:  []FileOp{{Kind: opWrite, Path: rel, Data: sh.NewFile(parseDetSeed)}},
		}, nil
	}
}

func planParseDetModifyFile(sh ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		f, err := largestFile(m)
		if err != nil {
			return nil, err
		}
		out, err := sh.Append(f.Data, parseDetSeed)
		if err != nil {
			return nil, err
		}
		return &Mutation{
			Desc: "rewrite " + f.Rel + " in place: append one new top-level construct while every " +
				"existing construct keeps its identity",
			Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: out}},
		}, nil
	}
}

func planParseDetDeleteFile(_ ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		f, err := smallestFile(m)
		if err != nil {
			return nil, err
		}
		return &Mutation{
			Desc: "delete " + f.Rel + ", so the per-file stale-node purge runs over its symbols while " +
				"every sibling file's symbols must survive",
			Ops: []FileOp{{Kind: opDelete, Path: f.Rel}},
		}, nil
	}
}

func planParseDetRename(sh ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		if sh.Rename == nil {
			return nil, errNoTarget
		}
		for _, f := range m.Files {
			out, err := sh.Rename(f.Data, parseDetSeed)
			if err == errNoTarget {
				continue
			}
			if err != nil {
				return nil, err
			}
			return &Mutation{
				Desc: "rename the last top-level construct of " + f.Rel + " in place; the construct's name " +
					"IS the symbol identity, so this is a delete-plus-add inside one file",
				Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: out}},
			}, nil
		}
		return nil, errNoTarget
	}
}

func planParseDetReorder(sh ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		if sh.Reorder == nil {
			return nil, errNoTarget
		}
		for _, f := range m.Files {
			out, err := sh.Reorder(f.Data)
			if err == errNoTarget {
				continue
			}
			if err != nil {
				return nil, err
			}
			return &Mutation{
				Desc: "permute the top-level constructs of " + f.Rel + " with NO textual change to any " +
					"construct body: the symbol SET is unchanged and only source order and line numbers move",
				Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: out}},
			}, nil
		}
		return nil, errNoTarget
	}
}

func planParseDetReparseIdentical(_ ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		f, err := largestFile(m)
		if err != nil {
			return nil, err
		}
		// The bytes are IDENTICAL by construction: the drift scanner sees an
		// empty drift set and Reconcile short-circuits, so the incremental
		// graph must equal the full graph over unchanged bytes. The row's force
		// comes from the two independent full passes, not from the edit.
		return &Mutation{
			Desc: "rewrite " + f.Rel + " with BYTE-IDENTICAL content (mtime moves, bytes do not)",
			Ops:  []FileOp{{Kind: opWrite, Path: f.Rel, Data: append([]byte(nil), f.Data...)}},
		}, nil
	}
}

// parseDetStandardSpecs builds the five classes every one of the six languages
// declares under the same meaning, given the language's id prefix and the two
// ids that differ per language (the rename-shaped and the reorder-shaped one).
func parseDetStandardSpecs(prefix string, renameID, reorderID string, sh ParseDetShape,
	renameNote, reorderNote string) []ParseDetSpec {
	return []ParseDetSpec{
		{ID: prefix + "_add_file", Plan: planParseDetAddFile(sh),
			Note: "A source file of this language arrives in a NEW directory: the pure add path."},
		{ID: prefix + "_modify_file", Plan: planParseDetModifyFile(sh),
			Note: "An indexed file is rewritten in place; existing constructs keep identity."},
		{ID: prefix + "_delete_file", Plan: planParseDetDeleteFile(sh),
			Note: "A delete drives the per-file stale-node purge — the PARITY-001 defect class was a " +
				"purge-ordering defect in origin, so a stale node surviving a delete is what this row catches."},
		{ID: renameID, Plan: planParseDetRename(sh), Note: renameNote},
		{ID: reorderID, Plan: planParseDetReorder(sh), Note: reorderNote},
		{ID: prefix + "_reparse_identical_bytes", Plan: planParseDetReparseIdentical(sh),
			Note: "The direct same-bytes-same-AST row. The edit is deliberately semantics-preserving; the " +
				"row's force comes from the two independent full passes over the identical tree."},
	}
}

// ---------------------------------------------------------------------------
// Text splitters shared by the language shapes
// ---------------------------------------------------------------------------

// splitTopLevelBraceBlocks splits src into the segments delimited by top-level
// `{`…`}` pairs, returning each block INCLUDING its leading inter-block text, so
// re-joining the slice reproduces src exactly when the order is unchanged.
//
// It is quote-aware and comment-blind by design: it tracks `"`-delimited
// strings with backslash escapes (which is what makes it safe on HCL and JSON
// values containing braces) and does not attempt to understand comments, which
// is why every shape that uses it treats an unbalanced result as errNoTarget
// rather than guessing.
func splitTopLevelBraceBlocks(src []byte) ([]string, error) {
	var blocks []string
	depth, start := 0, 0
	inStr, esc := false, false
	closedAny := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, errNoTarget
			}
			if depth == 0 {
				end := i + 1
				// Absorb the rest of the line (a trailing newline belongs to
				// the block that just closed, or re-joining would fuse two
				// blocks onto one line).
				for end < len(src) && src[end] != '\n' {
					end++
				}
				if end < len(src) {
					end++
				}
				blocks = append(blocks, string(src[start:end]))
				start = end
				i = end - 1
				closedAny = true
			}
		}
	}
	if depth != 0 || inStr || !closedAny {
		return nil, errNoTarget
	}
	if start < len(src) {
		blocks = append(blocks, string(src[start:]))
	}
	return blocks, nil
}

// splitLineSections splits src into sections that each begin at a line for
// which isHeader is true. Everything before the first header is the PREAMBLE
// and is returned separately, because a preamble that moved would be a textual
// change to something that is not a top-level construct.
func splitLineSections(src []byte, isHeader func(line string) bool) (preamble string, sections []string) {
	lines := strings.SplitAfter(string(src), "\n")
	cur := ""
	started := false
	for _, ln := range lines {
		if isHeader(strings.TrimRight(ln, "\r\n")) {
			if started {
				sections = append(sections, cur)
			} else {
				preamble = cur
				started = true
			}
			cur = ln
			continue
		}
		cur += ln
	}
	if started {
		sections = append(sections, cur)
	} else {
		preamble = cur
	}
	return preamble, sections
}

// rotateSections moves the LAST section to the front. Rotation rather than a
// shuffle: a permutation must be deterministic, or two dispatches would permute
// differently and the harness would report its own randomness as a product
// finding.
func rotateSections(sections []string) []string {
	if len(sections) < 2 {
		return nil
	}
	out := make([]string, 0, len(sections))
	out = append(out, sections[len(sections)-1])
	out = append(out, sections[:len(sections)-1]...)
	return out
}
