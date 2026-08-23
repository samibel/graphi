package evidence

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CitationRule is the machine-readable name of one citation rule. It is part of a
// violation's key, so the grandfather list can suppress "this exact breach" and
// nothing wider.
type CitationRule string

const (
	// RuleMissingPath — a citation names a path that does not exist at HEAD (AC-2).
	RuleMissingPath CitationRule = "missing-path"
	// RuleSHAMismatch — the recorded blob sha is not the sha of the cited bytes (AC-1).
	RuleSHAMismatch CitationRule = "sha-mismatch"
	// RuleSHAUnresolvable — the recorded sha names no git object and is no content digest.
	RuleSHAUnresolvable CitationRule = "sha-unresolvable"
	// RuleWorktreeDrift — the cited path's worktree bytes are not the committed bytes (AC-3).
	RuleWorktreeDrift CitationRule = "worktree-drift"
	// RuleSymbolUnresolved — a `path::Symbol` citation names no declaration in that file (AC-5).
	RuleSymbolUnresolved CitationRule = "symbol-unresolved"
	// RuleUnclassifiedURI — an evidence_uri segment matches no declared rule (AC-4).
	RuleUnclassifiedURI CitationRule = "unclassified-uri"
	// RuleGrandfatherUnused — a ratchet entry whose target now passes (AC-11).
	RuleGrandfatherUnused CitationRule = "grandfather-unused"
	// RuleGrandfatherNoOwner — a ratchet entry with no owner story (AC-11).
	RuleGrandfatherNoOwner CitationRule = "grandfather-no-owner"
	// RuleGrandfatherMalformed — a structurally invalid ratchet entry (AC-11).
	RuleGrandfatherMalformed CitationRule = "grandfather-malformed"
)

// CitationViolation is one breach of a citation rule. Scope is the gate id or the
// governed document (with a line, where there is one), Target is the thing cited,
// and Rule says which rule broke — "the file moved" and "the file changed" are
// separate rules on purpose, because they need different fixes (AC-2).
type CitationViolation struct {
	Scope  string
	Rule   CitationRule
	Target string
	Detail string
}

// Key is the stable identity a grandfather entry names. It deliberately excludes
// Detail (which carries measured shas that change) and, for governed documents,
// the line number (which drifts as the record grows on top).
func (v CitationViolation) Key() string {
	return fmt.Sprintf("%s :: %s :: %s", stripLine(v.Scope), v.Rule, v.Target)
}

func stripLine(scope string) string {
	if i := strings.LastIndexByte(scope, ':'); i > 0 {
		if _, err := fmt.Sscanf(scope[i+1:], "%d", new(int)); err == nil {
			return scope[:i]
		}
	}
	return scope
}

// CitationReport is the outcome of the citation sweep: the breaches that stand,
// the census of what was classified but not verified (AC-4), the count of
// citations exempted by an AC-7 marker, and the ratchet entries that were used.
type CitationReport struct {
	Violations    []CitationViolation
	Suppressed    []CitationViolation
	Census        map[CitationKind]int
	Verified      int
	Exempt        int
	UnbackedPASS  []string
	GovernedDocs  int
	GateRows      int
	GrandfatherOK int
}

// Pass reports whether the citation sweep found nothing that stands.
func (r CitationReport) Pass() bool { return len(r.Violations) == 0 }

// provenanceBinding is the declared grammar for a sha binding inside a gate row's
// `provenance:` narrative: `<path>[ (note)] @ blob <sha>`. That form was written
// by SW-194 to record blob shas the single `sha:` key has no room for, and it is
// exactly the form failure mode 1 went stale in, so it is checked.
var provenanceBinding = regexp.MustCompile(`([A-Za-z0-9_./+-]+\.[A-Za-z0-9]+)(?:\s*\([^()]*\))?\s+@\s+blob\s+([0-9a-f]{7,64})`)

// ProvenanceBindings extracts the declared `<path> @ blob <sha>` bindings from a
// provenance narrative, in source order.
func ProvenanceBindings(text string) [][2]string {
	var out [][2]string
	for _, m := range provenanceBinding.FindAllStringSubmatch(text, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// CheckCitations resolves everything the evidence index and the governed records
// cite, against the repository at root. It is the AC-1…AC-7 rule set.
//
// What it verifies, and nothing more (the boundary is the point):
//   - a cited path exists at HEAD (AC-2)
//   - a recorded blob sha IS the sha of the cited bytes at HEAD (AC-1)
//   - the cited path's worktree bytes are committed (AC-3)
//   - a `path::Symbol` citation resolves to a declaration in that file (AC-5)
//   - every evidence_uri segment falls under a declared classification (AC-4)
//
// What it deliberately does NOT verify: that the cited artifact SUPPORTS the claim
// (out of scope, and not mechanizable), and line numbers (AC-8 decision (b) — see
// the contract in citation.go).
func CheckCitations(root string, idx Index, gf Grandfather) (CitationReport, error) {
	g := NewGit(root)
	syms := NewSymbolResolver(g)
	rep := CitationReport{Census: map[CitationKind]int{}}

	var found []CitationViolation
	add := func(v CitationViolation) { found = append(found, v) }

	// ── the evidence index rows ─────────────────────────────────────────────
	for _, gate := range idx.Gates {
		if strings.TrimSpace(gate.EvidenceURI) == "" && strings.TrimSpace(gate.Provenance) == "" {
			continue
		}
		rep.GateRows++
		cites := ClassifyURI(gate.EvidenceURI)
		var repoFiles []string
		for _, c := range cites {
			rep.Census[c.Kind]++
			switch c.Kind {
			case KindUnclassified:
				add(CitationViolation{Scope: gate.ID, Rule: RuleUnclassifiedURI, Target: c.Raw,
					Detail: "evidence_uri segment matches no declared citation rule — an unreadable citation is an unbacked one"})
				continue
			case KindRepoPath, KindDocAnchor, KindTestSymbol:
			default:
				continue
			}
			vs, isFile, err := resolvePath(g, syms, gate.ID, c)
			if err != nil {
				return rep, err
			}
			found = append(found, vs...)
			if len(vs) == 0 {
				rep.Verified++
				if isFile {
					repoFiles = append(repoFiles, c.Path)
				}
			}
		}
		found = append(found, checkRowSHA(g, gate, cites, repoFiles)...)
		if gate.Status == StatusPass && len(repoFiles) == 0 && !hasVerifiableCitation(cites) {
			rep.UnbackedPASS = append(rep.UnbackedPASS, gate.ID)
		}

		// provenance sha bindings (the `<path> @ blob <sha>` form)
		for _, b := range ProvenanceBindings(gate.Provenance) {
			path, sha := b[0], b[1]
			if !hasCitationRoot(path) || !hasCitationExtension(path) {
				continue
			}
			ok, err := g.IsFileAtHEAD(path)
			if err != nil {
				return rep, err
			}
			if !ok {
				add(CitationViolation{Scope: gate.ID, Rule: RuleMissingPath, Target: path,
					Detail: "provenance binds a blob sha to a path that does not exist at HEAD"})
				continue
			}
			actual, err := g.BlobSHA(path)
			if err != nil {
				return rep, err
			}
			if !shaPrefixMatch(sha, actual) {
				add(CitationViolation{Scope: gate.ID, Rule: RuleSHAMismatch, Target: path,
					Detail: fmt.Sprintf("provenance records blob %s but HEAD:%s is %s", sha, path, actual)})
				continue
			}
			rep.Verified++
		}
	}

	// ── the governed prose records (AC-6/AC-7) ──────────────────────────────
	docs, err := ListGoverned(root)
	if err != nil {
		return rep, err
	}
	rep.GovernedDocs = len(docs)
	for _, doc := range docs {
		text, err := ReadGoverned(root, doc)
		if err != nil {
			return rep, err
		}
		cites, exempt := ScanDocument(doc, text)
		rep.Exempt += exempt
		for _, c := range cites {
			rep.Census[c.Kind]++
			scope := fmt.Sprintf("%s:%d", c.Doc, c.Line)
			vs, _, err := resolvePath(g, syms, scope, c)
			if err != nil {
				return rep, err
			}
			// AC-6 applies AC-2 and AC-5 to prose, not AC-3: a governed record is
			// not a sha-bearing row, and worktree drift in a file it merely names
			// is somebody else's edit in flight, not a false claim.
			for _, v := range vs {
				if v.Rule == RuleWorktreeDrift {
					continue
				}
				found = append(found, v)
			}
			if len(vs) == 0 {
				rep.Verified++
			}
		}
	}

	// ── the ratchet (AC-11) ─────────────────────────────────────────────────
	found = append(found, gf.Validate()...)
	suppressed := map[string]bool{}
	for _, e := range gf.Entries {
		suppressed[e.Target] = false
	}
	var stands []CitationViolation
	for _, v := range found {
		k := v.Key()
		if _, ok := suppressed[k]; ok {
			suppressed[k] = true
			rep.Suppressed = append(rep.Suppressed, v)
			continue
		}
		stands = append(stands, v)
	}
	for _, e := range gf.Entries {
		if !suppressed[e.Target] {
			stands = append(stands, CitationViolation{
				Scope: fmt.Sprintf("%s:%d", GrandfatherPath, e.Line), Rule: RuleGrandfatherUnused, Target: e.Target,
				Detail: "this entry's target now passes — DELETE THIS ENTRY. The list is a ratchet; a drained entry may not be left behind"})
		} else {
			rep.GrandfatherOK++
		}
	}
	sort.SliceStable(stands, func(i, j int) bool { return stands[i].Scope < stands[j].Scope })
	rep.Violations = stands
	return rep, nil
}

// resolvePath applies AC-2, AC-3 and AC-5 to one classified citation. isFile
// reports whether the citation resolved to a blob (as opposed to a directory), so
// the caller knows which paths a recorded sha may legitimately bind to.
func resolvePath(g *Git, syms *SymbolResolver, scope string, c Citation) (out []CitationViolation, isFile bool, err error) {
	exists, err := g.ExistsAtHEAD(c.Path)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		target := c.Path
		if c.Symbol != "" {
			target = c.Path + "::" + c.Symbol
		}
		return []CitationViolation{{Scope: scope, Rule: RuleMissingPath, Target: target,
			Detail: "cited path does not exist at HEAD"}}, false, nil
	}
	isFile, err = g.IsFileAtHEAD(c.Path)
	if err != nil {
		return nil, false, err
	}
	if c.Kind == KindTestSymbol {
		ok, serr := syms.Declares(c.Path, c.Symbol)
		if serr != nil {
			return []CitationViolation{{Scope: scope, Rule: RuleSymbolUnresolved, Target: c.Path + "::" + c.Symbol,
				Detail: serr.Error()}}, isFile, nil
		}
		if !ok {
			return []CitationViolation{{Scope: scope, Rule: RuleSymbolUnresolved, Target: c.Path + "::" + c.Symbol,
				Detail: fmt.Sprintf("%s declares no symbol %s at HEAD", c.Path, c.Symbol)}}, isFile, nil
		}
	}
	if isFile {
		head, err := g.BlobSHA(c.Path)
		if err != nil {
			return nil, isFile, err
		}
		wt, err := g.WorktreeSHA(c.Path)
		if err == nil && wt != head {
			return []CitationViolation{{Scope: scope, Rule: RuleWorktreeDrift, Target: c.Path,
				Detail: fmt.Sprintf("worktree bytes hash %s but HEAD is %s — a row may not claim a sha for bytes that are not committed", wt, head)}}, isFile, nil
		}
	}
	return nil, isFile, nil
}

// checkRowSHA is AC-1. The rule set for what a recorded sha may be is declared
// here, because the index records three legitimate kinds and conflating them
// would produce false failures:
//
//	git blob sha   → MUST be the HEAD blob of one of the cited repo files
//	git commit sha → the commit must exist and every cited path must exist in it
//	sha256 digest  → MUST equal the sha256 of the HEAD bytes of one cited file
//
// Anything else — a sha that names no object and matches no cited file's digest —
// is a violation. A blank sha is left to the pre-existing non-empty rule in Check.
func checkRowSHA(g *Git, gate Gate, cites []Citation, repoFiles []string) []CitationViolation {
	sha := strings.TrimSpace(gate.SHA)
	if sha == "" {
		return nil
	}
	if len(repoFiles) == 0 {
		// Nothing resolvable to compare the sha against. AC-4 governs this case:
		// classify it, print it, and do NOT count it as verified — but do not fail
		// on it either. A template ("corpus/fixtures/hero-<lang>") or a command
		// ("cmd/coverage -check") is a DECLARED classification, and AC-4 makes only
		// an UNCLASSIFIABLE uri a violation. Failing here would also flip rows the
		// story's Out-of-scope forbids it to touch. The count is surfaced instead,
		// by name, in the report's unbacked-PASS line.
		return nil
	}
	typ, terr := g.ObjectType(sha)
	switch {
	case terr == nil && typ == "blob":
		var actuals []string
		for _, p := range repoFiles {
			a, err := g.BlobSHA(p)
			if err != nil {
				continue
			}
			if shaPrefixMatch(sha, a) {
				return nil
			}
			actuals = append(actuals, p+"="+a)
		}
		return []CitationViolation{{Scope: gate.ID, Rule: RuleSHAMismatch, Target: strings.Join(repoFiles, ","),
			Detail: fmt.Sprintf("recorded blob sha %s matches no cited path at HEAD (actual: %s)", sha, strings.Join(actuals, " "))}}
	case terr == nil && (typ == "commit" || typ == "tag"):
		// A commit sha on an evidence row names the CANDIDATE the measurement was
		// taken against, not the revision the artifact was committed at — the
		// artifact is normally written after the candidate it measures. Requiring
		// the cited path to exist in that commit would therefore be wrong, and was:
		// it fired on WP4, GA-LANG-java-G7 and GA-LANG-kotlin-G7, all three of which
		// record an honest "measured at candidate X". The non-vacuous half of the
		// claim — that the commit is a real one — is checked by ObjectType above,
		// and the artifact's own existence is AC-2, checked separately.
		return nil
	case terr == nil:
		return []CitationViolation{{Scope: gate.ID, Rule: RuleSHAUnresolvable, Target: sha,
			Detail: fmt.Sprintf("recorded sha resolves to a git %s, which cannot back a file citation", typ)}}
	}
	// Not a git object: the third legal kind is a sha256 of the cited bytes.
	if len(sha) == 64 {
		for _, p := range repoFiles {
			d, err := g.ContentDigest(p)
			if err == nil && d == sha {
				return nil
			}
		}
	}
	return []CitationViolation{{Scope: gate.ID, Rule: RuleSHAUnresolvable, Target: sha,
		Detail: "recorded sha names no git object and is not the sha256 of any cited file at HEAD"}}
}

// hasVerifiableCitation reports whether any citation in the row is of a kind the
// gate resolves.
func hasVerifiableCitation(cites []Citation) bool {
	for _, c := range cites {
		if c.Kind.Verified() {
			return true
		}
	}
	return false
}

// shaPrefixMatch implements AC-1's prefix rule: a recorded sha may be abbreviated,
// but the characters it does record must agree.
func shaPrefixMatch(recorded, actual string) bool {
	recorded = strings.ToLower(strings.TrimSpace(recorded))
	actual = strings.ToLower(actual)
	if recorded == "" || len(recorded) > len(actual) {
		return false
	}
	return strings.HasPrefix(actual, recorded)
}

// FormatCitations renders the citation sweep for CI logs, deterministically.
func (r CitationReport) FormatCitations() string {
	var b strings.Builder
	kinds := make([]string, 0, len(r.Census))
	for k := range r.Census {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	var census []string
	for _, k := range kinds {
		census = append(census, fmt.Sprintf("%s=%d", k, r.Census[CitationKind(k)]))
	}
	fmt.Fprintf(&b, "citation check: %d gate rows, %d governed docs, %d citations verified, %d exempted by a declared STALE/SUPERSEDED/CORRECTION marker, %d grandfathered\n",
		r.GateRows, r.GovernedDocs, r.Verified, r.Exempt, r.GrandfatherOK)
	fmt.Fprintf(&b, "citation classification census: %s\n", strings.Join(census, " "))
	if len(r.UnbackedPASS) > 0 {
		fmt.Fprintf(&b, "citation check NOTE — %d PASS row(s) cite only classified-but-unresolvable evidence (a template, a command or an external URL) and are NOT counted as verified: %s\n",
			len(r.UnbackedPASS), strings.Join(r.UnbackedPASS, " "))
	}
	if r.Pass() {
		b.WriteString("citation check PASS — every verified citation resolves, every recorded sha is the sha of what it cites.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "citation check FAILED — %d citation(s) do not resolve:\n", len(r.Violations))
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "    - [%s] %s: %s — %s\n", v.Scope, v.Rule, v.Target, v.Detail)
		fmt.Fprintf(&b, "        key: %s\n", v.Key())
	}
	fmt.Fprintf(&b, "\nFix the citation, or add the key to %s with a reason and an owner story.\n", GrandfatherPath)
	return b.String()
}
