package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/eval/retrieval"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// init registers two test-only embedder schemes so tests can drive a real
// selector path through the real harness without a loopback Ollama or a
// pinned static artifact: `field-parity-mock` (SW-272's 2x3 control) and
// `mock` (SW-269's AC-5 fingerprint test). Mirrors
// internal/eval/retrieval/runner_test.go's test-only init: production wiring
// goes through cmd/retrieval-eval/main.go's blank imports of the ollama and
// static subpackages (their init()s call embed.RegisterScheme); this init
// just layers the two extra schemes on top.
func init() {
	embed.RegisterScheme("field-parity-mock", func(string) (embed.Embedder, error) {
		return embed.NewMockEmbedder(8), nil
	})
	embed.RegisterScheme("mock", func(_ string) (embed.Embedder, error) {
		return embed.NewMockEmbedder(8), nil
	})
}

// repoRoot is the repository root; the binary's relative defaults (the
// fixture path, corpus/manifest.json) are anchored there.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func chdirRoot(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

const fixtureDataset = "internal/eval/retrieval/testdata/datasets/fixture-v1.json"

func TestRetrievalEval_SetupTokenizerFromLocalArtifact(t *testing.T) {
	src := filepath.Join(repoRoot(t), "internal", "eval", "tokenizer", "testdata", "artifact")
	if _, err := os.Stat(filepath.Join(src, evaltokenizer.PinnedVocabularyFile)); err != nil {
		t.Fatalf("checked-in real tokenizer artifact is absent: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "tokenizer")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-setup-tokenizer", "-tokenizer-local", src, "-tokenizer-dir", dest}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("setup-tokenizer exit %d:\n%s", code, stderr.String())
	}
	if _, err := evaltokenizer.Load(dest); err != nil {
		t.Fatalf("installed tokenizer does not load: %v", err)
	}
	for _, want := range []string{evaltokenizer.TokenizerID, evaltokenizer.PinnedVocabularySHA256, dest} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("setup output does not contain %q: %s", want, stdout.String())
		}
	}
}

// AC-5 / AC-10 end to end: the dispatch entry point runs the fixture through
// the real seams, exports raw samples, and -aggregate reproduces them.
func TestRetrievalEval_FixtureRunExportAndAggregate(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	raw := filepath.Join(dir, "raw")
	var stderr bytes.Buffer

	code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
		"-out", out, "-export-raw", raw, "-runner-class", "test", "-repeats", "2", "-date", "2026-08-30"}, &bytes.Buffer{}, &stderr)
	if code != exitOK {
		t.Fatalf("run exit %d\n%s", code, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report retrieval.Report
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Reproducible.Repo.Name != FixtureRepoName || report.Reproducible.RunnerClass != "test" || len(report.Reproducible.Baselines) != 7 {
		t.Errorf("report header = %+v", report.Reproducible)
	}
	if !strings.Contains(stderr.String(), "semantic_name_only") || !strings.Contains(stderr.String(), "unavailable") {
		t.Errorf("summary does not report the unavailable baseline:\n%s", stderr.String())
	}

	t.Run("aggregate reproduces the untouched run", func(t *testing.T) {
		var w bytes.Buffer
		if code := run([]string{"-aggregate", raw}, &bytes.Buffer{}, &w); code != retrieval.ExitReproduced {
			t.Fatalf("aggregate exit %d\n%s", code, w.String())
		}
		if !strings.Contains(w.String(), "PASS") {
			t.Errorf("aggregate output:\n%s", w.String())
		}
		if _, err := os.Stat(filepath.Join(raw, AggregateFile)); err != nil {
			t.Errorf("aggregate.json not written: %v", err)
		}
	})

	t.Run("aggregate exits 1 on a nudged report", func(t *testing.T) {
		nudged := filepath.Join(t.TempDir(), "raw")
		if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
			"-out", filepath.Join(filepath.Dir(nudged), "r.json"), "-export-raw", nudged, "-repeats", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
			t.Fatal("second run failed")
		}
		ib, _ := os.ReadFile(filepath.Join(nudged, retrieval.RunIndexFile))
		var idx retrieval.RunIndex
		_ = json.Unmarshal(ib, &idx)
		p := filepath.Join(nudged, idx.Report)
		rb, _ := os.ReadFile(p)
		var r retrieval.Report
		_ = json.Unmarshal(rb, &r)
		r.Reproducible.Baselines[0].Overall.Metrics[retrieval.MetricRecall10] += 0.01
		nb, _ := retrieval.MarshalReport(&r)
		_ = os.WriteFile(p, nb, 0o644)
		idx.ReportSHA256 = retrieval.SHA256Hex(nb)
		ob, _ := json.MarshalIndent(idx, "", "  ")
		_ = os.WriteFile(filepath.Join(nudged, retrieval.RunIndexFile), ob, 0o644)

		var w bytes.Buffer
		if code := run([]string{"-aggregate", nudged}, &bytes.Buffer{}, &w); code != retrieval.ExitDiscrepancy {
			t.Errorf("aggregate exit %d, want %d\n%s", code, retrieval.ExitDiscrepancy, w.String())
		}
	})

	t.Run("aggregate exits 1 on a query removed coherently from dataset, report and raw", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "raw")
		if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
			"-out", filepath.Join(filepath.Dir(dir), "r.json"), "-export-raw", dir, "-repeats", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
			t.Fatal("export run failed")
		}
		removeQueryEverywhere(t, dir, "fx-10")
		var w bytes.Buffer
		if code := run([]string{"-aggregate", dir}, &bytes.Buffer{}, &w); code != retrieval.ExitDiscrepancy {
			t.Errorf("aggregate exit %d, want %d\n%s", code, retrieval.ExitDiscrepancy, w.String())
		}
		if !strings.Contains(w.String(), "run dataset: published") {
			t.Errorf("the dataset citation must be what catches a coordinated removal:\n%s", w.String())
		}
	})

	t.Run("derive writes targets and budgets citing the report", func(t *testing.T) {
		targets := filepath.Join(dir, "targets.json")
		budgets := filepath.Join(dir, "budgets.json")
		var w bytes.Buffer
		if code := run([]string{"-derive", "-targets-report", out, "-budget-small", out,
			"-targets-out", targets, "-budgets-out", budgets, "-date", "2026-08-30"}, &bytes.Buffer{}, &w); code != exitOK {
			t.Fatalf("derive exit %d\n%s", code, w.String())
		}
		tb, err := os.ReadFile(targets)
		if err != nil {
			t.Fatal(err)
		}
		var tg retrieval.Targets
		if err := json.Unmarshal(tb, &tg); err != nil {
			t.Fatal(err)
		}
		if tg.DerivedFrom.Report != out || tg.DerivedFrom.SHA256 != retrieval.SHA256Hex(b) || tg.ImmutableUntil != retrieval.ImmutableUntil {
			t.Errorf("targets derived_from = %+v", tg.DerivedFrom)
		}
		bb, err := os.ReadFile(budgets)
		if err != nil {
			t.Fatal(err)
		}
		var bg retrieval.Budgets
		if err := json.Unmarshal(bb, &bg); err != nil {
			t.Fatal(err)
		}
		if bg.Fixtures[retrieval.FixtureSmall].Status != retrieval.StatusMeasured || bg.Fixtures[retrieval.FixtureLarge].Status != retrieval.StatusUnknown {
			t.Errorf("budgets = %+v", bg.Fixtures)
		}
	})
}

func TestRetrievalEval_FieldParitySelectsOnlyDevNLBehaviour(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	runDir := filepath.Join(dir, "run")
	var stderr bytes.Buffer
	code := run([]string{
		"-field-parity", "-manifest", "corpus/manifest.json", "-repo", FixtureRepoName,
		"-dataset", fixtureDataset, "-out", out, "-export-raw", runDir,
		"-runner-class", "test", "-repeats", "1", "-date", "2026-09-03",
		"-embedder", "field-parity-mock",
	}, &bytes.Buffer{}, &stderr)
	if code != exitOK {
		t.Fatalf("field-parity run exit %d\n%s", code, stderr.String())
	}
	loaded, err := retrieval.ReadRunDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Report.Reproducible.Dataset.Queries != 1 || loaded.Report.Reproducible.Dataset.Dev != 1 || loaded.Report.Reproducible.Dataset.Holdout != 0 {
		t.Errorf("field-parity dataset citation = %+v", loaded.Report.Reproducible.Dataset)
	}
	if got := loaded.Report.Reproducible.RelevantMinGrade; got != retrieval.GradeMax {
		t.Errorf("field-parity relevant grade = %d, want exact grade %d", got, retrieval.GradeMax)
	}
	var got []retrieval.Baseline
	for _, baseline := range loaded.Report.Reproducible.Baselines {
		got = append(got, baseline.Name)
	}
	if !reflect.DeepEqual(got, retrieval.FieldParityBaselines) {
		t.Errorf("field-parity baselines = %v, want %v", got, retrieval.FieldParityBaselines)
	}
	agg := retrieval.Reproduce(loaded)
	if agg.ExitCode() != retrieval.ExitReproduced {
		t.Fatalf("field-parity aggregate = %s, discrepancies=%v, unknown=%d", agg.Status, agg.Discrepancies, agg.Unknown)
	}

	stderr.Reset()
	code = run([]string{
		"-field-parity", "-baseline", "lexical", "-repo", FixtureRepoName,
		"-dataset", fixtureDataset, "-out", filepath.Join(dir, "invalid.json"),
	}, &bytes.Buffer{}, &stderr)
	if code != exitUsage || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Errorf("field-parity plus baseline exit %d, stderr %q", code, stderr.String())
	}
}

// readJSON / writeJSON are the run-directory file helpers the tamper below
// needs; every write goes through the same stable encoder the harness uses.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) []byte {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

// removeQueryEverywhere is the coordinated structural tamper through the
// public API: one query dropped from the dataset copy, every raw series and
// every ok baseline, every statistic and performance block recomputed with
// the production functions, every digest re-stamped in run.json — so that
// only the dataset citation the report still carries can catch it.
func removeQueryEverywhere(t *testing.T, dir, id string) {
	t.Helper()
	var idx retrieval.RunIndex
	readJSON(t, filepath.Join(dir, retrieval.RunIndexFile), &idx)

	var ds retrieval.Dataset
	readJSON(t, filepath.Join(dir, idx.Dataset), &ds)
	kept := ds.Queries[:0]
	for _, q := range ds.Queries {
		if q.ID != id {
			kept = append(kept, q)
		}
	}
	ds.Queries = kept
	idx.DatasetSHA256 = retrieval.SHA256Hex(writeJSON(t, filepath.Join(dir, idx.Dataset), &ds))

	latency := map[retrieval.Baseline]retrieval.RawLatencySet{}
	for i := range idx.Raw {
		ref := &idx.Raw[i]
		p := filepath.Join(dir, filepath.FromSlash(ref.File))
		switch ref.Series {
		case retrieval.RawSeriesHits:
			var set retrieval.RawHitSet
			readJSON(t, p, &set)
			set.Samples = 0
			qs := set.Queries[:0]
			for _, q := range set.Queries {
				if q.ID != id {
					qs = append(qs, q)
					set.Samples += len(q.Hits)
				}
			}
			set.Queries = qs
			ref.Digest, ref.Samples = retrieval.SHA256Hex(writeJSON(t, p, &set)), set.Samples
		case retrieval.RawSeriesLatency:
			var set retrieval.RawLatencySet
			readJSON(t, p, &set)
			set.Samples = 0
			qs := set.Queries[:0]
			for _, q := range set.Queries {
				if q.ID != id {
					qs = append(qs, q)
					set.Samples += len(q.SamplesUS)
				}
			}
			set.Queries = qs
			ref.Digest, ref.Samples = retrieval.SHA256Hex(writeJSON(t, p, &set)), set.Samples
			latency[ref.Baseline] = set
		}
	}

	var r retrieval.Report
	readJSON(t, filepath.Join(dir, idx.Report), &r)
	for i := range r.Reproducible.Baselines {
		b := &r.Reproducible.Baselines[i]
		if b.Status != retrieval.BaselineStatusOK {
			continue
		}
		qs := b.Queries[:0]
		for _, q := range b.Queries {
			if q.ID != id {
				qs = append(qs, q)
			}
		}
		b.Queries = qs
		b.Overall, b.Strata, b.Splits = retrieval.AggregateAll(b.Queries, r.Reproducible.TokenBudgets)
	}
	for i := range r.Performance {
		r.Performance[i] = retrieval.PerformanceFromRaw(r.Performance[i].Baseline, latency[r.Performance[i].Baseline])
	}
	rb, err := retrieval.MarshalReport(&r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, idx.Report), rb, 0o644); err != nil {
		t.Fatal(err)
	}
	idx.ReportSHA256 = retrieval.SHA256Hex(rb)
	writeJSON(t, filepath.Join(dir, retrieval.RunIndexFile), &idx)
}

func TestRetrievalEval_UsageErrors(t *testing.T) {
	chdirRoot(t)
	cases := []struct {
		name string
		args []string
		want int
		msg  string
	}{
		{"no repo", []string{"-dataset", fixtureDataset, "-out", "x"}, exitUsage, "-repo"},
		{"unknown baseline", []string{"-repo", FixtureRepoName, "-dataset", fixtureDataset, "-out", "x", "-baseline", "bm42"}, exitUsage, "bm42"},
		{"dataset for another repo", []string{"-repo", "cobra", "-dataset", fixtureDataset, "-out", "x"}, exitUsage, "judged against"},
		{"repo not in manifest", []string{"-repo", "nope", "-dataset", fixtureDataset, "-out", "x"}, exitUsage, "judged against"},
		{"aggregate over a missing dir", []string{"-aggregate", filepath.Join(t.TempDir(), "missing")}, retrieval.ExitUsage, "run.json"},
		{"derive without outputs", []string{"-derive"}, exitUsage, "-targets-out"},
		{"budgets without a report", []string{"-derive", "-budgets-out", filepath.Join(t.TempDir(), "b.json")}, exitUsage, "-budget-small"},
		{"unknown flag", []string{"-frobnicate"}, exitUsage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w bytes.Buffer
			if code := run(tc.args, &bytes.Buffer{}, &w); code != tc.want {
				t.Errorf("exit %d, want %d\n%s", code, tc.want, w.String())
			}
			if tc.msg != "" && !strings.Contains(w.String(), tc.msg) {
				t.Errorf("output does not mention %q:\n%s", tc.msg, w.String())
			}
		})
	}
}

func TestRetrievalEval_CheckClaimRejectsUnsafeSentence(t *testing.T) {
	unsafe := "Semantic search beats BM25 and the CoIR lexical baseline across Go repositories."
	var stderr bytes.Buffer
	if code := run([]string{"-check-claim", unsafe}, &bytes.Buffer{}, &stderr); code != exitError {
		t.Fatalf("check-claim exit %d, want %d\n%s", code, exitError, stderr.String())
	}
	want := "retrieval-eval: retrieval claim rejected: " +
		"claim_shape (\"not the frozen descriptive template\"); comparator_scope (\"BM25\"); comparator_scope (\"CoIR\"); comparator_scope (\"lexical baseline\"); " +
		"population_scope (\"across Go repositories\"); population_scope (\"Go repositories\"); " +
		"population_scope (\"missing frozen Cobra dataset scope\"); population_scope (\"missing required pinned-Cobra limitation\"); " +
		"semantic_superiority (\"Semantic\")\n"
	if stderr.String() != want {
		t.Fatalf("check-claim output:\n%s\nwant exactly:\n%s", stderr.String(), want)
	}
}

func TestRetrievalEval_CheckClaimAcceptsOnlyNarrowDescriptiveScope(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"-check-claim", retrieval.ClaimTemplateExample}, &bytes.Buffer{}, &stderr); code != exitOK {
		t.Fatalf("check-claim exit %d, want %d\n%s", code, exitOK, stderr.String())
	}
	want := "retrieval-eval: claim accepted by " + retrieval.MeasurementContractVersion + "\n"
	if stderr.String() != want {
		t.Fatalf("check-claim output = %q, want %q", stderr.String(), want)
	}
}

// TestRetrievalEval_InvalidEmbedderSelectorExitsNonZeroAndWritesNoReport
// is the regression the SW-263 reviewer required (and SW-269 explicitly
// hardens). Three checks the SW-269 AC-1 contract MUST satisfy at once:
//
//   - exit non-zero (the harness cannot silently downgrade);
//   - stderr names the selector and the construction reason;
//   - stderr enumerates the three accepted selector forms
//     (`ollama:host:port`, `static:<model>@<revision>`, `onnx:<model>`)
//     so a user following the error lands on a fix path rather than
//     guessing;
//   - no report file is written;
//   - no publishable artefact lands in the export-raw directory.
//
// The two selector forms SW-269 tests:
//
//  1. `ollama:nomic-embed-text` — the historic wrong form the
//     pre-SW-263 help advertised (the segment after the colon is the
//     endpoint, never the model name). The loopback guard rejects it
//     as a non-IP host. The pre-fix harness silently downgraded the
//     failure to "no embedder; semantic baselines: unavailable" and
//     exited zero; that shape is what this test exists to keep dead.
//
//  2. `ollama:1.2.3.4:11434` — a non-loopback host; refused for a
//     different reason but with the same fail-closed posture.
//
// The omitted-`-embedder` path is intentionally NOT tested here: it is the
// "intentional unavailable baselines" mode and exits 0 by contract (see
// TestRetrievalEval_FixtureRunExportAndAggregate, which uses neither flag).
func TestRetrievalEval_InvalidEmbedderSelectorExitsNonZeroAndWritesNoReport(t *testing.T) {
	chdirRoot(t)
	cases := []struct {
		name     string
		selector string
		wantMsg  string
	}{
		{
			name:     "ollama:nomic-embed-text (advertised-but-invalid form)",
			selector: "ollama:nomic-embed-text",
			wantMsg:  "non-loopback",
		},
		{
			name:     "ollama on a non-loopback host",
			selector: "ollama:1.2.3.4:11434",
			wantMsg:  "non-loopback",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "report.json")
			raw := filepath.Join(dir, "raw")

			var stderr bytes.Buffer
			code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
				"-out", out, "-export-raw", raw, "-runner-class", "test", "-repeats", "1", "-date", "2026-08-30",
				"-embedder", tc.selector}, &bytes.Buffer{}, &stderr)
			if code != exitError {
				t.Errorf("run exit %d, want %d (the reviewer ruled that a non-empty -embedder that fails to construct must exit non-zero):\n%s",
					code, exitError, stderr.String())
			}
			errOut := stderr.String()
			if !strings.Contains(errOut, tc.wantMsg) {
				t.Errorf("stderr does not mention %q:\n%s", tc.wantMsg, errOut)
			}
			if !strings.Contains(errOut, tc.selector) {
				t.Errorf("stderr does not name the failing selector %q:\n%s", tc.selector, errOut)
			}
			// SW-269 AC-1: the error message must enumerate the accepted
			// selector forms so a copy-paste-following user lands on a fix
			// path. None of the three forms has the failing selector in
			// its exact text, but each is named in the printed guidance.
			for _, want := range []string{"`ollama:host:port`", "`static:<model>@<revision>`", "`onnx:<model>`"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr does not enumerate the accepted form %s:\n%s", want, errOut)
				}
			}
			if !strings.Contains(errOut, "SW-269") {
				t.Errorf("stderr does not mention the SW-269 fail-loudly marker:\n%s", errOut)
			}
			// Publishable report MUST not exist: the harness failed before
			// the report was written. -export-raw may exist (as a directory)
			// but it MUST be empty — the inverted case is exactly the defect.
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("report file %s exists; expected it NOT to be written on a failed embedder:\n%s", out, errOut)
			}
			if fi, err := os.Stat(raw); err == nil {
				entries, _ := os.ReadDir(raw)
				if fi.IsDir() && len(entries) > 0 {
					t.Errorf("export-raw directory %s is non-empty after a failed embedder (%d entries); expected no publishable artifact:\n%v",
						raw, len(entries), entries)
				}
			}
		})
	}
}

// TestRetrievalEval_ReportStampsEmbedderSpecForUnconfiguredRun (AC-5)
// pins the empty-selector leg of the SW-269 contract: a successful run
// with no `-embedder` writes a report whose `reproducible.embedder_spec`
// is the explicit `lexical_only` marker — NOT a fingerprint, NOT absent.
// Reading the report under the /3 shape would otherwise silently
// downgrade the run to ambiguous, the exact defect the SW-269 contract
// removes.
func TestRetrievalEval_ReportStampsEmbedderSpecForUnconfiguredRun(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	var stderr bytes.Buffer
	if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
		"-out", out, "-runner-class", "test", "-repeats", "1", "-date", "2026-08-30"}, &bytes.Buffer{}, &stderr); code != exitOK {
		t.Fatalf("unconfigured run exit %d, want 0:\n%s", code, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report retrieval.Report
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	spec := report.Reproducible.EmbedderSpec
	if spec == nil {
		t.Fatalf("report %s has no embedder_spec (a /3 unconfigured run MUST carry the lexical_only marker)", out)
	}
	if !spec.LexicalOnly || spec.Fingerprint != "" {
		t.Errorf("unconfigured run embedder_spec = %+v, want LexicalOnly=true and empty Fingerprint", spec)
	}
	if err := retrieval.CheckEmbedderSpec(&report); err != nil {
		t.Errorf("CheckEmbedderSpec(lexical-only report): %v", err)
	}
}

// TestRetrievalEval_ReportStampsEmbedderSpecForConfiguredRun (AC-5)
// pins the configured-embedder leg of SW-269: a successful run with
// `-embedder mock` writes a report whose `reproducible.embedder_spec`
// is a non-empty fingerprint, NOT a lexical-only marker, NOT absent.
// Reading the report under the /3 shape would otherwise silently
// downgrade the run to ambiguous, the exact defect the SW-269 contract
// removes.
//
// The test also asserts CheckEmbedderSpec accepts the report outright:
// the runner is the only path that stamps the spec, but a read-time
// check is what enforces it; if either side regresses the other fails
// too.
func TestRetrievalEval_ReportStampsEmbedderSpecForConfiguredRun(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	var stderr bytes.Buffer
	if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
		"-out", out, "-runner-class", "test", "-repeats", "1", "-date", "2026-08-30",
		"-embedder", "mock"}, &bytes.Buffer{}, &stderr); code != exitOK {
		t.Fatalf("mock-configured run exit %d, want 0:\n%s", code, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report retrieval.Report
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	spec := report.Reproducible.EmbedderSpec
	if spec == nil {
		t.Fatalf("report %s has no embedder_spec (a /3 configured run MUST carry the resolved fingerprint)", out)
	}
	if spec.LexicalOnly || spec.Fingerprint == "" {
		t.Errorf("configured run embedder_spec = %+v, want LexicalOnly=false and non-empty Fingerprint", spec)
	}
	if err := retrieval.CheckEmbedderSpec(&report); err != nil {
		t.Errorf("CheckEmbedderSpec(configured report): %v", err)
	}
	// The harness_version must be /3 — the runner stamps /3, the reader
	// accepts /3 (current) and /2 (legacy); if the runner were to lapse
	// to /2 this would catch the regression by failing the version pin
	// in CheckReportVersion (the report must round-trip through /3
	// without exiting legacy).
	if report.HarnessVersion != retrieval.HarnessVersion {
		t.Errorf("harness_version = %q, want %q", report.HarnessVersion, retrieval.HarnessVersion)
	}
}

// TestRetrievalEval_ReaderRefusesReportWithoutEmbedderSpec (AC-5) is
// the "reading without a fingerprint is an error" half of the SW-269
// contract, expressed against the reader path the harness itself uses.
// A /3-shaped report whose `embedder_spec` is silently dropped (the
// defect the story removes) must be refused with a message that names
// the field; the contract is enforced, not optional. The version of
// the tampered report is left at /3 so the CheckReportVersion gate
// accepts the shape and the CheckEmbedderSpec gate is what fires.
func TestRetrievalEval_ReaderRefusesReportWithoutEmbedderSpec(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	var stderr bytes.Buffer
	if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
		"-out", out, "-runner-class", "test", "-repeats", "1", "-date", "2026-08-30",
		"-embedder", "mock"}, &bytes.Buffer{}, &stderr); code != exitOK {
		t.Fatalf("configured run exit %d, want 0:\n%s", code, stderr.String())
	}

	// Tamper: drop the embedder_spec field and re-serialise. The bytes
	// the reader sees match the digest in run.json only when this edit
	// is done via a path the reader also stamps; for the unit-level
	// assertion we don't need a full run directory, just a round-trip
	// through marshal/unmarshal that proves CheckEmbedderSpec fires.
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report retrieval.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	report.Reproducible.EmbedderSpec = nil
	if err := retrieval.CheckEmbedderSpec(&report); err == nil {
		t.Fatal("CheckEmbedderSpec(nil spec on /3 report) = nil error; want a refusal naming the field")
	} else if !strings.Contains(err.Error(), "embedder_spec") {
		t.Errorf("refusal does not name embedder_spec: %v", err)
	}

	// Both-set is symmetrically rejected — exactly one of the two markers
	// is the contract; "best-effort interpretation" is what the story
	// refuses to leave in place.
	report.Reproducible.EmbedderSpec = &retrieval.EmbedderSpec{Fingerprint: "abc", LexicalOnly: true}
	if err := retrieval.CheckEmbedderSpec(&report); err == nil || !strings.Contains(err.Error(), "BOTH") {
		t.Errorf("BOTH-set refusal = %v; want an error naming 'BOTH'", err)
	}
	// Empty-both (neither marker set) is the silent-accept defect
	// shape: the same fingerprint-less ambiguity as the nil case,
	// surfaced through a different representation. Same refusal.
	report.Reproducible.EmbedderSpec = &retrieval.EmbedderSpec{}
	if err := retrieval.CheckEmbedderSpec(&report); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty embedder_spec refusal = %v; want an error naming 'empty'", err)
	}
}

// A URL-pinned entry is never cloned: an absent checkout fails closed with
// the instruction, and a checkout at another sha is refused.
func TestResolveRepo_PinnedEntryNeedsALocalCheckoutAtTheSHA(t *testing.T) {
	chdirRoot(t)
	ctx := t.Context()
	if _, _, err := resolveRepo(ctx, "corpus/manifest.json", "cobra", filepath.Join(t.TempDir(), "absent")); err == nil || !strings.Contains(err.Error(), "never clones") {
		t.Errorf("absent checkout: err = %v", err)
	}
	wrong := t.TempDir()
	if _, _, err := resolveRepo(ctx, "corpus/manifest.json", "cobra", wrong); err == nil {
		t.Error("a directory that is not a git checkout must be refused")
	}
	root, sha, err := resolveRepo(ctx, "corpus/manifest.json", "tier1-fixture-hero-go", "")
	if err != nil || sha != "" || !strings.HasSuffix(filepath.ToSlash(root), "corpus/fixtures/hero-go") {
		t.Errorf("local-path entry = %q, %q, %v", root, sha, err)
	}
	root, sha, err = resolveRepo(ctx, "corpus/manifest.json", FixtureRepoName, "")
	if err != nil || sha != "" || root != filepath.FromSlash(fixtureRepoPath) {
		t.Errorf("fixture = %q, %q, %v", root, sha, err)
	}
}
