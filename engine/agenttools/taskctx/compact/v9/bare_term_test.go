package v9

import (
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

// TestBareTermIsNotNamedByADocumentationHeading: a Markdown heading that
// spells the word is titled by it, not a declaration of it. Before this a
// heading "### Aliases" crowned the documentation section under the
// exact-lookup path while the field it documents was never emitted.
func TestBareTermIsNotNamedByADocumentationHeading(t *testing.T) {
	docs := []contract.Item{{RefID: "d", Reason: "primary: type completions.Aliases (site/content/completions/_index.md:402) score 300 [seed 1, search]"}}
	if compactTaskContextAnyItemNamed(docs, "Aliases") {
		t.Fatal("a documentation heading named the identifier")
	}
	code := []contract.Item{{RefID: "c", Reason: "primary: type cobra.Aliases (command.go:12) score 300 [seed 1, search]"}}
	if !compactTaskContextAnyItemNamed(code, "Aliases") {
		t.Fatal("a source declaration did not name the identifier")
	}
}

func bareTermWindow(ref, path string, start int, lines []string) contract.Evidence {
	text := strings.Join(lines, "\n")
	return contract.Evidence{
		RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, start+len(lines)-1),
		Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
	}
}

func bareTermSelect(t *testing.T, query string, declaration []string) []CompactTaskContextSource {
	t.Helper()
	mentions := make([]string, 12)
	for i := range mentions {
		mentions[i] = fmt.Sprintf("\tfor _, a := range c.Aliases { run(a, %d) } // post run aliases", i)
	}
	seedLines := []string{"### Aliases", "", "Aliases for nouns are supported.", "", "See below."}
	evidence := []contract.Evidence{
		bareTermWindow("e1", "site/content/completions/_index.md", 402, seedLines),
		bareTermWindow("grepread-read-1", "bash_completions.go", 633, mentions),
		bareTermWindow("grepread-read-2", "command.go", 60, declaration),
	}
	items := []contract.Item{{RefID: "i1", Rank: 9 << 20, Reason: "primary: type completions.Aliases (site/content/completions/_index.md:402) score 300 [seed 1, search]", EvidenceRefIDs: []string{"e1"}}}
	sources, _, err := compactTaskContextSelect(query, evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

// TestBareTermAnchorsOnTheLineThatDeclaresIt: with the documentation heading
// leading by retrieval order, the one discovery region emitted is the window
// that declares the identifier, anchored on its declaration line, not the
// window with the most mentions.
func TestBareTermAnchorsOnTheLineThatDeclaresIt(t *testing.T) {
	cases := map[string]struct {
		query       string
		declaration []string
		wantLine    int
	}{
		"field":           {"Aliases", []string{"\tUse string", "", "\t// Aliases is an array of aliases that can be used instead of the first word in Use.", "\tAliases []string", "", "\t// SuggestFor is an array of command names."}, 63},
		"separated token": {"post-run", []string{"\tRun func(cmd *Command, args []string)", "\t// RunE: Run but returns an error.", "\tRunE func(cmd *Command, args []string) error", "\t// PostRun: run after the Run command.", "\tPostRun func(cmd *Command, args []string)", "\t// PostRunE: PostRun but returns an error."}, 64},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sources := bareTermSelect(t, tc.query, tc.declaration)
			for _, source := range sources {
				if source.Path == "command.go" && source.Start <= tc.wantLine && source.End >= tc.wantLine {
					return
				}
			}
			t.Fatalf("declaration line %d not emitted: %+v", tc.wantLine, sources)
		})
	}
}

func TestSeparatedIdentifierKeepsAdjacentFieldFamily(t *testing.T) {
	declaration := []string{
		"\tRun func(cmd *Command, args []string)",
		"\t// RunE: Run but returns an error.",
		"\tRunE func(cmd *Command, args []string) error",
		"\t// PostRun: run after the Run command.",
		"\tPostRun func(cmd *Command, args []string)",
		"\t// PostRunE: PostRun but returns an error.",
		"\tPostRunE func(cmd *Command, args []string) error",
		"\t// PersistentPostRun: children inherit and execute after PostRun.",
		"\tPersistentPostRun func(cmd *Command, args []string)",
		"\t// PersistentPostRunE: PersistentPostRun but returns an error.",
		"\tPersistentPostRunE func(cmd *Command, args []string) error",
	}
	sources := bareTermSelect(t, "post-run", declaration)
	for _, source := range sources {
		if source.Path == "command.go" && source.Start <= 64 && source.End >= 70 {
			return
		}
	}
	t.Fatalf("separated lifecycle field lost its adjacent variants: %+v", sources)
}
