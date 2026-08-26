package parity

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// ---------------------------------------------------------------------------
// W5.n hermetic tests.
//
// They pin the DRIVER'S SCORING, which is the SW-175 FINDING B-2 discipline:
// the counters that decide a row must not be the parser's own. Nothing here
// runs the built binary — that is the dispatch harness's job and it needs pins
// this repository does not have. What these tests assert is everything the
// harness decides BEFORE and AFTER the binary runs: which classes exist, which
// repositories are admissible, what each planner writes, and how a verdict is
// reached from bytes.
// ---------------------------------------------------------------------------

// TestParseDet_BindsToDeclaredMatrix is the MISSING/PHANTOM pair, in both
// directions, for all six languages. A class declared in SW-199's YAML with no
// planner here, or a planner here for a class the YAML does not declare, fails
// the build — which is the property that stops the real-repo table and the
// hermetic table drifting apart.
func TestParseDet_BindsToDeclaredMatrix(t *testing.T) {
	for _, name := range ParseDetLanguages() {
		name := name
		t.Run(name, func(t *testing.T) {
			lang, ok := ParseDetLanguageFor(name)
			if !ok {
				t.Fatalf("ParseDetLanguages() lists %q but ParseDetLanguageFor has no binding", name)
			}
			rows, err := LoadClasses(filepath.Join("..", "..", ParseDetClassesPath(name)))
			if err != nil {
				t.Fatalf("LoadClasses(%s): %v", ParseDetClassesPath(name), err)
			}
			byID := lang.SpecByID()
			declared := map[string]bool{}
			for _, row := range rows {
				if row.Kind != kindChangeClass || row.HarnessRow == harnessDeferred {
					continue
				}
				declared[row.ID] = true
				if _, ok := byID[row.ID]; !ok {
					t.Errorf("MISSING: %s declares change class %q with harness_row %q, and the %s table has no planner",
						ParseDetClassesPath(name), row.ID, row.HarnessRow, name)
				}
			}
			for _, s := range lang.Specs {
				if !declared[s.ID] {
					t.Errorf("PHANTOM: the %s table binds class %q, which %s does not declare as a required change class",
						name, s.ID, ParseDetClassesPath(name))
				}
				if s.Plan == nil {
					t.Errorf("class %q has no planner", s.ID)
				}
			}
			if got, want := len(lang.Specs), len(declared); got != want {
				t.Errorf("%s binds %d classes, %s declares %d", name, got, ParseDetClassesPath(name), want)
			}
		})
	}
}

// TestParseDet_IDPrefixesAreLanguageScoped pins the frozen-wire-identifier rule
// the six YAMLs state: ids are prefixed `<lang>_` so they stay globally unique
// across every family's twin. Without it, two languages could bind the same id
// and -verdict-diff would compare rows from different languages as one.
func TestParseDet_IDPrefixesAreLanguageScoped(t *testing.T) {
	seen := map[string]string{}
	for _, name := range ParseDetLanguages() {
		lang, _ := ParseDetLanguageFor(name)
		for _, s := range lang.Specs {
			if !strings.HasPrefix(s.ID, name+"_") {
				t.Errorf("%s binds class %q, which is not prefixed %q", name, s.ID, name+"_")
			}
			if prev, dup := seen[s.ID]; dup {
				t.Errorf("class id %q is bound by both %s and %s", s.ID, prev, name)
			}
			seen[s.ID] = name
		}
	}
}

// TestParseDet_AxisIsTwoCellsAndSaysWhichStore pins refusal (2) of parsedet.go's
// header as a TEST rather than as a comment: the dispatch axis is the one
// reachable store crossed with the two behaviourally distinct profiles, and
// each row says which store it measured.
//
// It exists so that a future edit adding a second "store" value has to justify
// itself here, in a test, rather than silently making the report claim a
// backend the built binary cannot emit an envelope from.
func TestParseDet_AxisIsTwoCellsAndSaysWhichStore(t *testing.T) {
	axes := parseDetAxes()
	if len(axes) != 2 {
		t.Fatalf("parseDetAxes() = %d cells, want 2", len(axes))
	}
	profiles := map[string]bool{}
	for _, a := range axes {
		if a.Store != ParseDetStoreSQLite {
			t.Errorf("axis %v names store %q; no dispatch-driven full index can emit an envelope from any other backend",
				a, a.Store)
		}
		profiles[a.Profile] = true
		if !strings.Contains(a.Describe(), a.Store) {
			t.Errorf("axis %v does not name its store in Describe(): %q", a, a.Describe())
		}
		if !strings.Contains(a.Suffix(), "store="+a.Store) {
			t.Errorf("axis %v does not carry its store in the row id suffix: %q", a, a.Suffix())
		}
	}
	if !profiles[""] || !profiles["fast"] {
		t.Errorf("the profile axis is %v, want {resolved default, fast}", profiles)
	}
}

// TestParseDet_CrossedCountIsBelowTheFR7Floor is PARITY-011, pinned.
//
// It is deliberately an ASSERTION OF THE DEFECT, not of a fix: the six declared
// classes crossed over the two reachable axes are twelve, FR7ChangeClasses is
// fifteen, and Report.Finalize therefore refuses completeness for this family
// whatever the rows say. Writing it down as a test means the day someone lifts
// the floor or adds a class, this test fails and the person lifting it has to
// decide consciously whether the family became publishable.
func TestParseDet_CrossedCountIsBelowTheFR7Floor(t *testing.T) {
	for _, name := range ParseDetLanguages() {
		rows, err := LoadClasses(filepath.Join("..", "..", ParseDetClassesPath(name)))
		if err != nil {
			t.Fatalf("LoadClasses(%s): %v", ParseDetClassesPath(name), err)
		}
		crossed := CountChangeClasses(rows) * len(parseDetAxes())
		if crossed >= parityreport.FR7ChangeClasses {
			t.Fatalf("%s now crosses to %d rows, which is at or above FR7ChangeClasses=%d: "+
				"PARITY-011's premise no longer holds and the family's publishability must be re-decided "+
				"deliberately, not inherited from this test passing", name, crossed, parityreport.FR7ChangeClasses)
		}
		if crossed != 12 {
			t.Errorf("%s crosses to %d rows, want 12 (6 declared classes x 2 axes)", name, crossed)
		}
	}
}

// TestParseDet_NoPinEntryIsNotACandidate pins the honest-abstention rule: a
// manifest row whose whole purpose is to declare that no pin exists must never
// be selected as one.
func TestParseDet_NoPinEntryIsNotACandidate(t *testing.T) {
	m := corpus.Manifest{Entries: []corpus.Entry{
		{Name: "css-abstention", Language: "css", Tier: 3, NoPin: true, NoPinReason: "declared gap"},
		{Name: "real-css", Language: "css", Tier: 3, URL: "https://example.invalid/x", Ref: "v1"},
		{Name: "fixture-css", Language: "css", Tier: 1, Path: "corpus/fixtures/hero-css"},
	}}
	got := parseDetRepos(m, "css", 3, false)
	if len(got) != 1 || got[0].Name != "real-css" {
		t.Fatalf("parseDetRepos(allowLocal=false) = %v, want only real-css", names(got))
	}
	got = parseDetRepos(m, "css", 3, true)
	if len(got) != 2 {
		t.Fatalf("parseDetRepos(allowLocal=true) = %v, want the fixture and the clone, never the abstention", names(got))
	}
	for _, e := range got {
		if e.NoPin {
			t.Fatalf("parseDetRepos admitted the no_pin abstention entry %q", e.Name)
		}
	}
}

func names(es []corpus.Entry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

// TestParseDet_TheRealManifestStillHasNoPinForAnyOfTheSix is the guard that
// actually goes red when a pin lands. It reads the REAL corpus/manifest.json —
// not a literal, not a fixture — and asserts that selection still finds no
// real-repository candidate for any of the six intra/parse residual languages.
//
// WHY IT IS A SEPARATE TEST. SW-200's whole record is an abstention whose first
// named blocker is "no pin exists". Every other test in this file passes an
// in-test manifest, so none of them can observe the manifest changing: they
// would stay green forever after SW-201's v3 pins land, and the abstention
// could persist silently in six evidence-index rows while the fact underneath
// it had gone stale. This one cannot. The day a css/hcl/json/markdown/toml/yaml
// entry above tier 1 appears in corpus/manifest.json, this test fails and the
// six GA-LANG-<lang>-G4 abstentions have to be re-decided by a person rather
// than inherited from a green suite.
//
// It reads selection through parseDetRepos with allowLocal=false — the
// PRODUCTION posture — so a tier-1 fixture arriving does not trip it; only a
// real pin does, which is exactly the event that changes the story's answer.
func TestParseDet_TheRealManifestStillHasNoPinForAnyOfTheSix(t *testing.T) {
	path := filepath.Join("..", "..", "corpus", "manifest.json")
	m, err := corpus.LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest(%s): %v", path, err)
	}
	for _, lang := range ParseDetLanguages() {
		got := parseDetRepos(m, lang, 3, false)
		if len(got) != 0 {
			t.Errorf("%s: %s now offers %v as a real-repository candidate.\n"+
				"THIS TEST IS DOING ITS JOB: SW-200 recorded GA-LANG-%s-G4 as UNKNOWN because no such pin "+
				"existed, and docs/rc/intra-parse-residual-parity-abstention.md §2.1 names that as the first "+
				"of three blockers. A pin has landed, so re-run the dispatch for this language and re-decide "+
				"the row deliberately — do not silence this test.",
				lang, path, names(got), lang)
		}
	}
}

// TestParseDet_NoPinReasonNamesTheMechanism pins AC-3's requirement that the
// abstention names WHICH check refuses and WHAT would lift it — not "blocked".
func TestParseDet_NoPinReasonNamesTheMechanism(t *testing.T) {
	got := parseDetNoPinReason("css", 3, false)
	for _, want := range []string{
		"corpus/manifest.json",
		"stays UNKNOWN",
		"SW-201",
		"sw-201-w5o-corpus-pins-v3-intra-parse",
		"no fixture is substituted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the abstention reason does not mention %q:\n%s", want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The planners
// ---------------------------------------------------------------------------

func modelOf(t *testing.T, lang string, files map[string]string) *ParseDetModel {
	t.Helper()
	l, ok := ParseDetLanguageFor(lang)
	if !ok {
		t.Fatalf("no binding for %q", lang)
	}
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := discoverParseDet(root, lang, l.Exts)
	if err != nil {
		t.Fatalf("discoverParseDet: %v", err)
	}
	return m
}

// parseDetFixtures are the per-language trees the planner tests run over. They
// are the SHAPE of each language's checked-in hero fixture, kept in the test so
// a planner assertion cannot silently start depending on a fixture another
// story owns.
func parseDetFixtures() map[string]map[string]string {
	return map[string]map[string]string{
		"css": {
			"base/tokens.css": ":root {\n  --a: 1;\n}\n",
			"site/theme.css":  ".cart {\n  color: red;\n}\n\n.grid {\n  display: grid;\n}\n",
		},
		"hcl": {
			"infra/main.tf":  "variable \"region\" {\n  type = string\n}\n\nresource \"aws_s3_bucket\" \"logs\" {\n  bucket = \"x\"\n}\n",
			"modules/net.tf": "output \"id\" {\n  value = 1\n}\n",
		},
		"json": {
			"api/schema.json": "{\n  \"alpha\": 1,\n  \"beta\": {\"nested\": true}\n}\n",
			"api/small.json":  "{\n  \"only\": 1\n}\n",
		},
		"markdown": {
			"guide/intro.md": "Lede paragraph.\n\n# Setup\n\nOne.\n\n## Details\n\nTwo.\n",
			"notes/x.md":     "# Notes\n\nShort.\n",
		},
		"toml": {
			"config/app.toml": "name = \"app\"\n\n[server]\nport = 1\n\n[client]\nretries = 2\n",
			"config/e.toml":   "[only]\nk = 1\n",
		},
		"yaml": {
			"pipeline/build.yaml": "name: build\n\nsteps:\n  - run: make\n\nenv:\n  A: 1\n",
			"shared/common.yaml":  "shared: 1\n",
		},
	}
}

// TestParseDet_PlannersProduceRealEdits runs every planner of every language
// over that language's fixture shape and asserts the class's DEFINING property
// of the produced mutation — not merely that a mutation came back.
func TestParseDet_PlannersProduceRealEdits(t *testing.T) {
	fixtures := parseDetFixtures()
	for _, name := range ParseDetLanguages() {
		name := name
		t.Run(name, func(t *testing.T) {
			lang, _ := ParseDetLanguageFor(name)
			m := modelOf(t, name, fixtures[name])
			if len(m.Files) < 2 {
				t.Fatalf("fixture for %s discovered %d files, want >= 2", name, len(m.Files))
			}
			byRel := map[string][]byte{}
			for _, f := range m.Files {
				byRel[f.Rel] = f.Data
			}
			for _, s := range lang.Specs {
				s := s
				t.Run(s.ID, func(t *testing.T) {
					mut, err := s.Plan(m)
					if err != nil {
						t.Fatalf("planner returned %v; every class must be exhibited by this fixture shape", err)
					}
					if mut == nil || len(mut.Ops) == 0 {
						t.Fatal("planner returned no file operation")
					}
					if strings.TrimSpace(mut.Desc) == "" {
						t.Error("mutation carries no description; the report must be re-appliable by hand from it")
					}
					op := mut.Ops[0]
					switch {
					case strings.HasSuffix(s.ID, "_add_file"):
						if op.Kind != opWrite || !strings.HasPrefix(op.Path, parseDetAddedDir+"/") {
							t.Errorf("add_file wrote %q (%s), want a file under %s/", op.Path, op.Kind, parseDetAddedDir)
						}
						if _, existed := byRel[op.Path]; existed {
							t.Error("add_file targeted a file that already exists; that is modify, not add")
						}
					case strings.HasSuffix(s.ID, "_delete_file"):
						if op.Kind != opDelete {
							t.Errorf("delete_file produced op %q, want %q", op.Kind, opDelete)
						}
						if _, ok := byRel[op.Path]; !ok {
							t.Errorf("delete_file targeted %q, which is not in the tree", op.Path)
						}
					case strings.HasSuffix(s.ID, "_reparse_identical_bytes"):
						if op.Kind != opWrite {
							t.Fatalf("reparse produced op %q, want %q", op.Kind, opWrite)
						}
						if string(op.Data) != string(byRel[op.Path]) {
							t.Error("reparse_identical_bytes did NOT write byte-identical content; " +
								"the whole point of the row is that the bytes do not change")
						}
					default:
						if op.Kind != opWrite {
							t.Fatalf("produced op %q, want %q", op.Kind, opWrite)
						}
						if string(op.Data) == string(byRel[op.Path]) {
							t.Error("the planner wrote byte-identical content for a class whose whole " +
								"purpose is to change the source; a no-op edit would make the row vacuous")
						}
					}
				})
			}
		})
	}
}

// TestParseDet_ReorderPermutesWithoutChangingContent is the reorder class's own
// property, asserted per language: the multiset of non-whitespace bytes is
// unchanged, and the byte sequence is not.
//
// Asserting "the bytes differ" alone would pass a planner that rewrote the
// file; asserting the multiset alone would pass one that did nothing. Both
// halves together are what makes this a permutation.
func TestParseDet_ReorderPermutesWithoutChangingContent(t *testing.T) {
	fixtures := parseDetFixtures()
	for _, name := range ParseDetLanguages() {
		name := name
		t.Run(name, func(t *testing.T) {
			lang, _ := ParseDetLanguageFor(name)
			m := modelOf(t, name, fixtures[name])
			var reorder ParseDetSpec
			for _, s := range lang.Specs {
				if strings.Contains(s.ID, "_reorder_") {
					reorder = s
				}
			}
			if reorder.Plan == nil {
				t.Skipf("%s declares no reorder-shaped class", name)
			}
			mut, err := reorder.Plan(m)
			if err != nil {
				t.Fatalf("reorder planner: %v", err)
			}
			op := mut.Ops[0]
			var before []byte
			for _, f := range m.Files {
				if f.Rel == op.Path {
					before = f.Data
				}
			}
			if before == nil {
				t.Fatalf("reorder targeted %q, which is not in the tree", op.Path)
			}
			if string(before) == string(op.Data) {
				t.Fatal("reorder produced identical bytes: nothing was permuted")
			}
			if a, b := sortedRunes(before), sortedRunes(op.Data); a != b {
				t.Errorf("reorder changed the CONTENT, not just the order\n before multiset: %q\n after  multiset: %q", a, b)
			}
		})
	}
}

// sortedRunes renders the multiset of non-whitespace bytes.
func sortedRunes(b []byte) string {
	var cs []string
	for _, c := range b {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		cs = append(cs, string(rune(c)))
	}
	sort.Strings(cs)
	return strings.Join(cs, "")
}

// TestParseDet_PlannersAreDeterministic pins the property the whole two-dispatch
// discipline rests on: the same tree plans the same edit twice. A planner that
// picked "the first file the walk yielded" would break here, and the harness
// would otherwise report its own instability as a product finding.
func TestParseDet_PlannersAreDeterministic(t *testing.T) {
	fixtures := parseDetFixtures()
	for _, name := range ParseDetLanguages() {
		lang, _ := ParseDetLanguageFor(name)
		a := modelOf(t, name, fixtures[name])
		b := modelOf(t, name, fixtures[name])
		for _, s := range lang.Specs {
			ma, erra := s.Plan(a)
			mb, errb := s.Plan(b)
			if erra != nil || errb != nil {
				t.Fatalf("%s: planner errors %v / %v", s.ID, erra, errb)
			}
			if ma.Desc != mb.Desc || len(ma.Ops) != len(mb.Ops) {
				t.Fatalf("%s: two plans over the same tree differ:\n a: %s\n b: %s", s.ID, ma.Desc, mb.Desc)
			}
			for i := range ma.Ops {
				if ma.Ops[i].Kind != mb.Ops[i].Kind || ma.Ops[i].Path != mb.Ops[i].Path ||
					string(ma.Ops[i].Data) != string(mb.Ops[i].Data) {
					t.Fatalf("%s: op %d differs between two plans over the same tree", s.ID, i)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The scoring
// ---------------------------------------------------------------------------

// TestParseDet_VacuityRefusesAnEmptyGraph is the SW-175 FINDING B-2 assertion:
// two empty graphs are byte-identical, and the driver must FAIL such a row
// rather than score the byte equality as a PASS.
func TestParseDet_VacuityRefusesAnEmptyGraph(t *testing.T) {
	css, _ := ParseDetLanguageFor("css")
	mut := &Mutation{Desc: "x", Ops: []FileOp{{Kind: opWrite, Path: "a/b.css", Data: []byte(".x{}")}}}
	if why := parseDetVacuity(css, graphPayload{}, mut, t.TempDir()); why == "" {
		t.Fatal("an empty graph was scored NON-vacuous; two empty graphs are byte-identical and " +
			"a PASS over them certifies that nothing was indexed")
	}
}

// TestParseDet_VacuityRefusesAFileOnlyGraph pins the sharper half: a graph that
// carries file nodes and NO symbol of the language's declared kinds means the
// extractor produced nothing, even though the graph is non-empty.
func TestParseDet_VacuityRefusesAFileOnlyGraph(t *testing.T) {
	g := graphPayload{}
	g.Nodes = append(g.Nodes, struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		QualifiedName string `json:"qualified_name"`
		SourcePath    string `json:"source_path"`
	}{ID: "n1", Kind: "file", SourcePath: "a/b.css"})
	css, _ := ParseDetLanguageFor("css")
	mut := &Mutation{Desc: "x", Ops: []FileOp{{Kind: opWrite, Path: "a/b.css", Data: []byte(".x{}")}}}
	why := parseDetVacuity(css, g, mut, t.TempDir())
	if why == "" {
		t.Fatal("a file-only graph was scored NON-vacuous for css, whose declared node set is {file, type}")
	}
	if !strings.Contains(why, "type") {
		t.Errorf("the vacuity reason does not name the missing kind: %q", why)
	}
}

// TestParseDet_VacuityRequiresTheEditedPath pins the third half: a graph rich in
// the language's symbols still proves nothing if none of them is anchored in
// the file the class actually edited.
func TestParseDet_VacuityRequiresTheEditedPath(t *testing.T) {
	g := graphPayload{}
	g.Nodes = append(g.Nodes, struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		QualifiedName string `json:"qualified_name"`
		SourcePath    string `json:"source_path"`
	}{ID: "n1", Kind: "type", SourcePath: "elsewhere/other.css"})
	css, _ := ParseDetLanguageFor("css")
	mut := &Mutation{Desc: "x", Ops: []FileOp{{Kind: opWrite, Path: "a/b.css", Data: []byte(".x{}")}}}
	why := parseDetVacuity(css, g, mut, t.TempDir())
	if why == "" || !strings.Contains(why, "a/b.css") {
		t.Fatalf("a graph with no node in the edited file was scored non-vacuous: %q", why)
	}
}

// TestParseDet_ParseOnlyWitnessReadsTheTree pins json's branch: the witness is
// the parse boundary, and it reads the FILESYSTEM. A write that did not land
// and a delete that did not happen both fail it.
func TestParseDet_ParseOnlyWitnessReadsTheTree(t *testing.T) {
	js, _ := ParseDetLanguageFor("json")
	if !js.ParseOnly {
		t.Fatal("json must be marked ParseOnly: docs/rc/parity-classes-json.yaml:63 says a JSON " +
			"document contributes no node to the graph")
	}
	if len(js.MinSymbolKinds) != 0 {
		t.Errorf("json declares MinSymbolKinds %v; a parse-only language mints no symbol and claiming "+
			"one would be an over-claim", js.MinSymbolKinds)
	}
	root := t.TempDir()
	want := []byte("{\"a\":1}\n")
	if err := os.WriteFile(filepath.Join(root, "doc.json"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	landed := &Mutation{Ops: []FileOp{{Kind: opWrite, Path: "doc.json", Data: want}}}
	if why := parseDetVacuity(js, graphPayload{}, landed, root); why != "" {
		t.Errorf("a landed write was scored vacuous: %q", why)
	}
	notLanded := &Mutation{Ops: []FileOp{{Kind: opWrite, Path: "doc.json", Data: []byte("{\"b\":2}\n")}}}
	if why := parseDetVacuity(js, graphPayload{}, notLanded, root); why == "" {
		t.Error("a write that did not land was scored non-vacuous; the parse boundary is the only " +
			"witness a parse-only language has, and it must observe the change")
	}
	notDeleted := &Mutation{Ops: []FileOp{{Kind: opDelete, Path: "doc.json"}}}
	if why := parseDetVacuity(js, graphPayload{}, notDeleted, root); why == "" {
		t.Error("a delete that did not happen was scored non-vacuous")
	}
}

// TestParseDet_RefusesAnUnknownLanguage pins the fail-closed door: a family the
// registry does not know is refused, never defaulted to another language.
func TestParseDet_RefusesAnUnknownLanguage(t *testing.T) {
	r := &Runner{ParseDetLang: "elvish", WorkDir: t.TempDir()}
	_, err := r.RunParseDeterminism(t.Context(), corpus.Manifest{}, nil, parityreport.Provenance{})
	if err == nil {
		t.Fatal("an unknown -family was accepted")
	}
	if !strings.Contains(err.Error(), "elvish") {
		t.Errorf("the refusal does not name the rejected language: %v", err)
	}
}

// TestParseDet_AbstainsWithoutAPinAndIsNotPublishable is the end-to-end shape of
// what SW-200 actually measured on this tree: with no corpus entry for the
// language, every declared row SKIPS with the mechanism named, and the run is
// neither complete nor publishable.
//
// It drives no binary: nothing reaches the dispatch, because selection abstains
// first. That is exactly the state the story records.
//
// WHAT THIS TEST DOES NOT DO, stated because an earlier version of this comment
// claimed it (SW-200 review round 1, major M3): it passes corpus.Manifest{}, an
// EMPTY LITERAL, so it never reads corpus/manifest.json and a landing pin
// cannot turn it red. It asserts the SHAPE of the abstention — every row
// SKIPPED, the mechanism named in the detail, an axis note on every row,
// neither complete nor publishable — which is a property of the runner and is
// meant to hold whatever the manifest says. The manifest-bound half, the one
// that really does go red when a pin lands, is
// TestParseDet_TheRealManifestStillHasNoPinForAnyOfTheSix above.
func TestParseDet_AbstainsWithoutAPinAndIsNotPublishable(t *testing.T) {
	for _, name := range ParseDetLanguages() {
		name := name
		t.Run(name, func(t *testing.T) {
			rows, err := LoadClasses(filepath.Join("..", "..", ParseDetClassesPath(name)))
			if err != nil {
				t.Fatal(err)
			}
			r := &Runner{
				ParseDetLang: name,
				WorkDir:      t.TempDir(),
				MaxTier:      3,
				RunnerClass:  "hermetic-test",
			}
			rep, err := r.RunParseDeterminism(t.Context(), corpus.Manifest{}, rows, parityreport.Provenance{})
			if err != nil {
				t.Fatalf("RunParseDeterminism: %v", err)
			}
			if got, want := len(rep.Classes), CountChangeClasses(rows)*len(parseDetAxes()); got != want {
				t.Fatalf("report carries %d rows, want the crossed count %d", got, want)
			}
			for _, c := range rep.Classes {
				if c.Verdict != parityreport.VerdictSkipped {
					t.Errorf("row %s is %s, want SKIPPED with the abstention named", c.ID, c.Verdict)
				}
				if !strings.Contains(c.Detail, "NO CORPUS ENTRY") {
					t.Errorf("row %s does not name the mechanism: %q", c.ID, c.Detail)
				}
				if c.AxisNote == "" {
					t.Errorf("row %s carries no axis note; a reader cannot tell which cell it ran under", c.ID)
				}
			}
			if rep.Complete {
				t.Error("a run in which every row SKIPPED reported itself complete")
			}
			if rep.Publishable {
				t.Error("a run in which every row SKIPPED reported itself publishable")
			}
			if rep.Family != FamilyParseDet(name) {
				t.Errorf("Family = %q, want %q", rep.Family, FamilyParseDet(name))
			}
			if rep.MatrixSource != ParseDetClassesPath(name) {
				t.Errorf("MatrixSource = %q, want %q", rep.MatrixSource, ParseDetClassesPath(name))
			}
		})
	}
}

// TestParseDet_RefusalSetsAreIdenticalAcrossTwoDispatches is the two-dispatch
// discipline (SW-175 AC-5) at the level this tree can assert it: two
// independent runs of the same family produce bit-identical refusal sets and
// bit-identical verdict and count sets.
//
// It is the hermetic twin of the -refusal-diff / -verdict-diff / -counts-diff
// comparison the story runs against the built binary, and it is the half that
// stays green when the pins are missing.
//
// ITS LIMIT, STATED (SW-200 review round 1, major M3, second half): it also
// passes corpus.Manifest{}, so both dispatches abstain before any row is
// measured. What it can catch is nondeterminism in the REPORTING layer — map
// iteration leaking into a row order, a digest that folds in a timestamp, an
// abstention string built non-deterministically. What it cannot catch is
// nondeterminism in the MEASUREMENT: two full index passes over a real tree
// serialising to different bytes. That half of AC-5 is carried by artifacts,
// not by this test — the twelve checked-in dispatch pairs under
// docs/eval/runs/2026-08-26-Darwin-ARM64/*/raw/ — and it cannot be asserted
// here without executing the product binary over a materialized corpus, which
// is a network clone and a multi-minute full index per row. Filed as
// PARITY-013 rather than papered over.
func TestParseDet_RefusalSetsAreIdenticalAcrossTwoDispatches(t *testing.T) {
	for _, name := range ParseDetLanguages() {
		rows, err := LoadClasses(filepath.Join("..", "..", ParseDetClassesPath(name)))
		if err != nil {
			t.Fatal(err)
		}
		dispatch := func() parityreport.Report {
			r := &Runner{ParseDetLang: name, WorkDir: t.TempDir(), MaxTier: 3, RunnerClass: "hermetic-test"}
			rep, rerr := r.RunParseDeterminism(t.Context(), corpus.Manifest{}, rows, parityreport.Provenance{})
			if rerr != nil {
				t.Fatalf("%s: %v", name, rerr)
			}
			return rep
		}
		a, b := dispatch(), dispatch()
		if a.VerdictSetDigest() != b.VerdictSetDigest() {
			t.Errorf("%s: verdict sets differ between two dispatches", name)
		}
		if a.CountsSetDigest() != b.CountsSetDigest() {
			t.Errorf("%s: count sets differ between two dispatches", name)
		}
		if a.RefusalSetDigest() != b.RefusalSetDigest() {
			t.Errorf("%s: refusal sets differ between two dispatches:\n a: %v\n b: %v",
				name, a.RefusalSet(), b.RefusalSet())
		}
	}
}
