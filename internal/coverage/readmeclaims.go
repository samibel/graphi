package coverage

// SW-216: the README CAPABILITY-CLAIM gate — readme.md's countable capability
// claims, bound to docs/capability-manifest.json.
//
// WHY IT EXISTS. The manifest is generated from the CI-enforced coverage matrix
// and is therefore true by construction; readme.md restates its census in prose
// and was, until this check, unbound. Drift is not hypothetical: on 2026-08-25
// the readme claimed 15 of 59 subcommands, did not know five capabilities that
// shipped in v0.10.0, and carried a binary size wrong since the baseline was
// re-pinned.
//
// WHAT IT ASSERTS — three things, not one. SW-215 shipped a sentence reading
// "counts 173 capabilities: 59, 56, 22, 23, 7 and 5", whose six numbers sum to
// 172: every individual count was correct and the DECOMPOSITION was incomplete,
// silently omitting the single ga-language row. A gate that checked each count
// in isolation would have stayed green on that defect. So:
//
//	(1) COUNTS — every "<N> <label>" claim in readme.md equals the manifest's
//	    count for that category, wherever in the file it appears; and every
//	    "<N> capabilities" equals the manifest's total.
//	(2) SUM — the census sentence's decomposition adds up to the total it
//	    states (and that total equals the manifest's).
//	(3) SET COMPLETENESS — the categories named in the decomposition are
//	    exactly the categories present in the manifest. An eighth category
//	    cannot be silently dropped from the enumeration, and a category with
//	    no readme label at all is a failure, not a skip.
//
// SEAM (SW-216 AC-4, recorded here rather than in a commit message). This
// EXTENDS cmd/coverage -check rather than adding a sixth gate binary.
// context/standards.md warns "Don't extend one measurement instrument to serve
// another" — that warning is about instruments with different measurement
// DOMAINS (internal/evalreport's performance runs vs internal/parityreport's
// reliability matrix, which keep separate change-class lists and schemas on
// purpose). Here the domain is identical: this is the capability census,
// measured by the instrument that already derives it, already loads the
// manifest, and already verifies the manifest is fresh in the same process.
// A parallel binary would duplicate that load, could run against a manifest
// the freshness gate had not blessed, and would conflict with SW-218's AC-6,
// which requires reuse of this seam.
//
// SCOPE (AC-7). NUMBERS ONLY. Prose claims — tier language, "the rest are
// Labs", positioning, anything not mechanically decidable from the manifest —
// are NOT checked, and both the PASS and the FAIL output say so, so that a
// green run is never read as "readme.md is verified". Backlog :16 keeps its
// prose half open with its original reasoning intact. In particular the
// "the rest are Labs" clause is deliberately NOT gated as a tier assertion:
// it is over-broad by exactly one row and filed as an open minor, and pinning
// it here would encode the defect.
//
// FAIL CLOSED (AC-5). A missing or unparseable manifest, a manifest with no
// capabilities or a row with no category, an unreadable readme.md, a readme
// with no census sentence, or an enumeration this parser cannot delimit are
// all ERRORS. None of them is a skip.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ReadmePath is the repo-relative path of the gated document.
const ReadmePath = "readme.md"

// readmeCategoryLabels maps the readme's prose label to a manifest category id.
// Every category present in the manifest must appear here, or the check fails
// (a new category the readme has no vocabulary for is exactly the silent-drop
// case this gate exists to catch).
var readmeCategoryLabels = map[string]string{
	"CLI subcommands": "cli-subcommand",
	"MCP tools":       "mcp-tool",
	"analyzers":       "analyzer",
	"parsers":         "parser",
	"surfaces":        "surface",
	"feature units":   "feature-unit",
	"GA language":     "ga-language",
	"GA languages":    "ga-language",
}

// Census is the capability count per category, plus the total, as read from
// the generated manifest.
type Census struct {
	ByCategory map[string]int
	Total      int
}

// Categories returns the manifest's category ids, sorted.
func (c Census) Categories() []string {
	out := make([]string, 0, len(c.ByCategory))
	for k := range c.ByCategory {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ManifestCensus reads the generated capability manifest and counts its rows by
// category. It fails closed: unparseable JSON, an empty capability list, or a
// row with no category is an error rather than an empty census that would make
// every readme claim vacuously wrong (or, worse, vacuously right).
func ManifestCensus(manifestJSON []byte) (Census, error) {
	var m struct {
		Capabilities []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Census{}, fmt.Errorf("coverage: parse %s: %w", MatrixJSONPath, err)
	}
	if len(m.Capabilities) == 0 {
		return Census{}, fmt.Errorf("coverage: %s lists no capabilities", MatrixJSONPath)
	}
	c := Census{ByCategory: map[string]int{}, Total: len(m.Capabilities)}
	for i, row := range m.Capabilities {
		if strings.TrimSpace(row.Category) == "" {
			return Census{}, fmt.Errorf("coverage: %s capability %d (%q) has no category", MatrixJSONPath, i, row.ID)
		}
		c.ByCategory[row.Category]++
	}
	return c, nil
}

// ReadmeClaimsReport is the outcome of CheckReadmeClaims.
type ReadmeClaimsReport struct {
	// Total is the headline total the readme states (0 if none was found).
	Total int
	// Decomposition is the census sentence's enumeration, category id ->
	// claimed count.
	Decomposition map[string]int
	// Violations, empty on pass, name each contradiction in readme order.
	Violations []string
}

// Pass reports whether readme.md's numbers match the manifest.
func (r ReadmeClaimsReport) Pass() bool { return len(r.Violations) == 0 }

var (
	// totalClaimRE matches any "<N> capabilities" claim, bold markers optional.
	totalClaimRE = regexp.MustCompile(`\*{0,2}(\d+)\*{0,2}\s+capabilities\b`)
	// censusAnchorRE matches the census sentence's opening, which introduces
	// the decomposition this gate sums and checks for completeness.
	censusAnchorRE = regexp.MustCompile(`counts\s+\*{0,2}(\d+)\*{0,2}\s+capabilities:`)
	// categoryClaimRE matches "<N> <category label>", tolerating bold markers
	// and a line wrap inside a multi-word label.
	categoryClaimRE = buildCategoryClaimRE()
)

func buildCategoryClaimRE() *regexp.Regexp {
	labels := make([]string, 0, len(readmeCategoryLabels))
	for l := range readmeCategoryLabels {
		labels = append(labels, l)
	}
	// Longest first: Go's alternation is leftmost-FIRST, so "GA languages" must
	// be offered before "GA language".
	sort.Slice(labels, func(i, j int) bool {
		if len(labels[i]) != len(labels[j]) {
			return len(labels[i]) > len(labels[j])
		}
		return labels[i] < labels[j]
	})
	alts := make([]string, 0, len(labels))
	for _, l := range labels {
		alts = append(alts, strings.ReplaceAll(regexp.QuoteMeta(l), " ", `\s+`))
	}
	return regexp.MustCompile(`\*{0,2}(\d+)\*{0,2}\s+(` + strings.Join(alts, "|") + `)\b`)
}

// CheckReadmeClaims compares every countable capability claim in readme.md
// against the manifest census. See the package-level comment for the three
// assertions and the scope limit.
func CheckReadmeClaims(readme string, census Census) ReadmeClaimsReport {
	rep := ReadmeClaimsReport{Decomposition: map[string]int{}}

	// ---- (1) COUNTS: every claim anywhere in the document. ----
	for _, loc := range totalClaimRE.FindAllStringSubmatchIndex(readme, -1) {
		claimed, _ := strconv.Atoi(readme[loc[2]:loc[3]])
		if claimed != census.Total {
			rep.violate(readme, loc[0], "claims %d capabilities in total; %s has %d", claimed, MatrixJSONPath, census.Total)
		}
	}
	for _, loc := range categoryClaimRE.FindAllStringSubmatchIndex(readme, -1) {
		claimed, _ := strconv.Atoi(readme[loc[2]:loc[3]])
		label := normalizeLabel(readme[loc[4]:loc[5]])
		category := readmeCategoryLabels[label]
		if real, ok := census.ByCategory[category]; !ok {
			rep.violate(readme, loc[0], "claims %d %s, but %s has no %q rows at all", claimed, label, MatrixJSONPath, category)
		} else if claimed != real {
			rep.violate(readme, loc[0], "claims %d %s; %s has %d", claimed, label, MatrixJSONPath, real)
		}
	}

	// ---- (2) SUM and (3) SET COMPLETENESS, over the census sentence. ----
	anchor := censusAnchorRE.FindStringSubmatchIndex(readme)
	if anchor == nil {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s: no capability census sentence found (expected %q) — this gate cannot verify the decomposition and refuses to pass silently",
			ReadmePath, `counts <N> capabilities:`))
		return rep
	}
	rep.Total, _ = strconv.Atoi(readme[anchor[2]:anchor[3]])
	anchorLine := lineOf(readme, anchor[0])

	rest := readme[anchor[1]:]
	end := strings.Index(rest, ".")
	if end < 0 {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s:%d: the capability census sentence is not terminated by '.' — its decomposition cannot be delimited",
			ReadmePath, anchorLine))
		return rep
	}
	enumeration := rest[:end]

	sum := 0
	for _, loc := range categoryClaimRE.FindAllStringSubmatchIndex(enumeration, -1) {
		claimed, _ := strconv.Atoi(enumeration[loc[2]:loc[3]])
		category := readmeCategoryLabels[normalizeLabel(enumeration[loc[4]:loc[5]])]
		if _, dup := rep.Decomposition[category]; dup {
			rep.violate(readme, anchor[1]+loc[0], "names category %q twice in one decomposition", category)
			continue
		}
		rep.Decomposition[category] = claimed
		sum += claimed
	}
	if sum != rep.Total {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s:%d: the decomposition sums to %d but the sentence states %d — %d %s unaccounted for",
			ReadmePath, anchorLine, sum, rep.Total, absInt(rep.Total-sum), plural(absInt(rep.Total-sum), "capability", "capabilities")))
	}

	claimed := make([]string, 0, len(rep.Decomposition))
	for c := range rep.Decomposition {
		claimed = append(claimed, c)
	}
	for _, missing := range setDiff(census.Categories(), claimed) {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s:%d: the decomposition omits category %q (%d %s in %s) — every manifest category must be named",
			ReadmePath, anchorLine, missing, census.ByCategory[missing],
			plural(census.ByCategory[missing], "row", "rows"), MatrixJSONPath))
	}
	for _, extra := range setDiff(claimed, census.Categories()) {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s:%d: the decomposition names category %q, which %s does not have",
			ReadmePath, anchorLine, extra, MatrixJSONPath))
	}
	return rep
}

func (r *ReadmeClaimsReport) violate(readme string, off int, format string, args ...any) {
	r.Violations = append(r.Violations, fmt.Sprintf("%s:%d: ", ReadmePath, lineOf(readme, off))+fmt.Sprintf(format, args...))
}

// readmeClaimsScopeNote is AC-7 made visible: it is printed on PASS and on
// FAIL, so nobody reads a green run as "the readme is verified".
const readmeClaimsScopeNote = "  SCOPE: numbers only. Prose claims (tier wording, \"the rest are Labs\", positioning, performance figures) are NOT checked — a green readme-claims run does not mean readme.md is verified.\n"

// Format renders a deterministic, CI-log-shaped report in the style of the
// other cmd/coverage checks.
func (r ReadmeClaimsReport) Format() string {
	var b strings.Builder
	if r.Pass() {
		fmt.Fprintf(&b, "readme-claims check PASS — %s's capability counts match %s (%s = %d over %d categories).\n",
			ReadmePath, MatrixJSONPath, r.decompositionString(), r.Total, len(r.Decomposition))
		b.WriteString(readmeClaimsScopeNote)
		return b.String()
	}
	fmt.Fprintf(&b, "readme-claims check FAILED — %s contradicts %s:\n\n", ReadmePath, MatrixJSONPath)
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "    - %s\n", v)
	}
	fmt.Fprintf(&b, "\n  Fix %s to match the manifest (the manifest is generated from the CI-enforced\n  matrix and wins), then re-run: go run ./cmd/coverage -check\n", ReadmePath)
	b.WriteString(readmeClaimsScopeNote)
	return b.String()
}

func (r ReadmeClaimsReport) decompositionString() string {
	cats := make([]string, 0, len(r.Decomposition))
	for c := range r.Decomposition {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool {
		if r.Decomposition[cats[i]] != r.Decomposition[cats[j]] {
			return r.Decomposition[cats[i]] > r.Decomposition[cats[j]]
		}
		return cats[i] < cats[j]
	})
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		parts = append(parts, strconv.Itoa(r.Decomposition[c]))
	}
	return strings.Join(parts, "+")
}

func normalizeLabel(s string) string { return strings.Join(strings.Fields(s), " ") }

func lineOf(text string, off int) int { return 1 + strings.Count(text[:off], "\n") }

func setDiff(a, b []string) []string {
	have := make(map[string]bool, len(b))
	for _, s := range b {
		have[s] = true
	}
	var out []string
	for _, s := range a {
		if !have[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
