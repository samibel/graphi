package main

// SW-177 (W1.d): the published-artifact half of the G7 JVM baseline.
//
// These tests read the CHECKED-IN run directories under
// docs/eval/runs/2026-08-19-local-sandbox/ and pin the properties the baseline
// document asserts about them, so the document and the evidence cannot drift
// apart silently. They follow SW-134's and SW-153's precedent
// (partialoutcome_characterization_test.go::TestPublishedBaseline_*): the
// mechanism tests live beside the mechanism, and the arithmetic over published
// artifacts lives here in cmd/eval, because committed JSON is what they are
// about.
//
// ON AC-7 (wall-clock assertions). Nothing here measures time. Every number
// below is read out of a frozen committed file, so these are file-content
// assertions, not timing assertions, and the "order-of-magnitude margin or a
// stated noise budget" rule has nothing to bind. Where a duration IS compared
// against a constant (TestPublishedG7_GuavaAgentBriefIsP0Shaped) the constant is
// chosen with a stated margin against the published spread and is documented at
// its use site — and it would still be deterministic on a machine that never
// runs the harness at all.
//
// No network, no corpus clone, no index: `go test ./cmd/eval` reads JSON.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// g7RunDir is the run directory SW-177 published.
// docs/eval/runs/2026-08-19-local-sandbox/g7-jvm-baseline.md describes it.
const g7RunDir = "../../docs/eval/runs/2026-08-19-local-sandbox"

// g7CandidateSHA is the W0.f-5 (LINK-001 / ADR 0011) measurement candidate every
// figure in that directory was measured at. AC-3 is exactly this: the candidate
// SHA is recorded with every figure, so it is asserted over every artifact
// rather than stated once in prose.
const g7CandidateSHA = "3b8d43f6bc0a264c74424ca209b6fbd2401c9a31"

var g7JVMRepos = []string{"guava", "okio", "kotlinx.serialization"}
var g7Dispatches = []string{"run-a", "run-b", "run-c"}

type g7Environment struct {
	CPUModel       string `json:"cpu_model"`
	CPUCount       int    `json:"cpu_count"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	RunnerClass    string `json:"runner_class"`
	RunnerRole     string `json:"runner_role"`
	MeasuredSHA    string `json:"measured_sha"`
	CandidateSHA   string `json:"candidate_sha"`
	CandidateMatch bool   `json:"candidate_match"`
	WorktreeDirty  bool   `json:"worktree_dirty"`
	CacheState     string `json:"cache_state"`
}

func readG7JSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// TestPublishedG7_EveryFigureCarriesTheCandidateSHAAndTheMachine is AC-3 and
// AC-6 asserted over the artifacts instead of asserted in prose. It walks EVERY
// environment.json in the run directory — including the platform-control legs,
// which are measured at a different revision on purpose — so a leg added later
// without provenance cannot hide in the middle of the tree.
func TestPublishedG7_EveryFigureCarriesTheCandidateSHAAndTheMachine(t *testing.T) {
	var seen int
	var candidateLegs int
	err := filepath.WalkDir(g7RunDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil || filepath.Base(path) != "environment.json" {
			return err
		}
		seen++
		var env g7Environment
		readG7JSON(t, path, &env)

		// AC-6: the machine, including the CPU model, on every single run.
		if strings.TrimSpace(env.CPUModel) == "" {
			t.Errorf("%s: cpu_model is empty; AC-6 requires the CPU model per run", path)
		}
		if env.CPUCount <= 0 {
			t.Errorf("%s: cpu_count = %d", path, env.CPUCount)
		}

		// The runner class is what makes every gate in these artifacts UNKNOWN
		// and what forbids freezing a budget from them (see the document, §0
		// and §12). If this ever reads "reference", the whole publication has
		// changed meaning.
		if env.RunnerClass != "local-sandbox" || env.RunnerRole != "comparison" {
			t.Errorf("%s: runner_class/%s role/%s, want local-sandbox/comparison — "+
				"these artifacts are published as COMPARISON-class evidence and their "+
				"scope statements depend on it", path, env.RunnerClass, env.RunnerRole)
		}

		// A dirty worktree would mean the measured bytes are not the named
		// revision's bytes.
		if env.WorktreeDirty {
			t.Errorf("%s: worktree_dirty is true; the measured tree is not the named revision", path)
		}

		// The release candidate is NOT what was measured, and the artifacts must
		// keep saying so. A true here would mean someone re-pointed the
		// evidence-index candidate block, which changes what these figures are
		// evidence about.
		if env.CandidateMatch {
			t.Errorf("%s: candidate_match is true. Every figure in this directory was "+
				"measured at %s, which is NOT the release candidate the evidence index "+
				"names. If the candidate has moved, the baseline document's §0 and §11 "+
				"are now wrong and must move with it.", path, g7CandidateSHA)
		}

		if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(g7RunDir)+"/run-") {
			candidateLegs++
			if env.MeasuredSHA != g7CandidateSHA {
				t.Errorf("%s: measured_sha = %q, want the W0.f-5 candidate %s (AC-3)",
					path, env.MeasuredSHA, g7CandidateSHA)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", g7RunDir, err)
	}
	// 3 dispatches x 3 repos x 3 suites. Asserted so that a directory silently
	// losing legs does not leave a green, vacuous test behind.
	if candidateLegs != 27 {
		t.Errorf("found %d candidate-measured legs, want 27 (3 dispatches x 3 repos x 3 suites)", candidateLegs)
	}
	if seen < candidateLegs {
		t.Errorf("walked %d environment.json but only %d candidate legs; walk is inconsistent", seen, candidateLegs)
	}
}

type g7ColdReport struct {
	ColdSeries struct {
		RunsCompleted int `json:"runs_completed"`
		Aggregates    map[string]struct {
			N   int     `json:"n"`
			Min float64 `json:"min"`
			P50 float64 `json:"p50"`
			Max float64 `json:"max"`
		} `json:"aggregates"`
	} `json:"cold_series"`
}

// TestPublishedG7_ColdIndexIsThirtyProcessesAndOneGraph pins the two properties
// that make §3's spread readable: the sample really is 10 cold PROCESSES per
// dispatch, and the graph the three dispatches built is the same graph. Without
// the second, a published wall-clock spread (1.05x-1.23x depending on the pin)
// could be a changing workload rather than noise, and would mean nothing.
func TestPublishedG7_ColdIndexIsThirtyProcessesAndOneGraph(t *testing.T) {
	for _, repo := range g7JVMRepos {
		nodes := map[float64]bool{}
		edges := map[float64]bool{}
		for _, d := range g7Dispatches {
			var rep g7ColdReport
			readG7JSON(t, filepath.Join(g7RunDir, d, "cold-index", repo, "report.json"), &rep)

			if rep.ColdSeries.RunsCompleted != 10 {
				t.Errorf("%s/%s: runs_completed = %d, want 10", d, repo, rep.ColdSeries.RunsCompleted)
			}
			wall, ok := rep.ColdSeries.Aggregates["index_wallclock_ms"]
			if !ok || wall.N != 10 {
				t.Errorf("%s/%s: index_wallclock_ms n = %d, want 10 samples", d, repo, wall.N)
			}
			nodes[rep.ColdSeries.Aggregates["nodes"].P50] = true
			edges[rep.ColdSeries.Aggregates["edges"].P50] = true
		}
		if len(nodes) != 1 || len(edges) != 1 {
			t.Errorf("%s: the three dispatches did not build the same graph "+
				"(distinct node counts: %d, distinct edge counts: %d). The published "+
				"wall-clock spread is only readable as noise because the workload is "+
				"identical across dispatches; if that stops holding, §3 of the baseline "+
				"document is no longer true.", repo, len(nodes), len(edges))
		}
	}
}

type g7QueryReport struct {
	Repo struct {
		QueryLatency struct {
			Classes []struct {
				Class       string `json:"class"`
				N           int    `json:"n"`
				Sufficient  bool   `json:"sufficient"`
				P50US       int64  `json:"p50_us"`
				P95US       int64  `json:"p95_us"`
				Executions  int    `json:"executions"`
				MinimumReqd int    `json:"minimum"`
			} `json:"classes"`
			Operations []struct {
				Operation string `json:"operation"`
				Latency   struct {
					N     int   `json:"n"`
					MinUS int64 `json:"min_us"`
					P50US int64 `json:"p50_us"`
					MaxUS int64 `json:"max_us"`
				} `json:"latency"`
			} `json:"operations"`
		} `json:"query_latency"`
	} `json:"repo"`
}

// TestPublishedG7_QueryLatencyMeetsTheFR8Floor keeps §4's table honest about its
// own sufficiency: a percentile over too few executions is UNKNOWN, not a
// baseline, and the harness says so per class.
func TestPublishedG7_QueryLatencyMeetsTheFR8Floor(t *testing.T) {
	for _, repo := range g7JVMRepos {
		for _, d := range g7Dispatches {
			var rep g7QueryReport
			readG7JSON(t, filepath.Join(g7RunDir, d, "query-latency", repo, "report.json"), &rep)
			if len(rep.Repo.QueryLatency.Classes) != 3 {
				t.Fatalf("%s/%s: %d classes, want 3", d, repo, len(rep.Repo.QueryLatency.Classes))
			}
			for _, c := range rep.Repo.QueryLatency.Classes {
				if !c.Sufficient {
					t.Errorf("%s/%s class %s: sufficient=false (n=%d, floor %d) — §4 publishes "+
						"it as a baseline, which requires the FR-8 floor to be met",
						d, repo, c.Class, c.N, c.MinimumReqd)
				}
			}
		}
	}
}

// TestPublishedG7_GuavaAgentBriefIsP0Shaped pins the single most consequential
// finding in the run: on guava the agent_tools POOL reads a sub-millisecond p50
// while agent_brief alone is uniformly hundreds of milliseconds. The pooled
// percentile is therefore an artifact of pool composition, and the cost is
// p0-shaped rather than a tail — the same shape SW-153 established for
// freshness_p95, and the reason "shave the tail" is not a candidate fix here.
//
// MARGINS, stated rather than implied (AC-7). The published guava agent_brief
// minimum across the three dispatches is 358,301-360,370 us. The floor asserted
// below is 300,000 us: ~16% under the smallest published value, which is far
// wider than the 0.6% spread the three dispatches actually show — and wider
// still than the 12.7% campaign-to-campaign movement measured in §3.1 of the
// baseline document. The pooled-p50 ceiling is 1,000 us against a published
// 94-100 us — a 10x margin. Both are comparisons against frozen committed
// numbers, not against a fresh measurement.
func TestPublishedG7_GuavaAgentBriefIsP0Shaped(t *testing.T) {
	for _, d := range g7Dispatches {
		var rep g7QueryReport
		readG7JSON(t, filepath.Join(g7RunDir, d, "query-latency", "guava", "report.json"), &rep)

		var pooledP50 int64 = -1
		for _, c := range rep.Repo.QueryLatency.Classes {
			if c.Class == "agent_tools" {
				pooledP50 = c.P50US
			}
		}
		if pooledP50 < 0 {
			t.Fatalf("%s: no agent_tools class in the published report", d)
		}
		if pooledP50 > 1000 {
			t.Errorf("%s: agent_tools pooled p50 = %d us, expected sub-millisecond. "+
				"§4.1's whole point is that the pool's p50 and its p95 describe different "+
				"operations; if the p50 has moved into the millisecond range the "+
				"decomposition needs re-reading.", d, pooledP50)
		}

		var brief struct {
			N     int
			MinUS int64
		}
		for _, op := range rep.Repo.QueryLatency.Operations {
			if op.Operation == "agent_brief" {
				brief.N, brief.MinUS = op.Latency.N, op.Latency.MinUS
			}
		}
		if brief.N != 250 {
			t.Fatalf("%s: agent_brief n = %d, want 250", d, brief.N)
		}
		if brief.MinUS < 300_000 {
			t.Errorf("%s: agent_brief MINIMUM = %d us, published as >= 358,301 us. "+
				"A minimum that has dropped below 300 ms would mean the p0-shaped finding "+
				"in §4.1 no longer holds and the baseline document must be re-read, not "+
				"this threshold relaxed.", d, brief.MinUS)
		}
	}
}

// TestPublishedG7_FreshnessBlockTranscriptsAreAFrozenRecordOfAClosedDefect pins
// the evidence for §5.
//
// SW-191 CHANGED WHAT THIS TEST MEANS, so its name and its failure messages
// changed with it. When SW-177 wrote it, the transcripts recorded a LIVE
// constraint: the freshness suite could not run on a JVM pin, and the assertion
// was the tripwire that would fire when someone closed EVALFRESH-001 without
// moving §5 and the G7 rows. EVALFRESH-001 is now closed
// (cmd/eval/sourcefamily.go, cmd/eval/freshness{jvm,python,typescript}_test.go),
// so what these files hold is a FROZEN HISTORICAL RECORD of a defect that no
// longer reproduces — the same bytes, a different claim.
//
// The assertion is kept, rather than deleted, because the record is the
// evidence §5 rests on and a silently-rewritten transcript would leave §5
// citing something that never said what it says. What it must NOT do any more
// is read as a statement about the harness's present behaviour: running
// `-full-run guava -incremental-changes 100` today does NOT reproduce these
// transcripts. That is stated here and in §5 rather than left to be inferred.
//
// The cobra control is asserted together with the three refusals and never
// separately: without it the refusals say nothing about LANGUAGE, only that
// something failed.
func TestPublishedG7_FreshnessBlockTranscriptsAreAFrozenRecordOfAClosedDefect(t *testing.T) {
	dir := filepath.Join(g7RunDir, "freshness-blocked")
	for _, repo := range g7JVMRepos {
		raw, err := os.ReadFile(filepath.Join(dir, repo+".txt"))
		if err != nil {
			t.Fatalf("read %s transcript: %v", repo, err)
		}
		text := string(raw)
		if !strings.Contains(text, "the index contains no modifiable Go source files to change") {
			t.Errorf("%s transcript no longer records the refusal it was captured for. These "+
				"files are a FROZEN record of EVALFRESH-001 as SW-177 measured it; §5 of the "+
				"baseline document cites them, so editing them leaves the document citing "+
				"something that never said what it says. (The defect itself is closed — a "+
				"fresh run does not reproduce this text, and §5 says so.)", repo)
		}
		if !strings.Contains(text, "exit status: 1") {
			t.Errorf("%s transcript does not record a failing exit status", repo)
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, "control-cobra-go.txt"))
	if err != nil {
		t.Fatalf("read the Go control transcript: %v", err)
	}
	if !strings.Contains(string(raw), "8/8 changes completed") {
		t.Errorf("the cobra control no longer records a completed change sequence. Without " +
			"it the three JVM refusals say nothing about language, and §5's claim is unsupported.")
	}
}

// TestPublishedG7_FreshnessBlockDoesNotReproduce is the live half of the pair
// above: the code path those frozen transcripts recorded is gone, so the
// mechanism that produced them must no longer be present. It is a MECHANISM
// assertion (no clone, no network) — the corpus-level proof is the -full-run
// evidence recorded in the SW-191 verification record.
func TestPublishedG7_FreshnessBlockDoesNotReproduce(t *testing.T) {
	for _, p := range []string{
		"guava/src/com/google/common/collect/ImmutableList.java",
		"okio/src/commonMain/kotlin/okio/Buffer.kt",
		"core/commonMain/src/kotlinx/serialization/Serializer.kt",
	} {
		packages := map[string]string{}
		if !admitSourceFile(p, []byte("package com.example;\n\nclass X {}\n"), packages) {
			t.Errorf("admitSourceFile(%q) = false: the JVM pins would abort again with the "+
				"message the frozen transcripts record", p)
		}
	}
	if strings.Contains(changeSequenceMethod(), "Go source files") {
		t.Error("the published determinism string still claims a Go-only scope")
	}
}
