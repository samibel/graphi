package conformance_test

// SW-206 AC-5 — the cross-family claim guard that makes ONE falsifiable prose
// defect unrepeatable across BOTH artefact kinds.
//
// THE DEFECT IT PINS. Twenty sites (nineteen `note:` strings in
// docs/rc/parity-classes-*.yaml and one `description:` in
// engine/conformance/ccppparity_test.go) described a change-class witness as
// asserting a resolver counter increment — "…and INCREMENTS the counter
// (Stats.Skipped += 1)", "…and Stats.Ambiguous += 1". No witness in this
// package reads any counter. `graphView` (changeclass_test.go:111-115) holds
// exactly nodes/edges/byQN/byID and every helper on it is a node/edge
// predicate, so the claim was not merely unmet by those particular witnesses —
// it was unreachable through the API a witness is handed.
//
// WHY THE EXISTING GUARDS DID NOT CATCH IT, stated so a reader does not assume
// one was bypassed. The <lang>parity_matrix_test.go drift guards cover
// MISSING / PHANTOM / DEFERRED / KIND / OWNER / VERDICT / AXIS / VOCABULARY —
// STRUCTURAL fields only. `grep -n note engine/conformance/bashparity_matrix_test.go`
// is empty: no drift guard reads `note:` at all. And SW-194's own COUNT-check
// pattern (`namedskip|histogram|unresolved|SkipCount`) cannot match the token
// `Stats.Skipped` the false sentences actually used, and was run only over the
// Go twins, never over the YAMLs where sixteen of the twenty sites lived.
//
// WHAT THIS GUARD IS NOT. It is not semantic verification of prose in general;
// that is not mechanizable and SW-205 rejected it deliberately (SW-205 "Out of
// scope": "it does not and cannot check that the cited artifact supports the
// claim"). This guard claims one narrow thing only: "the witness reads a
// counter" is a token search over a function body, so a sentence attributing a
// counter read to a witness can be checked against that body. Nothing wider.
//
// ENUMERATION IS A GLOB, NOT A LIST. Both halves enumerate with
// filepath.Glob — `*parity_test.go` for the twins, `../../docs/rc/parity-classes-*.yaml`
// for the published artefacts. A hardcoded file list is precisely the hole this
// defect lived in: SW-194's check named eight twins and no YAML, so a
// language family added later, or an artefact kind never listed, went
// unchecked. Same argument the SW-189 direction guard's header makes
// (parity_matrix_directions_test.go:18-25).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	parityTwinGlob      = "*parity_test.go"
	parityClassYAMLGlob = "../../docs/rc/parity-classes-*.yaml"
)

// counterClaimTokens is CLASS A: tokens whose presence in a witness-attributed
// sentence IS a counter-increment claim, with no further context needed. The
// first four are the tokens this defect actually used; `SkipCount` is carried
// from SW-194's pattern; the "drop-and-count half" / "drops + counts" spellings
// are the same claim in words rather than in an identifier, and were the two
// forms the `Stats.` token search did not reach.
var counterClaimTokens = []string{
	"stats.skipped",
	"stats.ambiguous",
	"increments the counter",
	"+= 1",
	"skipcount",
	"drop-and-count half",
	"drop+count",
	"drops + counts",
	"drop + count",
}

// counterContextTokens is CLASS B: tokens carried from SW-194's COUNT-check
// pattern (`namedskip|histogram|unresolved|SkipCount`). Applied bare to prose
// they false-positive on true sentences — "the resolver drops an unresolved
// target" is a statement about the RESOLVER and is true — so they count as a
// claim only when the same sentence ALSO carries a counting word
// (counterVerbTokens). SW-194's pattern was written to search witness function
// BODIES, where a bare token match is sound; this guard reads prose, where it
// is not, and the difference is stated rather than papered over.
var counterContextTokens = []string{
	"namedskip",
	"named-skip",
	"histogram",
	"unresolved",
}

var counterVerbTokens = []string{
	"counter",
	"counts",
	"count half",
	"increment",
}

// witnessAttributionTokens mark a sentence as attributing an assertion to the
// WITNESS rather than describing the resolver's contract. Only such sentences
// are checked: "the resolver drops and counts what it cannot resolve" is a true
// statement about the resolver and must stay sayable.
var witnessAttributionTokens = []string{
	"witness assert",
	"the witness",
	"witness proves",
	"witness checks",
	"witness reads",
}

// witnessCounterReadTokens is the token set searched in the witness FUNCTION
// BODY — SW-194's pattern applied where a bare token match is sound, widened
// with the identifiers a real counter-reading witness would have to name.
// A row whose witness body matches any of these MAY carry a counter claim in
// its prose; a row whose body matches none MAY NOT.
var witnessCounterReadTokens = []string{
	"Stats",
	"Skipped",
	"Ambiguous",
	"SkipCount",
	"namedskip",
	"histogram",
	"trust_file_evidence",
	"lastFileLinkStats",
}

// sentenceSplit breaks prose into sentences. Deliberately crude: a period or
// an em-dash-bounded clause is enough granularity to keep a witness attribution
// and a counter token in the same unit when they belong to the same claim, and
// to separate them when they do not.
var sentenceSplit = regexp.MustCompile(`(?:\. |\.$|; )`)

// parityRowClaim is one (id, prose) pair drawn from either artefact kind.
type parityRowClaim struct {
	file  string
	line  int
	id    string
	prose string
	kind  string // "description" or "note"
}

// parityTwinRow is one harness row read out of a *parity_test.go twin.
type parityTwinRow struct {
	file        string
	descLine    int
	id          string
	description string
	witnessBody string
}

// TestParityClaims_NoCounterAssertionWithoutACounterReadingWitness is the AC-5
// guard. For every change-class row declared in any *parity_test.go twin and
// for every row in any docs/rc/parity-classes-*.yaml, it fails when the prose
// attributes a counter increment to the row's WITNESS while that witness's
// function body reads no counter.
//
// Demonstrated RED at 3fe97f0 (the twenty sites, each named with file, line and
// row id) and green on the fixed tree — SW-206 AC-6.
func TestParityClaims_NoCounterAssertionWithoutACounterReadingWitness(t *testing.T) {
	twins := loadParityTwinRows(t)
	if len(twins) == 0 {
		t.Fatalf("glob %q matched no harness rows; the cross-family enumeration is broken", parityTwinGlob)
	}

	byID := map[string]parityTwinRow{}
	for _, r := range twins {
		if prev, dup := byID[r.id]; dup {
			t.Fatalf("duplicate harness row id %q in %s and %s", r.id, prev.file, r.file)
		}
		byID[r.id] = r
	}

	claims := make([]parityRowClaim, 0, len(twins)*2)
	for _, r := range twins {
		claims = append(claims, parityRowClaim{file: r.file, line: r.descLine, id: r.id, prose: r.description, kind: "description"})
	}
	claims = append(claims, loadParityClassYAMLClaims(t, byID)...)

	sort.Slice(claims, func(a, b int) bool {
		if claims[a].file != claims[b].file {
			return claims[a].file < claims[b].file
		}
		return claims[a].line < claims[b].line
	})

	for _, c := range claims {
		hit, sentence := counterClaimIn(c.prose)
		if hit == "" {
			continue
		}
		row := byID[c.id]
		if witnessReadsACounter(row.witnessBody) {
			continue
		}
		t.Errorf("COUNTER-CLAIM: %s:%d row %q %s: attributes a counter increment to the witness (token %q in %q), but the witness at %s reads no counter — graphView exposes only nodes/edges/byQN/byID. Either correct the prose to what the witness asserts, or make the witness read the counter.",
			filepath.ToSlash(c.file), c.line, c.id, c.kind, hit, strings.TrimSpace(sentence), filepath.ToSlash(row.file))
	}
}

// counterClaimIn returns the offending token and the sentence carrying it, or
// "" when the prose makes no witness-attributed counter claim.
func counterClaimIn(prose string) (string, string) {
	for _, s := range sentenceSplit.Split(prose, -1) {
		low := strings.ToLower(s)
		if !containsAny(low, witnessAttributionTokens) {
			continue
		}
		for _, tok := range counterClaimTokens {
			if strings.Contains(low, tok) {
				return tok, s
			}
		}
		if containsAny(low, counterVerbTokens) {
			for _, tok := range counterContextTokens {
				if strings.Contains(low, tok) {
					return tok, s
				}
			}
		}
	}
	return "", ""
}

func containsAny(s string, toks []string) bool {
	for _, t := range toks {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// witnessReadsACounter reports whether a witness function body names any
// counter surface. Empty bodies (a row with no witness) read nothing.
func witnessReadsACounter(body string) bool {
	for _, t := range witnessCounterReadTokens {
		if strings.Contains(body, t) {
			return true
		}
	}
	return false
}

// loadParityTwinRows parses every *parity_test.go twin found by glob and
// returns each composite-literal row that carries an `id:` together with its
// `description:` and the source text of its `witness:` function body.
func loadParityTwinRows(t *testing.T) []parityTwinRow {
	t.Helper()
	matches, err := filepath.Glob(parityTwinGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", parityTwinGlob, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %q matched 0 twins", parityTwinGlob)
	}
	sort.Strings(matches)

	var out []parityTwinRow
	for _, path := range matches {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			row := parityTwinRow{file: path}
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "id":
					if s, ok := staticString(kv.Value); ok {
						row.id = s
					}
				case "description":
					if s, ok := staticString(kv.Value); ok {
						row.description = s
						row.descLine = fset.Position(kv.Pos()).Line
					}
				case "witness":
					if fl, ok := kv.Value.(*ast.FuncLit); ok && fl.Body != nil {
						lo := fset.Position(fl.Body.Pos()).Offset
						hi := fset.Position(fl.Body.End()).Offset
						if lo >= 0 && hi <= len(src) && lo < hi {
							row.witnessBody = string(src[lo:hi])
						}
					}
				}
			}
			if row.id != "" && row.description != "" {
				out = append(out, row)
			}
			return true
		})
	}
	return out
}

// loadParityClassYAMLClaims reads every docs/rc/parity-classes-*.yaml found by
// glob and returns one claim per row `note:`, carrying the note's own line
// number. An id with no harness row is its own defect and fails here rather
// than being skipped — a skipped id is how a false sentence hides.
func loadParityClassYAMLClaims(t *testing.T, byID map[string]parityTwinRow) []parityRowClaim {
	t.Helper()
	matches, err := filepath.Glob(filepath.FromSlash(parityClassYAMLGlob))
	if err != nil {
		t.Fatalf("glob %q: %v", parityClassYAMLGlob, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %q matched 0 published parity-class artefacts", parityClassYAMLGlob)
	}
	sort.Strings(matches)

	var out []parityRowClaim
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		rows, err := parityClassRowNodes(&doc)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(rows) == 0 {
			t.Fatalf("%s declared no parity_classes rows", path)
		}
		for _, r := range rows {
			id, _, _ := mappingEntry(r, "id")
			note, noteLine, ok := mappingEntry(r, "note")
			if id == "" {
				t.Errorf("COUNTER-CLAIM: %s:%d row has no id: — the guard pairs prose to a witness by id and cannot check an unnamed row", filepath.ToSlash(path), r.Line)
				continue
			}
			if _, found := byID[id]; !found {
				t.Errorf("COUNTER-CLAIM: %s:%d row %q has no harness row in any %s — an unmatched id is its own defect, not a row to skip", filepath.ToSlash(path), r.Line, id, parityTwinGlob)
				continue
			}
			if !ok || note == "" {
				continue
			}
			out = append(out, parityRowClaim{file: path, line: noteLine, id: id, prose: note, kind: "note"})
		}
	}
	return out
}

// parityClassRowNodes returns the sequence entries under the document's
// top-level `parity_classes:` key.
func parityClassRowNodes(doc *yaml.Node) ([]*yaml.Node, error) {
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("empty YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("top level is not a mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "parity_classes" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("parity_classes is not a sequence")
		}
		return seq.Content, nil
	}
	return nil, fmt.Errorf("no parity_classes key")
}

// mappingEntry returns a mapping key's scalar value and the line the VALUE is
// written on.
func mappingEntry(m *yaml.Node, key string) (string, int, bool) {
	if m == nil || m.Kind != yaml.MappingNode {
		return "", 0, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1].Value, m.Content[i+1].Line, true
		}
	}
	return "", 0, false
}

// staticString evaluates a Go string literal, including a `"a" + "b"`
// concatenation, and reports whether the expression was fully static.
func staticString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, okl := staticString(v.X)
		r, okr := staticString(v.Y)
		if !okl || !okr {
			return "", false
		}
		return l + r, true
	}
	return "", false
}
