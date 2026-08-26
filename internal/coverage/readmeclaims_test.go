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
	if !strings.Contains(out, "SCOPE: numbers only") || !strings.Contains(out, "does not mean readme.md is verified") {
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
			if !strings.Contains(out, "SCOPE: numbers only") {
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
