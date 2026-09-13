package context

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDefinitionWindowBudgetAndSourceRoundtrip(t *testing.T) {
	reader := memReader{"source.go": "func Check() {\n// setup\n// setup\n// setup\nif requiredFlag {\nreturn err\n}\n}"}
	full, err := winnow(reader, Candidate{Path: "source.go", StartLine: 1, EndLine: 8}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for budget := 1; budget < 30; budget++ {
		s := definitionWindow("where required flags fail", full, budget)
		if s.Text == "" {
			continue
		}
		text, _, err := reader.ReadSpan(s.Citation.Path, Span{Start: s.Citation.StartLine, End: s.Citation.EndLine})
		if err != nil || text != s.Text {
			t.Fatalf("budget %d citation fails roundtrip: %q != %q (%v)", budget, text, s.Text, err)
		}
		if s.Tokens != countTokens(s.Text) || s.Tokens > budget {
			t.Fatalf("budget %d: %+v", budget, s)
		}
	}
	s := definitionWindow("requiredFlag", full, 5)
	if !strings.Contains(s.Text, "requiredFlag") {
		t.Fatal("window never moved to body match")
	}
}

func TestDefinitionSelectionBudgetRoundtripAndDeterminism(t *testing.T) {
	reader := memReader{
		"a.go": "package a\nfunc A() {\n" + strings.Repeat("println(\"prepare\")\n", 30) + "println(\"target\")\n}\n",
		"b.go": "package b\nfunc B() { println(\"target\") }\n",
	}
	candidates := []Candidate{
		{Path: "a.go", Symbol: "a.A", StartLine: 2, EndLine: 2, Rank: 1},
		{Path: "b.go", Symbol: "b.B", StartLine: 2, EndLine: 2, Rank: 1},
	}
	for _, budget := range []int{0, 1, 6, 15, 40, 1200} {
		b, err := AssembleDefinitions(t.Context(), "target", candidates, Options{Budget: budget}, reader)
		if err != nil {
			t.Fatal(err)
		}
		other, err := AssembleDefinitions(t.Context(), "target", []Candidate{candidates[1], candidates[0]}, Options{Budget: budget}, reader)
		if err != nil || !reflect.DeepEqual(b, other) {
			t.Fatalf("budget %d: input ordering changed output: %v", budget, err)
		}
		tokens := 0
		for _, s := range b.Snippets {
			text, span, err := reader.ReadSpan(s.Citation.Path, Span{s.Citation.StartLine, s.Citation.EndLine})
			if err != nil || text != s.Text || span.Start != s.Citation.StartLine || span.End != s.Citation.EndLine {
				t.Fatalf("budget %d: source citation changed: %+v", budget, s)
			}
			tokens += countTokens(s.Text)
		}
		if tokens != b.Tokens || tokens > budget {
			t.Fatalf("budget %d: incorrect charge %+v", budget, b)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := AssembleDefinitions(ctx, "target", candidates, Options{Budget: 1200}, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestAssembleDefinitionsContentCanOutweighInitialRank(t *testing.T) {
	reader := memReader{}
	var candidates []Candidate
	for i := 0; i < 6; i++ {
		path := fmt.Sprintf("p%d.go", i)
		body := strings.Repeat("println(\"irrelevant filler\")\n", 100)
		if i == 5 {
			body = "// Attach the child and set its parent pointer.\nchild.parent = parent\nparent.children = append(parent.children, child)\n"
		}
		reader[path] = "package p\nfunc Work() {\n" + body + "}\n"
		candidates = append(candidates, Candidate{Path: path, StartLine: 2, EndLine: 2, Symbol: "p.Work", Rank: float64(i)})
	}
	b, err := AssembleDefinitions(t.Context(), "attach child parent pointer", candidates, Options{Budget: 60}, reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range b.Snippets {
		if s.Citation.Path == "p5.go" && s.Citation.StartLine == 2 && s.Citation.EndLine == 6 {
			return
		}
	}
	t.Fatalf("relevant complete declaration lost to unrelated early candidates: %+v", b)
}

func TestAssembleDefinitionsReservesExactQueryBasename(t *testing.T) {
	reader := memReader{}
	var candidates []Candidate
	for i := 0; i < 15; i++ {
		candidatePath := fmt.Sprintf("topic-%02d.md", i)
		if i == 0 {
			candidatePath = "command.go"
		}
		reader[candidatePath] = "irrelevant\n"
		candidates = append(candidates, Candidate{Path: candidatePath, StartLine: 1, EndLine: 1, Rank: float64(i)})
	}
	reader["site/docgen/man.md"] = "opaque\n"
	candidates = append(candidates, Candidate{Path: "site/docgen/man.md", StartLine: 1, EndLine: 1, Rank: 15})

	bundle, err := AssembleDefinitions(t.Context(), "how to generate man pages for a command tree", candidates, Options{Budget: 2}, reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range bundle.Snippets {
		if snippet.Citation.Path == "site/docgen/man.md" {
			return
		}
	}
	t.Fatalf("exact query basename lost after candidate admission: %+v", bundle.Snippets)
}

func TestAssembleDefinitionsReservesSpaceForLaterSmallDeclarations(t *testing.T) {
	reader := memReader{"a.go": "package a\nfunc Large() {\n" + strings.Repeat("println(\"large body\")\n", 100) + "}\nfunc Small() { println(\"answer\") }"}
	candidates := []Candidate{
		{Path: "a.go", StartLine: 2, EndLine: 2, Symbol: "a.Large", Rank: 0},
		{Path: "a.go", StartLine: 104, EndLine: 104, Symbol: "a.Small", Rank: 1},
	}
	b, err := AssembleDefinitions(t.Context(), "answer", candidates, Options{Budget: 25}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if b.Tokens > 25 || len(b.Snippets) != 2 || !strings.Contains(b.Snippets[1].Text, "answer") {
		t.Fatalf("large first declaration starved the second: %+v", b)
	}
}

func TestAssembleDefinitionsDoesNotPayForDuplicateDeclaration(t *testing.T) {
	reader := memReader{"a.go": "package a\nfunc First() { println(\"first answer\") }\nfunc Second() { println(\"second answer\") }"}
	candidates := []Candidate{
		{Path: "a.go", StartLine: 2, EndLine: 2, Symbol: "a.First", Rank: 0},
		{Path: "a.go", StartLine: 2, EndLine: 2, Symbol: "a.First", Rank: 1},
		{Path: "a.go", StartLine: 3, EndLine: 3, Symbol: "a.Second", Rank: 2},
	}
	b, err := AssembleDefinitions(t.Context(), "answer", candidates, Options{Budget: 30}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snippets) != 2 || b.Snippets[0].Rank != 0 || b.Snippets[1].Rank != 2 || b.Tokens != 12 {
		t.Fatalf("duplicate consumed source budget or provenance changed: %+v", b)
	}
}

func TestSingleTopicKeepsDepthWhenCompleteSourcesFit(t *testing.T) {
	reader := memReader{}
	var candidates []Candidate
	for i := 0; i < 8; i++ {
		path := fmt.Sprintf("topic%d.go", i)
		body := "println(\"topic\")\n"
		if i == 7 {
			body = strings.Repeat("println(\"topic implementation\")\n", 350)
		}
		reader[path] = "package p\nfunc Topic() {\n" + body + "}\n"
		candidates = append(candidates, Candidate{Path: path, Symbol: "p.Topic", StartLine: 2, EndLine: 2, Rank: float64(i)})
	}
	b, err := AssembleDefinitions(t.Context(), "topic", candidates, Options{Budget: 1200}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snippets) != 8 || b.Snippets[7].Citation.EndLine != 353 || b.Tokens > 1200 {
		t.Fatalf("single-topic search needlessly fragmented a declaration: %+v", b)
	}
}

func TestGroupedConstantKeepsSurroundingDeclarationContext(t *testing.T) {
	reader := memReader{"completion.go": `package p

const (
	// ShellCompRequestCmd starts the completion protocol.
	ShellCompRequestCmd = "__complete"
	// ShellCompNoDescRequestCmd is its no-description variant.
	ShellCompNoDescRequestCmd = "__completeNoDesc"
)
`}
	b, err := AssembleDefinitions(t.Context(), "custom shell completion", []Candidate{{
		Path: "completion.go", StartLine: 5, EndLine: 5,
		Symbol: "p.ShellCompRequestCmd", Kind: "constant",
	}}, Options{Budget: 100, ContextLines: 4}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snippets) != 1 || !strings.Contains(b.Snippets[0].Text, "const (") ||
		!strings.Contains(b.Snippets[0].Text, "ShellCompNoDescRequestCmd") {
		t.Fatalf("grouped constant was narrowed to one value spec: %+v", b)
	}
}

func TestGroupedConstantContextDoesNotDisplaceCompleteDefinition(t *testing.T) {
	reader := memReader{
		"completion.go": `package p

const (
	// ShellCompRequestCmd starts the completion protocol.
	ShellCompRequestCmd = "__complete"
	// ShellCompNoDescRequestCmd is its no-description variant.
	ShellCompNoDescRequestCmd = "__completeNoDesc"
)
`,
		"dispatch.go": `package p

func Handle() {
	println("answer")
}
`,
	}
	candidates := []Candidate{
		{Path: "completion.go", StartLine: 5, EndLine: 5, Symbol: "p.ShellCompRequestCmd", Kind: "constant", Rank: 0},
		{Path: "dispatch.go", StartLine: 3, EndLine: 3, Symbol: "p.Handle", Kind: "function", Rank: 1},
	}
	core, err := AssembleDefinitions(t.Context(), "shell completion request", candidates, Options{Budget: 100, ContextLines: 0}, reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AssembleDefinitions(t.Context(), "shell completion request", candidates, Options{Budget: core.Tokens, ContextLines: 4}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snippets) != 2 {
		t.Fatalf("optional grouped context displaced a complete definition: core=%+v expanded=%+v", core, b)
	}
	for _, s := range b.Snippets {
		if s.Citation.Path == "dispatch.go" && (s.Citation.StartLine != 3 || s.Citation.EndLine != 5) {
			t.Fatalf("function body was truncated for optional sibling context: %+v", b)
		}
	}
}
