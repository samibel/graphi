package v9

import (
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

// namedDeclarationEvidence builds one long named declaration plus a tail of
// one-line neighbour citations, which is the shape every exact-identifier
// lookup produces: the named body, then the wrappers and call sites that
// mention it. bodyLines controls how much of the source budget the complete
// declaration needs.
func namedDeclarationEvidence(symbol string, bodyLines, neighbours int) ([]contract.Evidence, []contract.Item) {
	lines := []string{"// " + symbol + " runs the command.", "func " + symbol + "() error {"}
	for i := 0; i < bodyLines; i++ {
		lines = append(lines, fmt.Sprintf("\tstep%d := resolve(step%d)", i, i))
	}
	lines = append(lines, "\treturn nil", "}")
	body := strings.Join(lines, "\n")
	evidence := []contract.Evidence{{
		RefID: "named", Path: "run.go", Line: 1, Span: fmt.Sprintf("1-%d", len(lines)),
		Role: "snippet", Snippet: body, TextHash: shape.TextHash(body),
	}}
	items := []contract.Item{{
		RefID: "named-item", Rank: 1,
		Reason: fmt.Sprintf("candidate: function p.%s (run.go:2) score 9", symbol), EvidenceRefIDs: []string{"named"},
	}}
	for i := 0; i < neighbours; i++ {
		ref := fmt.Sprintf("near-%d", i)
		text := fmt.Sprintf("\tif err := %s(); err != nil { return err } // neighbour %d", symbol, i)
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: fmt.Sprintf("caller%d.go", i), Line: 1, Span: "1-1",
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		})
		items = append(items, contract.Item{
			RefID: fmt.Sprintf("near-item-%d", i), Rank: i + 2,
			Reason: fmt.Sprintf("candidate: function p.caller%d (caller%d.go:1) score 1", i, i), EvidenceRefIDs: []string{ref},
		})
	}
	return evidence, items
}

// TestNamedDeclarationSurvivesNeighbourAnchors pins the rule this candidate
// adds: when a question names a declaration and that declaration's complete
// definition fits the source budget, the response must contain all of it. Its
// last branches and its return are the part of an implementation a reader
// cannot guess, and they used to be lost because every neighbour citation was
// charged for its anchor line before the named body was completed.
func TestNamedDeclarationSurvivesNeighbourAnchors(t *testing.T) {
	evidence, items := namedDeclarationEvidence("ExecuteC", 24, 4)
	whole := evidence[0]
	budget := len(strings.Fields(whole.Snippet)) + 2
	sources, used, err := compactTaskContextSelect("ExecuteC", evidence, items, budget)
	if err != nil {
		t.Fatal(err)
	}
	if used > budget {
		t.Fatalf("selection used %d of a %d-field budget", used, budget)
	}
	for _, source := range sources {
		if source.Path == whole.Path && source.Start == 1 && source.Text == whole.Snippet {
			return
		}
	}
	t.Fatalf("named declaration was left cut by neighbour anchors: %#v", sources)
}

// TestNamedDeclarationReclamationStopsWhenTheDefinitionFits is the other half
// of the rule. Reclamation is a payment, not a policy: it must give back the
// fewest neighbour citations that let the definition close, and keep the rest.
func TestNamedDeclarationReclamationStopsWhenTheDefinitionFits(t *testing.T) {
	evidence, items := namedDeclarationEvidence("ExecuteC", 24, 4)
	whole := evidence[0]
	neighbour := len(strings.Fields(evidence[1].Snippet))
	// Three neighbours' anchors fit beside the complete definition; the fourth
	// does not, so exactly one must be given back.
	budget := len(strings.Fields(whole.Snippet)) + 3*neighbour
	sources, used, err := compactTaskContextSelect("ExecuteC", evidence, items, budget)
	if err != nil {
		t.Fatal(err)
	}
	if used > budget {
		t.Fatalf("selection used %d of a %d-field budget", used, budget)
	}
	complete := false
	for _, source := range sources {
		if source.Path == whole.Path && source.Text == whole.Snippet {
			complete = true
		}
	}
	if !complete {
		t.Fatalf("named declaration was left cut: %#v", sources)
	}
	if len(sources) != 4 {
		t.Fatalf("emitted %d sources, want the definition plus the three neighbours that still fit: %#v", len(sources), sources)
	}
}

// TestNamedDeclarationTooLargeToFinishKeepsItsExistingDepth pins the boundary
// the reclamation must not cross. A definition larger than the whole budget
// cannot be finished by giving anything back, so the projector's existing
// behaviour — spend the budget on that one implementation — must be untouched.
func TestNamedDeclarationTooLargeToFinishKeepsItsExistingDepth(t *testing.T) {
	evidence, items := namedDeclarationEvidence("ExecuteC", 60, 4)
	budget := len(strings.Fields(evidence[0].Snippet)) / 2
	sources, used, err := compactTaskContextSelect("ExecuteC", evidence, items, budget)
	if err != nil {
		t.Fatal(err)
	}
	if used > budget {
		t.Fatalf("selection used %d of a %d-field budget", used, budget)
	}
	if len(sources) != 1 || sources[0].Path != "run.go" {
		t.Fatalf("unaffordable named declaration changed shape: %#v", sources)
	}
	if used < budget-4 {
		t.Fatalf("unaffordable named declaration used only %d of %d fields", used, budget)
	}
}

// TestNamedDeclarationReclamationKeepsRankOrder guards the one thing the
// reclamation may never do: change which regions rank above which. It only
// ever drops from the bottom.
func TestNamedDeclarationReclamationKeepsRankOrder(t *testing.T) {
	evidence, items := namedDeclarationEvidence("ExecuteC", 24, 4)
	budget := len(strings.Fields(evidence[0].Snippet)) + 2
	sources, _, err := compactTaskContextSelect("ExecuteC", evidence, items, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 || sources[0].Path != "run.go" {
		t.Fatalf("named declaration is not the first source: %#v", sources)
	}
	previous := -1
	for _, source := range sources[1:] {
		var index int
		if _, err := fmt.Sscanf(source.Path, "caller%d.go", &index); err != nil {
			t.Fatalf("unexpected source %q", source.Path)
		}
		if index <= previous {
			t.Fatalf("reclamation reordered neighbours: %#v", sources)
		}
		previous = index
	}
}
