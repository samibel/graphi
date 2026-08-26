package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureReadme mirrors the shape of readme.md's census section: a heading that
// restates the total, the census sentence with a line-wrapped, partly bold
// decomposition, and one category claim far away from the sentence (the table
// row at readme.md:370 in the live document).
const fixtureReadme = `# graphi

Some prose that mentions no counts at all.

## The whole surface: 173 capabilities

The [capability manifest](docs/capability-manifest.json) is generated from the
CI-enforced coverage matrix and counts **173** capabilities: **59** CLI
subcommands, **56** MCP tools, **22** analyzers, 23 parsers, 7 surfaces, 5 feature
units and 1 GA language. Twelve are the frozen GA operations above; the rest are
**Labs** — shipped, opt-in, outside the promise.

| **Analysis & trust** | ` + "`analyze`" + ` over 22 analyzers (taint, pdg, …) |
`

func fixtureCensus() Census {
	return Census{
		Total: 173,
		ByCategory: map[string]int{
			"cli-subcommand": 59,
			"mcp-tool":       56,
			"parser":         23,
			"analyzer":       22,
			"surface":        7,
			"feature-unit":   5,
			"ga-language":    1,
		},
	}
}

func TestCheckReadmeClaims_Green(t *testing.T) {
	rep := CheckReadmeClaims(fixtureReadme, fixtureCensus())
	if !rep.Pass() {
		t.Fatalf("expected PASS, got violations:\n%s", strings.Join(rep.Violations, "\n"))
	}
	if rep.Total != 173 {
		t.Errorf("headline total = %d, want 173", rep.Total)
	}
	if got := len(rep.Decomposition); got != 7 {
		t.Errorf("decomposition has %d categories, want 7: %+v", got, rep.Decomposition)
	}
	out := rep.Format()
	if !strings.Contains(out, "readme-claims check PASS") {
		t.Errorf("PASS output missing headline: %q", out)
	}
	// AC-7: the scope limit must be visible on a GREEN run too, so nobody reads
	// it as "the readme is verified".
	if !strings.Contains(out, "SCOPE (AC-7): numbers only") || !strings.Contains(out, "does not mean readme.md is verified") {
		t.Errorf("PASS output does not state the gate's scope limit: %q", out)
	}
}

// TestCheckReadmeClaims_KillTests is AC-6: the gate must BITE. Every assertion
// it makes gets a mutation that must turn it red, with a message that names
// what is wrong and where.
func TestCheckReadmeClaims_KillTests(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(string) string
		census   Census
		wantMsgs []string
	}{
		{
			name:     "category count drifts (a CLI subcommand ships, readme unaware)",
			mutate:   func(s string) string { return strings.Replace(s, "**59** CLI", "**58** CLI", 1) },
			census:   fixtureCensus(),
			wantMsgs: []string{"readme.md:8: claims 58 CLI subcommands; docs/capability-manifest.json has 59"},
		},
		{
			name:     "category claim OUTSIDE the census sentence drifts",
			mutate:   func(s string) string { return strings.Replace(s, "over 22 analyzers", "over 21 analyzers", 1) },
			census:   fixtureCensus(),
			wantMsgs: []string{"readme.md:13: claims 21 analyzers; docs/capability-manifest.json has 22"},
		},
		{
			name:   "headline total drifts from the decomposition (the SUM assertion)",
			mutate: func(s string) string { return strings.Replace(s, "counts **173**", "counts **174**", 1) },
			census: fixtureCensus(),
			wantMsgs: []string{
				"readme.md:8: claims 174 capabilities in total; docs/capability-manifest.json has 173",
				"readme.md:8: the decomposition sums to 173 but the sentence states 174 — 1 capability unaccounted for",
			},
		},
		{
			name: "a category is dropped from the enumeration (the SW-215 defect)",
			mutate: func(s string) string {
				return strings.Replace(s, "5 feature\nunits and 1 GA language.", "5 feature\nunits.", 1)
			},
			census: fixtureCensus(),
			wantMsgs: []string{
				"the decomposition sums to 172 but the sentence states 173 — 1 capability unaccounted for",
				`the decomposition omits category "ga-language" (1 row in docs/capability-manifest.json)`,
			},
		},
		{
			name:   "an EIGHTH category appears in the manifest and the readme never names it",
			mutate: func(s string) string { return s },
			census: func() Census {
				c := fixtureCensus()
				c.ByCategory["dashboard"] = 3
				c.Total = 176
				return c
			}(),
			wantMsgs: []string{
				`the decomposition omits category "dashboard" (3 rows in docs/capability-manifest.json)`,
				"claims 173 capabilities in total; docs/capability-manifest.json has 176",
			},
		},
		{
			name:   "the readme names a category the manifest does not have",
			mutate: func(s string) string { return s },
			census: func() Census {
				c := fixtureCensus()
				delete(c.ByCategory, "ga-language")
				c.Total = 172
				return c
			}(),
			wantMsgs: []string{
				`claims 1 GA language, but docs/capability-manifest.json has no "ga-language" rows at all`,
				`the decomposition names category "ga-language", which docs/capability-manifest.json does not have`,
			},
		},
		{
			name:     "the census sentence is deleted — the gate refuses to pass silently",
			mutate:   func(s string) string { return strings.Replace(s, "counts **173** capabilities:", "counts them:", 1) },
			census:   fixtureCensus(),
			wantMsgs: []string{"no capability census sentence found"},
		},
		{
			name: "the census sentence loses its terminator — the decomposition cannot be delimited",
			mutate: func(s string) string {
				s = strings.Replace(s, "units and 1 GA language. Twelve are the frozen GA operations above; the rest are\n**Labs** — shipped, opt-in, outside the promise.", "units and 1 GA language", 1)
				return strings.Replace(s, "| **Analysis & trust** | `analyze` over 22 analyzers (taint, pdg, …) |\n", "", 1)
			},
			census:   fixtureCensus(),
			wantMsgs: []string{"is not terminated by '.'"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := CheckReadmeClaims(tc.mutate(fixtureReadme), tc.census)
			if rep.Pass() {
				t.Fatalf("MUTATION DID NOT BITE: the gate stayed GREEN on %q", tc.name)
			}
			out := rep.Format()
			if !strings.Contains(out, "readme-claims check FAILED") {
				t.Errorf("FAIL output missing headline: %q", out)
			}
			if !strings.Contains(out, "SCOPE (AC-7): numbers only") {
				t.Errorf("FAIL output does not state the gate's scope limit: %q", out)
			}
			for _, want := range tc.wantMsgs {
				if !strings.Contains(out, want) {
					t.Errorf("missing violation %q in:\n%s", want, out)
				}
			}
		})
	}
}

// TestCheckReadmeClaims_ReportsClaimedRealAndLine is AC-2: the failure names
// the claimed number, the real number and the readme.md line.
func TestCheckReadmeClaims_ReportsClaimedRealAndLine(t *testing.T) {
	mutated := strings.Replace(fixtureReadme, "**56** MCP tools", "**51** MCP tools", 1)
	rep := CheckReadmeClaims(mutated, fixtureCensus())
	if rep.Pass() {
		t.Fatal("expected FAIL")
	}
	want := "readme.md:9: claims 51 MCP tools; docs/capability-manifest.json has 56"
	if !strings.Contains(strings.Join(rep.Violations, "\n"), want) {
		t.Errorf("want violation %q, got:\n%s", want, strings.Join(rep.Violations, "\n"))
	}
}

// TestManifestCensus_FailsClosed is AC-5: an unusable manifest is an error, not
// a skip that would let every readme claim pass vacuously.
func TestManifestCensus_FailsClosed(t *testing.T) {
	cases := map[string]string{
		"not JSON":             "{{{",
		"no capabilities key":  `{"schema_version": 2}`,
		"empty capabilities":   `{"capabilities": []}`,
		"row without category": `{"capabilities": [{"id": "x", "category": "parser"}, {"id": "y"}]}`,
	}
	for name, in := range cases {
		if _, err := ManifestCensus([]byte(in)); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestManifestCensus_Counts(t *testing.T) {
	c, err := ManifestCensus([]byte(`{"capabilities": [
		{"id": "a", "category": "parser"},
		{"id": "b", "category": "parser"},
		{"id": "c", "category": "surface"}]}`))
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	if c.Total != 3 || c.ByCategory["parser"] != 2 || c.ByCategory["surface"] != 1 {
		t.Fatalf("wrong census: %+v", c)
	}
	if got := strings.Join(c.Categories(), ","); got != "parser,surface" {
		t.Errorf("Categories() = %q", got)
	}
}

// TestReadmeClaimsMatchLiveManifest binds the CHECKED-IN readme.md to the
// CHECKED-IN manifest, so the drift this gate exists for fails `go test` as
// well as `go run ./cmd/coverage -check`.
func TestReadmeClaimsMatchLiveManifest(t *testing.T) {
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(MatrixJSONPath)))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	census, err := ManifestCensus(manifest)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ReadmePath)))
	if err != nil {
		t.Fatalf("read readme: %v", err)
	}
	rep := CheckReadmeClaims(string(readme), census)
	if !rep.Pass() {
		t.Fatalf("readme.md contradicts %s:\n%s", MatrixJSONPath, rep.Format())
	}
	// Every manifest category is named by the decomposition, and the label map
	// can express all of them — the set-completeness assertion, over live data.
	if len(rep.Decomposition) != len(census.ByCategory) {
		t.Errorf("decomposition covers %d categories, manifest has %d", len(rep.Decomposition), len(census.ByCategory))
	}
}

// TestCheckReadmeClaims_DoesNotFireOnCorrectContent is the other half of the
// risk this gate carries, and the round-1 review's MAJOR-1: a gate that fires on
// correct prose gets disabled by the next person it inconveniences. Each case
// below is content the reviewer wrote by hand and that turned round 1 RED; all
// of it is correct, and none of it is mechanically decidable from the manifest.
func TestCheckReadmeClaims_DoesNotFireOnCorrectContent(t *testing.T) {
	fence := "```"
	cases := []struct {
		name string
		add  func(string) string
	}{
		{
			// FP1 — a SUBSET statement. "3 analyzers" is not a census, and
			// round 1 printed "claims 3 analyzers", an assertion the author
			// never made.
			name: "FP1 a subset statement in ordinary prose",
			add: func(s string) string {
				return s + "\nMost people only ever touch 3 analyzers: taint, call-chain and communities.\n"
			},
		},
		{
			// FP2 — a fenced REAL COMMAND OUTPUT. SW-214 made cited outputs
			// canonical in readme.md; a tool's output is evidence, not a claim.
			name: "FP2 a number inside a fenced real command output",
			add: func(s string) string {
				return s + "\n" + fence + "\n$ graphi doctor\n12 analyzers enabled (of 22 registered)\n" + fence + "\n"
			},
		},
		{
			// FP3 — a markdown link INSIDE the census sentence, every number
			// unchanged and correct. Round 1 cut the enumeration at the '.' in
			// "cli-reference.md" and reported seven false violations.
			name: "FP3 a markdown link inside the census sentence",
			add: func(s string) string {
				return strings.Replace(s, "**59** CLI\nsubcommands,",
					"**59** CLI\nsubcommands ([reference](docs/cli-reference.md)),", 1)
			},
		},
		{
			// FP4 — a fact about Go PACKAGES that shares a word with the
			// manifest's `surface` category. This exact sentence is SW-217's
			// chartered evidence: surfaces/* holds 8 packages while the
			// manifest counts 7 surface capabilities. Both numbers are right.
			name: "FP4 a package count that shares a word with a category (SW-217's evidence)",
			add: func(s string) string {
				return s + "\nThe `surfaces/` layer holds 8 surfaces; the `engine/` layer holds 31 packages.\n"
			},
		},
		{
			// Major-2's other truncators, in the same sentence: a decimal and a
			// version string, either of which cut round 1's enumeration short.
			name: "a decimal and a version string inside the census sentence",
			add: func(s string) string {
				return strings.Replace(s, "1 GA language.",
					"1 GA language (v0.10.0, generated in 0.10 s).", 1)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := CheckReadmeClaims(tc.add(fixtureReadme), fixtureCensus())
			if !rep.Pass() {
				t.Fatalf("FALSE POSITIVE: the gate went RED on correct content:\n%s", strings.Join(rep.Violations, "\n"))
			}
			if got := len(rep.Decomposition); got != 7 {
				t.Errorf("decomposition covers %d categories, want 7 (the enumeration was truncated): %+v", got, rep.Decomposition)
			}
			if rep.Total != 173 {
				t.Errorf("headline total = %d, want 173", rep.Total)
			}
		})
	}
}

// TestCheckReadmeClaims_MarkedClaimsOutsideTheCensusSentenceStillBite pins the
// two non-structural marks of the match domain: a bold number and a totality
// cue make a claim gated wherever it sits. Narrowing WHERE the gate looks must
// not narrow WHAT it decides once it looks.
func TestCheckReadmeClaims_MarkedClaimsOutsideTheCensusSentenceStillBite(t *testing.T) {
	cases := map[string]string{
		"bold":         "\nThe CLI ships **58** CLI subcommands today.\n",
		"totality cue": "\nEvery answer is checked across 21 analyzers.\n",
		"heading":      "\n## All 58 CLI subcommands\n",
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			rep := CheckReadmeClaims(fixtureReadme+add, fixtureCensus())
			if rep.Pass() {
				t.Fatalf("MUTATION DID NOT BITE: a marked, wrong claim (%s) passed", name)
			}
		})
	}
}

// TestCheckReadmeClaims_DuplicateCategoryInDecomposition covers the
// duplicate-category branch (round-1 review, minor n4): naming a category twice
// in one enumeration hides a second, contradictory count.
func TestCheckReadmeClaims_DuplicateCategoryInDecomposition(t *testing.T) {
	mutated := strings.Replace(fixtureReadme, "23 parsers,", "23 parsers, 12 parsers,", 1)
	rep := CheckReadmeClaims(mutated, fixtureCensus())
	if rep.Pass() {
		t.Fatal("MUTATION DID NOT BITE: a category named twice in one decomposition passed")
	}
	if want := `names category "parser" twice in one decomposition`; !strings.Contains(strings.Join(rep.Violations, "\n"), want) {
		t.Errorf("want violation %q, got:\n%s", want, strings.Join(rep.Violations, "\n"))
	}
}

// TestCheckReadmeClaims_SecondCensusSentence covers round-1 review minor n3: the
// gate sums exactly one decomposition, so a second census sentence would go
// unverified. It is a failure, not a silent skip (AC-5).
func TestCheckReadmeClaims_SecondCensusSentence(t *testing.T) {
	mutated := fixtureReadme + "\nThe appendix also counts **173** capabilities: **59** CLI subcommands and the rest. Done.\n"
	rep := CheckReadmeClaims(mutated, fixtureCensus())
	if rep.Pass() {
		t.Fatal("MUTATION DID NOT BITE: a second, unverified census sentence passed")
	}
	if want := "a second capability census sentence"; !strings.Contains(strings.Join(rep.Violations, "\n"), want) {
		t.Errorf("want violation %q, got:\n%s", want, strings.Join(rep.Violations, "\n"))
	}
}

// TestMaskFencedBlocks pins the masking contract the line numbers depend on:
// fenced content is blanked, byte offsets and line count are preserved exactly.
func TestMaskFencedBlocks(t *testing.T) {
	fence := "```"
	in := "before 22 analyzers\n" + fence + "sh\nover 21 analyzers\n" + fence + "\nafter 22 analyzers\n"
	got := maskFencedBlocks(in)
	if len(got) != len(in) {
		t.Fatalf("mask changed the length: %d != %d", len(got), len(in))
	}
	if strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Fatalf("mask changed the line count")
	}
	if strings.Contains(got, "over 21 analyzers") {
		t.Errorf("fenced content survived masking: %q", got)
	}
	if !strings.Contains(got, "before 22 analyzers") || !strings.Contains(got, "after 22 analyzers") {
		t.Errorf("unfenced content was masked: %q", got)
	}
}
