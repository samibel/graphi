package evidence

import (
	"path"
	"regexp"
	"strings"
)

// ── The citation contract (SW-205) ──────────────────────────────────────────
//
// A *citation* is the machine-checkable half of an evidence claim: the thing a
// row or a record points at so a reader can go and look. This file declares —
// in one place, deliberately, so no rule is inferred from a regexp buried in a
// caller — what counts as a citation, which shapes exist, and which shapes the
// gate stands behind.
//
// WHAT THE GATE STANDS BEHIND
//
//	repo-path    a repository-rooted path        → must exist at HEAD (AC-2),
//	                                               its recorded sha must be the
//	                                               sha of those bytes (AC-1),
//	                                               and the worktree copy must be
//	                                               committed (AC-3)
//	test-symbol  path/to/file_test.go::TestName  → TestName must be declared as
//	                                               a func in that file (AC-5)
//
// WHAT THE GATE DOES NOT STAND BEHIND — classified, counted, reported, never
// silently swallowed (AC-4):
//
//	doc-anchor   path.md#heading-slug            → the path half is verified,
//	                                               the #anchor half is not
//	external     https://…                       → not resolvable without egress
//	template     corpus/fixtures/hero-<lang>     → a placeholder, not a path
//	command      cmd/coverage -check             → an invocation, not an artifact
//	prose        "matching test files"           → free text
//	unclassified anything path-shaped that fits  → a VIOLATION, not a pass
//	             none of the above
//
// LINE NUMBERS ARE NOT PART OF THE CONTRACT (AC-8, decision (b)).
//
// A `file.go:NNN` citation is a *reading aid*; the gate does not check it and
// callers must not read a green gate as evidence that a line number is right.
// The decision, and why it is not (a):
//
//   - The only mechanically available check is "the file still has at least NNN
//     lines". That is vacuous against every instance of the defect it would be
//     written for. Five `GA-LANG-<lang>-G3` rows cited `line 38` for a guard that
//     sat at line 39; the file has hundreds of lines, so a bounds check passes on
//     all five. A gate that cannot fail on the defect that motivated it is worse
//     than no gate: it manufactures confidence.
//   - Verifying that line NNN still *contains the thing meant* is semantic, and
//     semantic verification is this story's declared out-of-scope.
//   - The replacement is the `::Symbol` form, which IS verified (AC-5). Where a
//     record needs to point at code and have the gate stand behind the pointer,
//     it cites `path/to/file.go::SymbolName`, not `path/to/file.go:NNN`.
//
// KNOWN LIMIT — a citation can resolve and still be wrong (recorded so the next
// reader does not over-trust a green gate). This gate proves that a cited thing
// EXISTS and that recorded bytes ARE the cited bytes. It cannot prove the cited
// thing supports the claim made about it, and — the sharper variant — it cannot
// tell a citation of the right rule from a citation of the wrong-but-real rule.
// "Per ADR 0008 D6" in `docs/decisions/2026-08-parity-candidate-move-adr0013.md`
// names a genuine D6 in a genuine ADR; it is simply not the D6 that was meant
// (ADR 0008's D6 is the overload binding rule; the never-rewrite discipline is
// `docs/plan/2026-08-wave0-handoff-v1.md` §6). Existence checks and sha
// comparison both pass on that citation. SW-206 covers part of the semantic
// class; this variant is covered by neither and is a standing limit of the gate.

// CitationKind is the declared classification of one citation. Classification is
// purely syntactic — what shape is this? — and is kept separate from resolution
// (does it exist?) so the report can say "we skipped 12 external URLs" instead of
// silently counting them green.
type CitationKind string

const (
	// KindRepoPath is a repository-rooted path (file or directory). Verified.
	KindRepoPath CitationKind = "repo-path"
	// KindTestSymbol is `path/to/file.go::SymbolName`. Verified.
	KindTestSymbol CitationKind = "test-symbol"
	// KindDocAnchor is `path.md#anchor`. The path half is verified; the anchor is not.
	KindDocAnchor CitationKind = "doc-anchor"
	// KindExternal is an absolute URL. Not verified — the gate makes no network calls.
	KindExternal CitationKind = "external-url"
	// KindTemplate contains a `<placeholder>` and so names no single artifact.
	KindTemplate CitationKind = "template"
	// KindCommand is an invocation ("cmd/coverage -check"), not an artifact.
	KindCommand CitationKind = "command"
	// KindProse is free text carrying no citation.
	KindProse CitationKind = "prose"
	// KindUnclassified is path-shaped text that matches no declared rule. A
	// violation, never a pass: an unreadable citation is an unbacked one.
	KindUnclassified CitationKind = "unclassified"
)

// Verified reports whether this kind is one the gate resolves. Kinds that are not
// verified are still classified and reported (AC-4) — they are simply not counted
// as evidence.
func (k CitationKind) Verified() bool {
	return k == KindRepoPath || k == KindTestSymbol || k == KindDocAnchor
}

// Citation is one classified citation with the source text it came from.
type Citation struct {
	Kind   CitationKind
	Raw    string // the text as written, for the violation message
	Path   string // repo-relative path, for KindRepoPath/KindTestSymbol/KindDocAnchor
	Symbol string // the `::Symbol` half, for KindTestSymbol
	Anchor string // the `#anchor` half, for KindDocAnchor
	Doc    string // governed document this came from ("" for evidence-index rows)
	Line   int    // 1-based line in Doc ("" / 0 for evidence-index rows)
}

// ── Declared rule set ───────────────────────────────────────────────────────

// CitationRoots are the repository top-level directories a citation may be rooted
// at. A path-shaped token that starts anywhere else is not a citation into THIS
// repository — it is an example, an external repo, or a portfolio path — and the
// gate says so rather than guessing. Derived from `git ls-tree --name-only HEAD`
// and deliberately checked in rather than discovered at runtime, so adding a new
// top-level directory to the repo is a conscious edit here.
var CitationRoots = []string{
	".github/", "bench/", "cmd/", "core/", "corpus/", "docs/", "engine/",
	"extensions/", "internal/", "packaging/", "scripts/", "site/", "surfaces/",
	"web/",
}

// CitationExtensions are the file extensions a path citation may end in. The
// allowlist exists to keep package-qualified Go references (`surfaces/mcp.Stable
// Operations`) out of the citation set: they are path-shaped and end in a dot
// segment, but `.StableOperations` is not a file extension.
var CitationExtensions = []string{
	".c", ".cc", ".cpp", ".cs", ".go", ".h", ".hpp", ".java", ".js", ".json",
	".kt", ".lua", ".md", ".mod", ".php", ".ps1", ".py", ".rb", ".rs", ".sh",
	".sql", ".sum", ".toml", ".ts", ".tsx", ".txt", ".work", ".yaml", ".yml",
}

// GovernedDocGlobs is the declared, checked-in set of prose records the citation
// sweep covers (AC-6). It is a literal list of globs, not a walk: widening the
// sweep is an edit here and shows up in review.
var GovernedDocGlobs = []string{
	"docs/rc/*.md",
	"docs/decisions/*.md",
	"docs/adr/*.md",
}

// GovernedDocExclusions are governed-glob matches that are deliberately NOT swept.
// docs/rc/evidence-index.md is GENERATED from docs/rc/evidence-index.yaml, whose
// rows are checked directly; sweeping it too would double-report every row and
// would report the forward-looking `next_action:` text ("Cite docs/rc/parity-
// classes-css.yaml …") as a claim, which it is not.
var GovernedDocExclusions = []string{
	"docs/rc/evidence-index.md",
}

// StaleSectionMarkers confer the AC-7 exemption. A citation inside a section whose
// heading text STARTS WITH one of these markers is not required to resolve — such
// a section exists precisely to record what is superseded or what was false, and
// requiring its citations to resolve would demand the record rewrite the spec's
// Boundaries forbid.
//
// Matched structurally and position-anchored: the heading's `#` run and any
// leading `N.` numbering are stripped, then the remainder must have one of these
// as its first word. "## 9. Change-control and the STALE rule" is therefore NOT
// exempt (STALE is not its first word), while "# Superseded measurement — …" is.
var StaleSectionMarkers = []string{
	"SUPERSEDED", "Superseded",
	"STALE",
	"CORRECTION", "Correction",
	"[SUPERSEDED]", "[STALE]",
}

// ── Grammar ─────────────────────────────────────────────────────────────────

var (
	// codeSpan matches a markdown inline code span. A prose citation is only a
	// citation when it is written as code — that is the declared grammar, and it
	// is what keeps ordinary sentences out of the citation set.
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	// citationToken is the shape a code span must have to be a citation at all:
	// a path-ish run, optionally `::Symbol`, optionally `#anchor`.
	citationToken = regexp.MustCompile(`^([A-Za-z0-9_.{},/+*-]+?)(#[A-Za-z0-9._-]+)?(::[A-Za-z0-9_]+)?$`)
	// braceGroup drives the declared brace expansion: `parity-classes{,-jvm}.yaml`
	// cites two files, and the records already write citations that way.
	braceGroup = regexp.MustCompile(`\{([^{}]*)\}`)
	// trailingNote strips an explanatory parenthetical from an evidence_uri
	// segment: "docs/rc/parity-matrix-real-repo.md (19/19 section)".
	trailingNote = regexp.MustCompile(`\s*\([^()]*\)\s*$`)
	// placeholder marks a template: "<lang>" is not a path, and neither is a glob
	// ("corpus/hero/*.yaml"). Both name a SET of artifacts, and a set has no sha.
	placeholder = regexp.MustCompile(`<[A-Za-z0-9_-]+>|\*`)
	// commandFlag marks an invocation: a space followed by a -flag.
	commandFlag = regexp.MustCompile(`\s-[A-Za-z]`)
)

// ExpandBraces applies the declared brace expansion to a citation token, in
// source order and without deduplication, so `a{,-b}.yaml` yields `a.yaml` then
// `a-b.yaml`.
func ExpandBraces(s string) []string {
	m := braceGroup.FindStringSubmatchIndex(s)
	if m == nil {
		return []string{s}
	}
	var out []string
	for _, alt := range strings.Split(s[m[2]:m[3]], ",") {
		out = append(out, ExpandBraces(s[:m[0]]+alt+s[m[1]:])...)
	}
	return out
}

// hasCitationRoot reports whether p is rooted at a declared repository root.
func hasCitationRoot(p string) bool {
	for _, r := range CitationRoots {
		if strings.HasPrefix(p, r) {
			return true
		}
	}
	return false
}

// hasCitationExtension reports whether p ends in a declared file extension.
func hasCitationExtension(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	for _, e := range CitationExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

// looksLikePath is the "is this path-shaped at all" test used to decide whether an
// unclassifiable evidence_uri segment is a violation (path-shaped but unreadable)
// or merely prose (AC-4).
func looksLikePath(s string) bool {
	return strings.Contains(s, "/") && !strings.ContainsAny(s, " \t")
}

// ClassifyURI splits one evidence-index `evidence_uri` value into its declared
// citations. The declared segmentation, in order:
//
//  1. everything from the first " — " (spaced em dash) is a note, not a citation;
//  2. the remainder splits on " + " into segments;
//  3. each segment loses a trailing " (...)" parenthetical;
//  4. each segment is brace-expanded, then classified.
//
// An empty or whitespace-only URI yields no citations at all (the pre-existing
// non-empty rule in Check covers that case).
func ClassifyURI(uri string) []Citation {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil
	}
	if i := strings.Index(uri, " — "); i >= 0 {
		uri = uri[:i]
	}
	var out []Citation
	for _, seg := range strings.Split(uri, " + ") {
		seg = strings.TrimSpace(trailingNote.ReplaceAllString(seg, ""))
		if seg == "" {
			continue
		}
		out = append(out, classifySegment(seg)...)
	}
	return out
}

// classifySegment applies the declared rule set to one segment. Order matters and
// is part of the contract: external URL, then template, then command, then path.
func classifySegment(seg string) []Citation {
	switch {
	case strings.HasPrefix(seg, "http://"), strings.HasPrefix(seg, "https://"):
		return []Citation{{Kind: KindExternal, Raw: seg}}
	case placeholder.MatchString(seg):
		return []Citation{{Kind: KindTemplate, Raw: seg}}
	case commandFlag.MatchString(seg):
		return []Citation{{Kind: KindCommand, Raw: seg}}
	}
	var out []Citation
	for _, expanded := range ExpandBraces(seg) {
		out = append(out, classifyOne(seg, expanded))
	}
	return out
}

// classifyOne classifies a single brace-expanded token. raw is the text as it was
// written (pre-expansion), so a violation message quotes what a human will find.
func classifyOne(raw, tok string) Citation {
	m := citationToken.FindStringSubmatch(tok)
	if m == nil {
		if looksLikePath(tok) {
			return Citation{Kind: KindUnclassified, Raw: raw}
		}
		return Citation{Kind: KindProse, Raw: raw}
	}
	p, anchor, symbol := m[1], strings.TrimPrefix(m[2], "#"), strings.TrimPrefix(m[3], "::")
	trimmed := strings.TrimSuffix(p, "/")
	switch {
	case !hasCitationRoot(trimmed):
		if looksLikePath(tok) {
			return Citation{Kind: KindUnclassified, Raw: raw}
		}
		return Citation{Kind: KindProse, Raw: raw}
	case symbol != "":
		return Citation{Kind: KindTestSymbol, Raw: raw, Path: trimmed, Symbol: symbol}
	case anchor != "":
		return Citation{Kind: KindDocAnchor, Raw: raw, Path: trimmed, Anchor: anchor}
	default:
		return Citation{Kind: KindRepoPath, Raw: raw, Path: trimmed}
	}
}

// ── Governed-document sweep (AC-6 / AC-7) ───────────────────────────────────

// heading is one markdown heading with its level and its exemption status.
type heading struct {
	level  int
	exempt bool
}

// CorrectionMarkers are the subset of StaleSectionMarkers that open a CORRECTION
// banner. A correction banner placed at the HEAD of a record — before any other
// content section — declares the whole record below it as-published, because that
// is what this project's never-rewrite convention means: a correction goes on top
// and the original body stays underneath, false citations and all. Such a banner
// therefore exempts every citation from its own heading to the end of the
// document, and the exempt count is reported so the size of that exemption is
// never invisible.
//
// A correction banner that appears LATER in a record — after real content — is
// scoped to its own section like any other marker. The distinction is structural
// (is this the first content section?) and not a judgement about the prose.
var CorrectionMarkers = []string{"CORRECTION", "Correction"}

// headingMarkerRe strips the `#` run and any `N.` / `N)` numbering so the marker
// test is position-anchored on the heading's first real word.
var headingMarkerRe = regexp.MustCompile(`^#+\s*(?:\d+[.)]\s*)*`)

// isStaleHeading reports whether a markdown heading line carries a declared
// AC-7 exemption marker as its first word.
func isStaleHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	text := strings.TrimSpace(headingMarkerRe.ReplaceAllString(line, ""))
	for _, m := range StaleSectionMarkers {
		if text == m || strings.HasPrefix(text, m+" ") || strings.HasPrefix(text, m+" ") ||
			strings.HasPrefix(text, m+":") || strings.HasPrefix(text, m+" —") {
			return true
		}
	}
	return false
}

// unquoteBlock strips markdown blockquote markers so a heading written inside a
// blockquote still reads as a heading. The convention these records use writes a
// correction banner as "> ## CORRECTION — …" (the whole block is quoted so it
// reads as an inserted annotation); treating that as ordinary prose would make the
// AC-7 marker invisible exactly where it is used most.
func unquoteBlock(line string) string {
	for strings.HasPrefix(line, ">") {
		line = strings.TrimPrefix(line, ">")
		line = strings.TrimPrefix(line, " ")
	}
	return line
}

// isCorrectionHeading reports whether a heading opens a CORRECTION banner.
func isCorrectionHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	text := strings.TrimSpace(headingMarkerRe.ReplaceAllString(line, ""))
	for _, m := range CorrectionMarkers {
		if text == m || strings.HasPrefix(text, m+" ") || strings.HasPrefix(text, m+":") {
			return true
		}
	}
	return false
}

// headingLevel counts the leading '#' run.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || (n < len(line) && line[n] != ' ') {
		return 0
	}
	return n
}

// ScanDocument extracts the declared citations from one governed document's text.
// Citations inside a section carrying a declared AC-7 marker (and inside its
// subsections, which belong to it) are returned with exempt=true rather than
// dropped, so the report can state how many were exempted.
func ScanDocument(doc, text string) (cites []Citation, exempt int) {
	var stack []heading
	exemptNow := false
	sections := 0
	restOfDoc := false
	for i, raw := range strings.Split(text, "\n") {
		line := unquoteBlock(raw)
		if lvl := headingLevel(line); lvl > 0 {
			sections++
			// A correction banner opening the record exempts everything below it.
			if sections <= 2 && isCorrectionHeading(line) {
				restOfDoc = true
			}
			for len(stack) > 0 && stack[len(stack)-1].level >= lvl {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, heading{level: lvl, exempt: isStaleHeading(line)})
			exemptNow = restOfDoc
			for _, h := range stack {
				if h.exempt {
					exemptNow = true
				}
			}
			continue
		}
		for _, m := range codeSpan.FindAllStringSubmatch(raw, -1) {
			for _, c := range classifyCodeSpan(strings.TrimSpace(m[1])) {
				if !c.Kind.Verified() {
					continue
				}
				if exemptNow {
					exempt++
					continue
				}
				c.Doc, c.Line = doc, i+1
				cites = append(cites, c)
			}
		}
	}
	return cites, exempt
}

// classifyCodeSpan classifies one inline code span. Unlike an evidence_uri
// segment, a code span in prose that matches no declared citation shape is simply
// not a citation — prose is full of code spans that name symbols, commands and
// external repos, and calling those unclassified would make the gate unusable.
func classifyCodeSpan(body string) []Citation {
	if body == "" || strings.ContainsAny(body, " \t") {
		return nil
	}
	// A glob or a <placeholder> names a SET of files, not one artifact. Declared
	// as a template here exactly as it is for an evidence_uri segment, so the two
	// entry points cannot drift apart.
	if placeholder.MatchString(body) {
		return []Citation{{Kind: KindTemplate, Raw: body}}
	}
	var out []Citation
	for _, expanded := range ExpandBraces(body) {
		c := classifyOne(body, expanded)
		if !hasCitationExtension(c.Path) {
			continue
		}
		out = append(out, c)
	}
	return out
}
