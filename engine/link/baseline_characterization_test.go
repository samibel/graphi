package link

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// SW-222 (AX-02) characterization baseline for the LINK RESOLVER registry.
//
// Written and made green BEFORE the registry-lifecycle refactor; must pass
// UNCHANGED after it. `link.Linker` holds a language-keyed resolver map whose
// collision policy is LAST-WINS. Languages() feeds the P1 trust capability
// matrix (`cross-file-heuristic`), so its contents are a published claim.

type charLinkResolver struct {
	lang string
	tag  string
}

func (r charLinkResolver) Language() string { return r.lang }
func (r charLinkResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return nil
}

// TestBaseline_LinkRegistry_DefaultLanguages pins the exact shipped resolver
// set. This list is a published capability claim, not an implementation detail.
func TestBaseline_LinkRegistry_DefaultLanguages(t *testing.T) {
	got := New().Languages()
	want := []string{
		"bash", "c", "c_sharp", "cpp", "go", "java", "javascript", "kotlin",
		"lua", "php", "python", "ruby", "rust", "sql", "tsx", "typescript",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("linker resolver languages drifted:\n got: %v\nwant: %v", got, want)
	}
}

// TestBaseline_LinkRegistry_LastWinsOverride pins the collision policy.
func TestBaseline_LinkRegistry_LastWinsOverride(t *testing.T) {
	l := New()
	l.Register(charLinkResolver{lang: "toy", tag: "first"})
	l.Register(charLinkResolver{lang: "toy", tag: "second"})

	r, ok := l.resolvers["toy"]
	if !ok {
		t.Fatal("toy resolver missing after registration")
	}
	cr, ok := r.(charLinkResolver)
	if !ok {
		t.Fatalf("unexpected resolver type %T", r)
	}
	if cr.tag != "second" {
		t.Fatalf("override did not win: tag = %q, want %q", cr.tag, "second")
	}
	if n := len(l.Languages()); n != 17 {
		t.Fatalf("override added a language: %d, want 17 (16 defaults + toy)", n)
	}
}

// TestBaseline_LinkRegistry_UnregisteredLanguageIsANoOp pins the documented
// silent behaviour: Link over a language with no resolver produces nothing and
// is not an error.
func TestBaseline_LinkRegistry_UnregisteredLanguageIsANoOp(t *testing.T) {
	l := New()
	nodes, edges, st, err := l.Link("no-such-language", []FileRefs{{SourcePath: "a.x"}}, BuildIndex(nil))
	if err != nil {
		t.Fatalf("Link over an unregistered language must not error: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("Link over an unregistered language produced %d nodes / %d edges", len(nodes), len(edges))
	}
	if st != (Stats{}) {
		t.Fatalf("Link over an unregistered language produced stats: %+v", st)
	}
}

// keep model referenced so the file compiles independently of resolver churn.
var _ = model.NodeId("")
