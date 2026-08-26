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
//	(1) COUNTS — every "<N> <label>" CENSUS CLAIM in readme.md equals the
//	    manifest's count for that category, wherever in the file it appears;
//	    and every "<N> capabilities" census claim equals the manifest's total.
//	    "Census claim" is defined by the MATCH DOMAIN below.
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
// MATCH DOMAIN — WHERE a number counts as a claim (AC-7, round 2). A number is
// a CENSUS CLAIM, and therefore mechanically decidable from the manifest, only
// where readme.md MARKS it as one. Four marks, two of them structural:
//
//	(a) inside the capability census sentence itself — hard-gated, always;
//	(b) inside a markdown heading ("## The whole surface: 173 capabilities") —
//	    hard-gated, always: a heading that states a count is a headline census;
//	(c) written in BOLD — "**59** CLI subcommands". This is the readme's own
//	    convention for census numbers, and it is the opt-in for a new one;
//	(d) immediately after a TOTALITY CUE — all / over / across / counts /
//	    a total of — as in "`analyze` over 22 analyzers".
//
// Numbers inside FENCED CODE BLOCKS are never claims and are masked out before
// anything is matched: SW-214 made cited real command output canonical in this
// file, and a tool's own output is evidence, not an assertion by the author.
//
// WHY the domain is narrowed rather than the assertions (round-1 review,
// MAJOR-1). Round 1 matched "<N> <label>" ANYWHERE, so it ruled on sentences
// the manifest cannot decide, and printed "claims 3 analyzers" about an author
// who claimed nothing of the sort. Three ordinary, CORRECT edits tripped it:
// "most people only ever touch 3 analyzers" (a SUBSET, not a census), a fenced
// real output reading "12 analyzers enabled (of 22 registered)" (EVIDENCE), and
// "the `surfaces/` layer holds 8 surfaces" — a fact about Go PACKAGES that
// merely shares a word with the manifest's `surface` category, and the
// chartered evidence sentence of the very next story in this chain. None of the
// three is mechanically decidable from the manifest, so gating them was outside
// AC-7, not merely noisy. Narrowing WHERE the gate looks costs it nothing: all
// three assertions above are unchanged and every kill test still bites.
//
// SCOPE (AC-7). Prose claims — tier language, "the rest are Labs", positioning,
// performance figures, anything not mechanically decidable from the manifest —
// are NOT checked, and both the PASS and the FAIL output state the limit as it
// actually is (the eight labels AND the four marks), so that a green run is
// never read as "readme.md is verified". Backlog :16 keeps its prose half open
// with its original reasoning intact. In particular the "the rest are Labs"
// clause is deliberately NOT gated as a tier assertion: it is over-broad by
// exactly one row and filed as an open minor, and pinning it here would encode
// the defect.
//
// FAIL CLOSED (AC-5). A missing or unparseable manifest, a manifest with no
// capabilities or a row with no category, an unreadable readme.md, a readme
// with no census sentence, a readme with MORE THAN ONE census sentence, or an
// enumeration this parser cannot delimit are all ERRORS. None of them is a
// skip.

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
	// totalClaimRE matches a "<N> capabilities" claim. Group 1 is the opening
	// bold marker (one of the four marks that make a number a census claim),
	// group 2 the number.
	totalClaimRE = regexp.MustCompile(`(\*{0,2})(\d+)\*{0,2}\s+capabilities\b`)
	// censusAnchorRE matches the census sentence's opening, which introduces
	// the decomposition this gate sums and checks for completeness.
	censusAnchorRE = regexp.MustCompile(`counts\s+\*{0,2}(\d+)\*{0,2}\s+capabilities:`)
	// categoryClaimRE matches "<N> <category label>", tolerating bold markers
	// and a line wrap inside a multi-word label. Groups: 1 bold marker,
	// 2 number, 3 label.
	categoryClaimRE = buildCategoryClaimRE()
	// totalityCueRE matches the text immediately BEFORE a number when that
	// number is being presented as a complete count. Deliberately short and
	// closed: "over 22 analyzers" is a census, "touch 3 analyzers" is not.
	totalityCueRE = regexp.MustCompile(`(?i)\b(all|over|across|counts|total of|totall?ing)\s+$`)
	// headingRE matches an ATX markdown heading line.
	headingRE = regexp.MustCompile(`^ {0,3}#{1,6}\s`)
	// sentenceEndRE is the census sentence's terminator: a '.' followed by
	// whitespace and a capital, or a '.' closing the paragraph. NOT a bare '.'
	// — see enumerationEnd.
	sentenceEndRE = regexp.MustCompile(`\.(?:\s+[A-Z]|\s*$)`)
	// paragraphBreakRE ends the markdown paragraph holding the census sentence.
	paragraphBreakRE = regexp.MustCompile(`\n[ \t]*\n`)
	// fenceRE opens or closes a fenced code block.
	fenceRE = regexp.MustCompile("^(```+|~~~+)")
)

// censusLabels lists the readme labels this gate understands, sorted, for the
// scope note — AC-7 has to state the limit as it really is.
func censusLabels() string {
	seen := map[string]bool{}
	out := make([]string, 0, len(readmeCategoryLabels))
	for l := range readmeCategoryLabels {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

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
	return regexp.MustCompile(`(\*{0,2})(\d+)\*{0,2}\s+(` + strings.Join(alts, "|") + `)\b`)
}

// CheckReadmeClaims compares every countable capability claim in readme.md
// against the manifest census. See the package-level comment for the three
// assertions and the scope limit.
func CheckReadmeClaims(readme string, census Census) ReadmeClaimsReport {
	rep := ReadmeClaimsReport{Decomposition: map[string]int{}}

	// Fenced code blocks hold cited command OUTPUT, not authorial claims. Mask
	// them first — byte-for-byte, so every offset and line number below is
	// still an offset into the real readme.md.
	doc := maskFencedBlocks(readme)

	// The census sentence is located first because it is the hard-gated region
	// of the match domain: everything inside it is a claim by construction.
	anchors := censusAnchorRE.FindAllStringSubmatchIndex(doc, -1)
	var (
		censusFrom, censusTo int
		anchorLine           int
		enumeration          string
		enumFrom             int
		delimited            bool
	)
	if len(anchors) > 0 {
		a := anchors[0]
		censusFrom, censusTo = a[0], a[1]
		anchorLine = lineOf(doc, a[0])
		enumFrom = a[1]
		if end, ok := enumerationEnd(doc[a[1]:]); ok {
			enumeration, delimited = doc[a[1]:a[1]+end], true
			censusTo = a[1] + end
		}
	}

	// ---- (1) COUNTS: every MARKED claim in the document (see MATCH DOMAIN). ----
	for _, loc := range totalClaimRE.FindAllStringSubmatchIndex(doc, -1) {
		if !isCensusClaim(doc, loc[0], doc[loc[2]:loc[3]], censusFrom, censusTo) {
			continue
		}
		claimed, _ := strconv.Atoi(doc[loc[4]:loc[5]])
		if claimed != census.Total {
			rep.violate(doc, loc[0], "claims %d capabilities in total; %s has %d", claimed, MatrixJSONPath, census.Total)
		}
	}
	for _, loc := range categoryClaimRE.FindAllStringSubmatchIndex(doc, -1) {
		if !isCensusClaim(doc, loc[0], doc[loc[2]:loc[3]], censusFrom, censusTo) {
			continue
		}
		claimed, _ := strconv.Atoi(doc[loc[4]:loc[5]])
		label := normalizeLabel(doc[loc[6]:loc[7]])
		category := readmeCategoryLabels[label]
		if real, ok := census.ByCategory[category]; !ok {
			rep.violate(doc, loc[0], "claims %d %s, but %s has no %q rows at all", claimed, label, MatrixJSONPath, category)
		} else if claimed != real {
			rep.violate(doc, loc[0], "claims %d %s; %s has %d", claimed, label, MatrixJSONPath, real)
		}
	}

	// ---- (2) SUM and (3) SET COMPLETENESS, over the census sentence. ----
	if len(anchors) == 0 {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s: no capability census sentence found (expected %q) — this gate cannot verify the decomposition and refuses to pass silently",
			ReadmePath, `counts <N> capabilities:`))
		return rep
	}
	// One decomposition is summed and set-checked; a second census sentence
	// would go unverified, so it is a failure rather than a silent skip.
	for _, extra := range anchors[1:] {
		rep.violate(doc, extra[0], "a second capability census sentence (the first is at %s:%d) — this gate sums exactly one decomposition; keep one census sentence, or the second one is never verified",
			ReadmePath, anchorLine)
	}
	rep.Total, _ = strconv.Atoi(doc[anchors[0][2]:anchors[0][3]])

	if !delimited {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s:%d: the capability census sentence is not terminated by '.' followed by a capital or the end of its paragraph — its decomposition cannot be delimited",
			ReadmePath, anchorLine))
		return rep
	}

	sum := 0
	for _, loc := range categoryClaimRE.FindAllStringSubmatchIndex(enumeration, -1) {
		claimed, _ := strconv.Atoi(enumeration[loc[4]:loc[5]])
		category := readmeCategoryLabels[normalizeLabel(enumeration[loc[6]:loc[7]])]
		if _, dup := rep.Decomposition[category]; dup {
			rep.violate(doc, enumFrom+loc[0], "names category %q twice in one decomposition", category)
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

// isCensusClaim reports whether the number matched at off is MARKED as a census
// claim: inside the census sentence [censusFrom, censusTo), inside a markdown
// heading, written in bold, or immediately after a totality cue. bold is the
// match's leading bold-marker capture. See MATCH DOMAIN in the package comment.
func isCensusClaim(doc string, off int, bold string, censusFrom, censusTo int) bool {
	if censusTo > censusFrom && off >= censusFrom && off < censusTo {
		return true // (a) the census sentence itself
	}
	lineStart := strings.LastIndex(doc[:off], "\n") + 1
	if headingRE.MatchString(doc[lineStart:]) {
		return true // (b) a heading that states a count
	}
	if bold == "**" {
		return true // (c) the readme's bold convention for census numbers
	}
	return totalityCueRE.MatchString(doc[lineStart:off]) // (d) a totality cue
}

// enumerationEnd delimits the census sentence's enumeration within rest (the
// text following the anchor), returning the offset just past its terminator.
//
// The terminator is a SENTENCE end — a '.' followed by whitespace and a capital,
// or a '.' closing the markdown paragraph — never a bare '.'. Round 1 cut at the
// first '.' anywhere, so a markdown link ("(docs/cli-reference.md)"), a decimal
// ("0.10") or a version string ("v0.10.0") inside the sentence truncated the
// enumeration: the reviewer's FP3 turned a sentence whose every number was
// correct into seven false violations. readme.md already carries such a link one
// clause before the anchor, and SW-217/SW-218 are both chartered to edit here.
//
// An enumeration with no terminator is NOT silently taken to run to the end of
// the paragraph: ok=false, and the caller reports it (AC-5, fail closed).
func enumerationEnd(rest string) (int, bool) {
	para := rest
	if p := paragraphBreakRE.FindStringIndex(rest); p != nil {
		para = rest[:p[0]]
	}
	if m := sentenceEndRE.FindStringIndex(para); m != nil {
		return m[0] + len("."), true
	}
	return 0, false
}

// maskFencedBlocks blanks every fenced code block, preserving byte offsets and
// line numbers exactly, so that cited command output is not read as an
// authorial claim. An unclosed fence masks to the end of the file — which, if it
// swallows the census sentence, is itself a failure rather than a skip.
func maskFencedBlocks(doc string) string {
	lines := strings.Split(doc, "\n")
	open := ""
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if open == "" {
			if f := fenceRE.FindString(trimmed); f != "" {
				open = f[:3]
				lines[i] = blankOut(ln)
			}
			continue
		}
		lines[i] = blankOut(ln)
		if strings.HasPrefix(trimmed, open) && strings.TrimRight(trimmed, string(open[0])+" \t") == "" {
			open = ""
		}
	}
	return strings.Join(lines, "\n")
}

func blankOut(line string) string {
	b := []byte(line)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func (r *ReadmeClaimsReport) violate(readme string, off int, format string, args ...any) {
	r.Violations = append(r.Violations, fmt.Sprintf("%s:%d: ", ReadmePath, lineOf(readme, off))+fmt.Sprintf(format, args...))
}

// readmeClaimsScopeNote is AC-7 made visible: it is printed on PASS and on
// FAIL, so nobody reads a green run as "the readme is verified".
//
// It states the limit as it actually is — the eight labels AND the four marks —
// rather than the round-1 wording "numbers only", which overstated the reach in
// one direction ("99 subcommands" is not gated: wrong label) and understated it
// in the other (a reader could think every number in the file was checked).
func readmeClaimsScopeNote() string {
	return "  SCOPE (AC-7): numbers only, and only numbers written as `<N> <label>` for one of these\n" +
		"  labels: " + censusLabels() + ".\n" +
		"  A number counts as a claim only where readme.md marks it as one: inside the capability\n" +
		"  census sentence, in a markdown heading, in **bold**, or directly after all/over/across/\n" +
		"  counts/a total of. Numbers inside fenced code blocks, subset statements (\"3 analyzers\n" +
		"  you will use daily\"), counts of anything the manifest does not enumerate, and prose\n" +
		"  claims (tier wording, \"the rest are Labs\", positioning, performance figures) are NOT\n" +
		"  checked — a green readme-claims run does not mean readme.md is verified.\n"
}

// Format renders a deterministic, CI-log-shaped report in the style of the
// other cmd/coverage checks.
func (r ReadmeClaimsReport) Format() string {
	var b strings.Builder
	if r.Pass() {
		fmt.Fprintf(&b, "readme-claims check PASS — %s's capability counts match %s (%s = %d over %d categories).\n",
			ReadmePath, MatrixJSONPath, r.decompositionString(), r.Total, len(r.Decomposition))
		b.WriteString(readmeClaimsScopeNote())
		return b.String()
	}
	fmt.Fprintf(&b, "readme-claims check FAILED — %s contradicts %s:\n\n", ReadmePath, MatrixJSONPath)
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "    - %s\n", v)
	}
	fmt.Fprintf(&b, "\n  Fix %s to match the manifest (the manifest is generated from the CI-enforced\n  matrix and wins), then re-run: go run ./cmd/coverage -check\n", ReadmePath)
	b.WriteString(readmeClaimsScopeNote())
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
