package v9

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// TestHydrationExpandsEveryRankedMarkdownHeadingForNaturalLanguage pins the
// compact/10 rule: for a natural-language question, every ranked
// documentation heading is hydrated to its whole section, whatever the
// question's shape. The retrieval row for a heading is the heading line; the
// answer is usually a paragraph further down the section, which the old
// heading-window never reached unless the question matched one of two
// hand-written shapes.
func TestHydrationExpandsEveryRankedMarkdownHeadingForNaturalLanguage(t *testing.T) {
	repository := fstest.MapFS{
		"site/content/completions/_index.md": {Data: []byte("# Completions\n\nintro\n\n### Descriptions for completions\n\nCobra supports descriptions.\n\nIf you don't want to show descriptions, add `--no-descriptions`.\n\n## Bash completions\n\nbash\n")},
	}
	items := []contract.Item{{
		RefID: "docs", Rank: 1,
		Reason: "candidate: type completions.Descriptions_for_completions (site/content/completions/_index.md:5) score 1",
	}}
	evidence, linked, err := compactTaskContextHydrateDefinitions(
		context.Background(), repository, nil, "how do I turn off the descriptions shown next to shell completions", items,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || len(linked) != 1 {
		t.Fatalf("ranked documentation heading was not hydrated: %#v", evidence)
	}
	if evidence[0].Path != "site/content/completions/_index.md" || evidence[0].Span != "5-10" {
		t.Fatalf("hydrated section = %s:%s, want the heading's whole section 5-10", evidence[0].Path, evidence[0].Span)
	}
}
