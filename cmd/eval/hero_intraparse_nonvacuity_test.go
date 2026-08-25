package main

// SW-202 — THE NON-VACUITY GUARD FOR THE SIX INTRA/PARSE HERO SUITES.
//
// A suite built out of honest-empty and abstention scenarios is maximally
// exposed to the failure mode this repository has hit before: a scenario that
// asserts `outcome: empty` passes just as happily when the operation never
// really ran, when the anchor quietly stopped existing, or when the fixture
// lost the construct the prose says it carries. "Green" then means nothing.
//
// This file is the mechanized guard against that, and it is deliberately
// cross-cutting: a per-language hero suite cannot opt out of it.
//
//	TestHeroIntraParse_AbstentionAnchorsResolve
//	    The empty answers must be the RELATION's, never the ANCHOR's. For the
//	    five symbol-minting languages, every abstention scenario's anchor is
//	    re-asked of the same fixture engine and a node whose qualified name is
//	    EXACTLY the anchor must come back. Note the asymmetry that makes this
//	    necessary: callers / callees / references / impact report not_found for
//	    an unresolved anchor, so their `empty` already proves resolution — but
//	    related_files, explain_symbol and change_risk report `empty` for an
//	    unresolved anchor too, so for those the scenario alone proves nothing
//	    and this control is the proof.
//
//	    WHY THE CONTROL DEMANDS AN EXACT QUALIFIED NAME AND NOT MERELY A
//	    RESOLUTION, which is a real finding and not a stylistic choice.
//	    resolve.Strict's documented order is node id, file path, exact
//	    qualified name, THEN LEXICAL SEARCH, and a single search hit resolves
//	    heuristically. Renaming `main` to `mainAlt` in the css fixture
//	    therefore leaves definition("base.main") reporting FOUND — it resolves
//	    to base.mainAlt through the search fallback — so a control that only
//	    asked "did it resolve?" would stay green while the symbol the whole
//	    suite is anchored on had ceased to exist. That is exactly the vacuous
//	    shape this file exists to catch, and it was caught here first: the
//	    weaker control was written, the rename mutation failed to turn it red,
//	    and the control was strengthened rather than the demonstration dropped.
//	    The neighborhood operation is used because it returns the SYMBOL node
//	    with its qualified name, where definition returns the defining file.
//
//	TestHeroIntraParse_JsonIsParseOnly
//	    json's suite asserts the opposite shape, so it needs the opposite
//	    control: each checked-in document must PARSE SUCCESSFULLY and still
//	    contribute no node. That is what makes hero-json's empty index the
//	    documented "parsed and unrepresented" finding (LANGHONEST-001) rather
//	    than an unread directory.
//
//	TestHeroIntraParse_FixtureConstructsMatchTheProse
//	    The §5.5 descriptions make claims about the FIXTURE — that css and
//	    markdown and yaml carry their language's cross-file construct while
//	    hcl and toml deliberately carry none. Those claims are asserted here
//	    against the checked-in bytes, so prose and artifact cannot drift apart.
//
//	TestHeroIntraParse_GraphCensusMatchesTheG6Rows
//	    Added in rebuild round 1 for review finding M-1. The five
//	    symbol-minting suites carried NO graph census, so a fixture document
//	    could be blanked to zero bytes with every gate still green and the G6
//	    rows' published node/edge counts bound by nothing. This asserts each
//	    row's counts against the built graph, in the same shape
//	    TestHeroJsonSuite_AmbiguousIsUnreachable already used for json.
//
//	TestHeroIntraParse_SuitesProduceRealEvidence
//	    Every suite must contain at least one scenario that comes back with
//	    real cited evidence, so a suite of nothing-but-empties cannot pass.
//	    json is included: its brief is the one operation that stays useful.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/scenario"
)

// symbolMintingIntraParseLangs are the five residual languages that DO mint
// symbols. json is excluded by construction and has its own control below.
var symbolMintingIntraParseLangs = []struct {
	lang string
	dir  string
}{
	{"css", "hero-css"},
	{"hcl", "hero-hcl"},
	{"markdown", "hero-markdown"},
	{"toml", "hero-toml"},
	{"yaml", "hero-yaml"},
}

// anchorOf returns the symbol-shaped argument a scenario points its operation
// at, or "" when the operation takes none (index, search).
func anchorOf(s scenario.Scenario) string {
	for _, key := range []string{"symbol", "anchor", "target", "topic", "ref"} {
		if v := s.Operation.Args[key]; v != "" {
			return v
		}
	}
	return ""
}

func TestHeroIntraParse_AbstentionAnchorsResolve(t *testing.T) {
	root := repoRoot(t)
	for _, lc := range symbolMintingIntraParseLangs {
		t.Run(lc.lang, func(t *testing.T) {
			eng, err := buildFixtureEngine(filepath.Join(root, "corpus", "fixtures", "hero-"+lc.lang))
			if err != nil {
				t.Fatalf("build hero-%s fixture engine: %v", lc.lang, err)
			}
			checked := 0
			for _, s := range loadIntraParseSuite(t, lc.dir) {
				if !isAbstentionScenario(s) {
					continue
				}
				anchor := anchorOf(s)
				if anchor == "" {
					t.Errorf("%s is an abstention scenario with no symbol-shaped anchor; its empty answer cannot be attributed to a relation", s.ID)
					continue
				}
				lines, _, derr := eng.Invoke("neighborhood", map[string]string{"symbol": anchor, "depth": "1"})
				if derr != nil {
					t.Errorf("%s: control neighborhood(%s) errored: %v", s.ID, anchor, derr)
					continue
				}
				got := ""
				exact := false
				for _, l := range lines {
					if v, ok := strings.CutPrefix(l, "outcome:"); ok {
						got = v
						continue
					}
					for _, field := range strings.Fields(l) {
						if field == anchor {
							exact = true
						}
					}
				}
				if got != "found" {
					t.Errorf("%s asserts %q for anchor %s, but the paired control neighborhood(%s) reports %q — the scenario's empty answer would be the ANCHOR's absence, not the relation's, which is exactly the vacuous shape this guard exists to catch",
						s.ID, s.Expect.Outcome, anchor, anchor, got)
				} else if !exact {
					t.Errorf("%s asserts %q for anchor %s, but the paired control neighborhood(%s) came back with no node whose qualified name is exactly %s (evidence: %v) — resolve.Strict falls back to lexical search, so this is what a renamed or deleted anchor looks like while still reporting found",
						s.ID, s.Expect.Outcome, anchor, anchor, anchor, lines)
				}
				checked++
			}
			if checked == 0 {
				t.Errorf("hero-%s produced no abstention anchor to control — this guard must have something to check", lc.lang)
			}
		})
	}
}

func TestHeroIntraParse_JsonIsParseOnly(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, heroJSONFixturePath)
	docs, err := filepath.Glob(filepath.Join(dir, "*", "*.json"))
	if err != nil {
		t.Fatalf("glob hero-json documents: %v", err)
	}
	if len(docs) < 4 {
		t.Fatalf("hero-json carries %d json documents, want at least 4", len(docs))
	}
	p := parse.NewJSONParser()
	for _, doc := range docs {
		src, rerr := os.ReadFile(doc)
		if rerr != nil {
			t.Fatalf("read %s: %v", doc, rerr)
		}
		rel, _ := filepath.Rel(root, doc)
		res, perr := p.Parse(context.Background(), rel, src)
		if perr != nil {
			t.Errorf("%s: PARSE FAILED (%v) — hero-json's whole claim is that these documents parse successfully and are still unrepresented; an unparseable document would make the suite's empties mean something else entirely", rel, perr)
			continue
		}
		if len(res.Nodes) != 0 || len(res.Edges) != 0 {
			t.Errorf("%s: json parse produced %d nodes and %d edges, want 0 and 0 — json is graded parse-only and hero-json's abstention scenarios rest on that", rel, len(res.Nodes), len(res.Edges))
		}
	}
}

// fixtureConstructClaim binds one sentence of §5.5 prose to the checked-in
// fixture bytes it claims something about.
type fixtureConstructClaim struct {
	// file is repo-root-relative.
	file string
	// needle is the construct text.
	needle string
	// want is true when the prose says the fixture CARRIES the construct, and
	// false when the prose says the fixture deliberately does NOT.
	want bool
	// codeOnly drops `#` comment lines before searching. The "deliberately
	// absent" claims are about the DOCUMENT, and the fixtures' own header
	// comments explain in prose why the construct is missing — a search that
	// counted those comments would fail on the explanation itself.
	codeOnly bool
	// why is the claim the description makes, quoted back in the failure.
	why string
}

// documentBody returns src with whole-line `#` comments removed.
func documentBody(src string) string {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

var intraParseConstructClaims = []fixtureConstructClaim{
	{"corpus/fixtures/hero-css/theme/light.css", `@import "../base/tokens.css"`, true, false,
		"the css abstention descriptions say theme/light.css opens with CSS's own @import at-rule and that graphi still declines it"},
	{"corpus/fixtures/hero-css/theme/dark.css", `@import "../base/tokens.css"`, true, false,
		"the css abstention descriptions say theme/dark.css opens with CSS's own @import at-rule too"},
	{"corpus/fixtures/hero-markdown/guide/intro.md", "[Configure](./setup.md)", true, false,
		"the markdown abstention descriptions say guide/intro.md carries CommonMark's inline link to setup.md"},
	{"corpus/fixtures/hero-yaml/pipeline/build.yaml", "shared: !include ../shared/common.yaml", true, false,
		"the yaml abstention descriptions say build.yaml uses YAML 1.2's tag mechanism"},
	{"corpus/fixtures/hero-yaml/pipeline/build.yaml", "include: ../shared/common.yaml", true, false,
		"the yaml abstention descriptions say build.yaml ALSO carries the bare convention key, so the two shapes are distinguishable in the source"},
	{"corpus/fixtures/hero-json/schema/server.schema.json", `"$ref": "./endpoint.schema.json"`, true, false,
		"the json abstention descriptions say schema/server.schema.json writes JSON Schema's $ref"},
	{"corpus/fixtures/hero-hcl/infra/main.tf", `module "`, false, true,
		"the hcl abstention descriptions say the fixture deliberately writes no Terraform module block, because grading a module block would be grading Terraform rather than HCL"},
	{"corpus/fixtures/hero-hcl/infra/other.tf", `module "`, false, true,
		"the hcl abstention descriptions say the fixture deliberately writes no Terraform module block"},
	{"corpus/fixtures/hero-hcl/modules/net.tf", `module "`, false, true,
		"the hcl abstention descriptions say the fixture deliberately writes no Terraform module block"},
	{"corpus/fixtures/hero-toml/config/app.toml", "include", false, true,
		"the toml abstention descriptions say the fixture writes no include key, because a fixture that did would be grading the host application rather than TOML"},
	{"corpus/fixtures/hero-toml/config/extra.toml", "include", false, true,
		"the toml abstention descriptions say the fixture writes no include key"},
	{"corpus/fixtures/hero-toml/deploy/stage.toml", "include", false, true,
		"the toml abstention descriptions say the fixture writes no include key"},
}

func TestHeroIntraParse_FixtureConstructsMatchTheProse(t *testing.T) {
	root := repoRoot(t)
	for _, c := range intraParseConstructClaims {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
		if err != nil {
			t.Errorf("read %s: %v", c.file, err)
			continue
		}
		src := string(raw)
		if c.codeOnly {
			src = documentBody(src)
		}
		got := strings.Contains(src, c.needle)
		if got == c.want {
			continue
		}
		if c.want {
			t.Errorf("%s no longer contains %q — %s. The prose is now false of the artifact", c.file, c.needle, c.why)
			continue
		}
		t.Errorf("%s now contains %q — %s. The prose is now false of the artifact", c.file, c.needle, c.why)
	}
}

func TestHeroIntraParse_SuitesProduceRealEvidence(t *testing.T) {
	root := repoRoot(t)
	for _, rule := range intraParseSpecCiteRules {
		t.Run(rule.lang, func(t *testing.T) {
			fixtures := heroJSONFixtures(t, root)
			results, err := runScenarios(filepath.Join(root, "corpus", rule.dir), root, fixtures, 1)
			if err != nil {
				t.Fatalf("run %s: %v", rule.dir, err)
			}
			evidenced := 0
			positive := 0
			for _, r := range results {
				if len(r.Evidence) > 0 {
					evidenced++
				}
				if r.AnchorPresent && r.ResultSize > 0 {
					positive++
				}
			}
			if evidenced == 0 {
				t.Errorf("no scenario in %s came back with any evidence line — a suite that only ever produces empty answers proves nothing about the operations it claims to cover", rule.dir)
			}
			if positive == 0 {
				t.Errorf("no scenario in %s produced a non-empty answer — the abstention scenarios need a working positive control in the same suite", rule.dir)
			}
		})
	}
}

// ── M-1 (review of e3975f9): THE GRAPH CENSUS, BOUND TO THE G6 ROWS ─────────
//
// The review of this story blanked corpus/fixtures/hero-hcl/modules/net.tf to
// ZERO BYTES and every gate above stayed green. That file contributes two of
// hcl's seven symbols, so `GA-LANG-hcl-G6`'s published sentence — "3 files …
// 7 block and attribute symbols plus 3 file nodes = 10 graph nodes and 7
// defines edges" — was prose bound by nothing. It could become false without a
// gate noticing, which is the very failure class ("a witness that cannot
// fail") this story was written against.
//
// TestHeroIntraParse_GraphCensusMatchesTheG6Rows closes it. Each of the five
// symbol-minting suites now carries the same shape json already had in
// TestHeroJsonSuite_AmbiguousIsUnreachable — build the fixture engine, read
// the graph back, count it — except that json asserts 0/0 and these five
// assert the exact numbers their G6 row PUBLISHES. The numbers below are
// transcribed from docs/rc/evidence-index.yaml, not measured and then written
// down: if the fixture and the row ever disagree, one of them is lying and
// this test says which.
//
// Three things are asserted per language, and each binds a different half of
// the sentence:
//
//   - fileNodes / symbolNodes / totalNodes — the row's "N files, M symbols
//     plus N file nodes = T graph nodes" arithmetic, so deleting, emptying or
//     adding a fixture file moves a number the row states;
//   - definesEdges, and that `defines` is the ONLY edge kind — the row's "and
//     E defines edges". An intra-file-only language that suddenly grew an
//     `imports` edge would change what the whole §5.5 grading rests on;
//   - every file node contributes at least one symbol. Counting alone would
//     still admit a fixture where one document was blanked and another grew
//     compensating symbols; this makes each of the three documents carry its
//     own weight, which is what "3 files, 7 symbols" is understood to mean.
//
// PROVEN, NOT ASSUMED: blanking modules/net.tf to zero bytes now fails this
// test with "hero-hcl fixture graph has 8 nodes, want 10" — see the story's
// Rebuild round 1 record, which carries the captured RED output for hcl, css
// and yaml.
type intraParseCensusClaim struct {
	lang string
	// fileNodes is the number of `file`-kind nodes: the fixture's documents.
	fileNodes int
	// symbolNodes is the number of non-file nodes the row calls "symbols".
	symbolNodes int
	// totalNodes is the row's stated total, asserted separately so the row's
	// own arithmetic is checked and not just re-derived.
	totalNodes int
	// definesEdges is the row's stated `defines` edge count.
	definesEdges int
	// rowPhrase is the sentence from docs/rc/evidence-index.yaml this claim
	// binds, quoted back in every failure so the reader knows which published
	// prose just became false.
	rowPhrase string
}

// intraParseCensusClaims transcribes the five G6 rows' census sentences.
var intraParseCensusClaims = []intraParseCensusClaim{
	{"css", 3, 7, 10, 7,
		"GA-LANG-css-G6: \"3 files, 7 selector symbols plus 3 file nodes = 10 graph nodes and 7 defines edges\""},
	{"hcl", 3, 7, 10, 7,
		"GA-LANG-hcl-G6: \"3 files across infra/ and modules/, 7 block and attribute symbols plus 3 file nodes = 10 graph nodes and 7 defines edges\""},
	{"markdown", 3, 7, 10, 7,
		"GA-LANG-markdown-G6: \"3 documents across guide/ and notes/, 7 heading symbols plus 3 file nodes = 10 graph nodes and 7 defines edges\""},
	{"toml", 3, 7, 10, 7,
		"GA-LANG-toml-G6: \"3 documents across config/ and deploy/, 7 table and key symbols plus 3 file nodes = 10 graph nodes and 7 defines edges\""},
	{"yaml", 3, 8, 11, 8,
		"GA-LANG-yaml-G6: \"3 documents across pipeline/ and shared/, 8 top-level mapping-key symbols plus 3 file nodes = 11 graph nodes and 8 defines edges\""},
}

func TestHeroIntraParse_GraphCensusMatchesTheG6Rows(t *testing.T) {
	root := repoRoot(t)
	if len(intraParseCensusClaims) != len(symbolMintingIntraParseLangs) {
		t.Fatalf("%d census claims for %d symbol-minting languages — every suite must carry one", len(intraParseCensusClaims), len(symbolMintingIntraParseLangs))
	}
	for _, c := range intraParseCensusClaims {
		t.Run(c.lang, func(t *testing.T) {
			eng, err := buildFixtureEngine(filepath.Join(root, "corpus", "fixtures", "hero-"+c.lang))
			if err != nil {
				t.Fatalf("build hero-%s fixture engine: %v", c.lang, err)
			}
			reader := eng.Deps.Query.Reader()
			nodes, err := reader.Nodes(context.Background(), graphstore.Query{})
			if err != nil {
				t.Fatalf("read hero-%s nodes: %v", c.lang, err)
			}
			edges, err := reader.Edges(context.Background(), graphstore.Query{})
			if err != nil {
				t.Fatalf("read hero-%s edges: %v", c.lang, err)
			}

			files := 0
			symbols := 0
			symbolsByPath := map[string]int{}
			filePaths := map[string]bool{}
			for _, n := range nodes {
				if n.Kind() == "file" {
					files++
					filePaths[n.SourcePath()] = true
					continue
				}
				symbols++
				symbolsByPath[n.SourcePath()]++
			}
			if files != c.fileNodes {
				t.Errorf("hero-%s fixture graph has %d file nodes, want %d — %s", c.lang, files, c.fileNodes, c.rowPhrase)
			}
			if symbols != c.symbolNodes {
				t.Errorf("hero-%s fixture graph has %d symbol nodes, want %d — %s", c.lang, symbols, c.symbolNodes, c.rowPhrase)
			}
			if len(nodes) != c.totalNodes {
				t.Errorf("hero-%s fixture graph has %d nodes, want %d — %s", c.lang, len(nodes), c.totalNodes, c.rowPhrase)
			}

			defines := 0
			otherKinds := map[string]int{}
			for _, e := range edges {
				if e.Kind() == "defines" {
					defines++
					continue
				}
				otherKinds[e.Kind()]++
			}
			if defines != c.definesEdges {
				t.Errorf("hero-%s fixture graph has %d defines edges, want %d — %s", c.lang, defines, c.definesEdges, c.rowPhrase)
			}
			if len(otherKinds) != 0 {
				t.Errorf("hero-%s fixture graph carries non-defines edges %v — the row states defines edges and nothing else, and an intra-file-only language growing a cross-file edge kind would invalidate the §5.5 grading this suite rests on", c.lang, otherKinds)
			}

			for path := range filePaths {
				if symbolsByPath[path] == 0 {
					t.Errorf("hero-%s document %s contributes no symbol at all — the counts above can still be met by one document going empty while another grows, so each of the %d documents must carry its own weight; %s", c.lang, path, c.fileNodes, c.rowPhrase)
				}
			}
			for path := range symbolsByPath {
				if !filePaths[path] {
					t.Errorf("hero-%s has symbols from %s but no file node for it", c.lang, path)
				}
			}
		})
	}
}
