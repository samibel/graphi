package v9

import (
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

// nlEvidence adds one candidate to a synthetic bundle. seed > 0 marks a
// retrieval seed at that rank, exactly as the assembler stamps it.
func nlEvidence(evidence *[]contract.Evidence, items *[]contract.Item, ref, path, symbol, kind string, start int, text string, seed int) {
	lines := strings.Count(text, "\n") + 1
	*evidence = append(*evidence, contract.Evidence{
		RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, start+lines-1),
		Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
	})
	reason := fmt.Sprintf("related: %s %s (%s:%d)", kind, symbol, path, start)
	if seed > 0 {
		reason = fmt.Sprintf("candidate: %s %s (%s:%d) score 9 [seed %d, semantic_first]", kind, symbol, path, start, seed)
	}
	*items = append(*items, contract.Item{RefID: "item-" + ref, Rank: 1000 - seed, Reason: reason, EvidenceRefIDs: []string{ref}})
}

func longBody(name string, before, after int, middle string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s runs the thing.\nfunc (c *Command) %s(a []string) error {\n", name, name)
	for i := 0; i < before; i++ {
		fmt.Fprintf(&b, "\tstep%d := prepare(step%d)\n", i, i)
	}
	b.WriteString(middle)
	for i := 0; i < after; i++ {
		fmt.Fprintf(&b, "\tfinish%d := wrap(finish%d)\n", i, i)
	}
	b.WriteString("\treturn nil\n}")
	return b.String()
}

// TestNaturalLanguageSelectionFollowsRetrievalOrder pins the rule compact/10
// exists for. Retrieval ranked the long implementation first; a short helper
// whose *name* is made of the question's words ranked second; and a type
// whose name equals one question word is also a candidate. The old selector
// re-scored the helper and the type above the implementation and left the
// implementation as a one-line anchor. The answer is a region inside the
// implementation; it must arrive, and the implementation must be the region
// that receives depth.
func TestNaturalLanguageSelectionFollowsRetrievalOrder(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	answer := "\thelpVal, err := c.Flags().GetBool(\"help\")\n\tif helpVal {\n\t\treturn flag.ErrHelp\n\t}\n\tif !c.Runnable() {\n\t\treturn flag.ErrHelp\n\t}\n"
	nlEvidence(&evidence, &items, "e1", "command.go", "cobra.Command.execute", "method", 874, longBody("execute", 18, 40, answer), 1)
	nlEvidence(&evidence, &items, "e2", "flag_groups.go", "cobra.Command.ValidateFlagGroups", "method", 79,
		"// ValidateFlagGroups validates the flag groups and returns an error.\nfunc (c *Command) ValidateFlagGroups() error {\n\tif c.DisableFlagParsing {\n\t\treturn nil\n\t}\n\treturn validate(c)\n}", 2)
	nlEvidence(&evidence, &items, "e3", "command.go", "cobra.Command", "type", 47,
		"// Command is just that, a command for your application.\ntype Command struct {\n\tUse string\n\tShort string\n\tRun func(cmd *Command, args []string)\n}", 3)
	nlEvidence(&evidence, &items, "e4", "command.go", "cobra.Command.Flag", "method", 1794,
		"// Flag climbs up the command tree looking for matching flag.\nfunc (c *Command) Flag(name string) *flag.Flag {\n\treturn c.Flags().Lookup(name)\n}", 0)

	sources, used, err := compactTaskContextSelect("when does executing a command return flag.ErrHelp instead of running it", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	if used > 325 {
		t.Fatalf("used %d of 325", used)
	}
	answerStart := 874 + 2 + 18
	answerEnd := answerStart + 6
	largest, largestFields := "", 0
	covered := false
	for _, s := range sources {
		fields := len(strings.Fields(s.Text))
		if fields > largestFields {
			largest, largestFields = s.Path+":"+fmt.Sprint(s.Start), fields
		}
		if s.Path == "command.go" && s.Start <= answerStart && s.End >= answerEnd {
			covered = true
		}
	}
	if !covered {
		t.Fatalf("the answer region %d-%d inside the first-ranked implementation was not delivered: %+v", answerStart, answerEnd, sources)
	}
	if !strings.HasPrefix(largest, "command.go:8") {
		t.Fatalf("depth went to %s, want the first-ranked implementation: %+v", largest, sources)
	}
}

// TestNaturalLanguageSelectionKeepsLexicalFallback: when only the query-only
// grep discovery found the answer, that region must still be emitted. This
// is the recall path the committed development split depends on.
func TestNaturalLanguageSelectionKeepsLexicalFallback(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	nlEvidence(&evidence, &items, "e1", "command.go", "cobra.Command.Execute", "method", 1035, longBody("Execute", 10, 10, "\t_, err := c.ExecuteC()\n"), 1)
	nlEvidence(&evidence, &items, "e2", "command.go", "cobra.Command.Root", "method", 860, "// Root finds root command.\nfunc (c *Command) Root() *Command {\n\treturn c\n}", 2)
	text := "// SetArgs sets arguments for the command. It is set to os.Args[1:] by default, if desired, can be overridden\n// particularly useful when testing.\nfunc (c *Command) SetArgs(a []string) {\n\tc.args = a\n}"
	evidence = append(evidence, contract.Evidence{
		RefID: "grepread-hydrated-1", Path: "command.go", Line: 274, Span: "274-278",
		Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
	})
	items = append(items, contract.Item{RefID: "grepread:grepread-hydrated-1", EvidenceRefIDs: []string{"grepread-hydrated-1"}})

	sources, _, err := compactTaskContextSelect("how to set flags in test", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.Path == "command.go" && s.Start <= 276 && s.End >= 276 {
			return
		}
	}
	t.Fatalf("the lexical fallback region carrying the answer was dropped: %+v", sources)
}

// TestNaturalLanguageSelectionDeliversTheDocumentationSection: a Markdown
// section that retrieval ranked first is delivered as a paragraph around
// the lines the question names, not as a two-line heading fragment.
func TestNaturalLanguageSelectionDeliversTheDocumentationSection(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	var section strings.Builder
	section.WriteString("### Descriptions for completions\n\nCobra provides support for completion descriptions.\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&section, "Line %d of prose about descriptions in shells.\n", i)
	}
	section.WriteString("If you don't want to show descriptions in the completions, you can add `--no-descriptions` to the default `completion` command to disable them, like:\n\n```bash\n$ source <(helm completion bash --no-descriptions)\n```\n")
	nlEvidence(&evidence, &items, "hydrated-1", "site/content/completions/_index.md", "", "", 355, strings.TrimSuffix(section.String(), "\n"), 1)
	nlEvidence(&evidence, &items, "e2", "completions.go", "cobra.CompletionOptions", "type", 104,
		"// CompletionOptions are the options to control shell completion\ntype CompletionOptions struct {\n\tDisableDescriptions bool\n}", 2)

	sources, _, err := compactTaskContextSelect("how do I turn off the descriptions shown next to shell completions", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	target := 355 + 3 + 20
	for _, s := range sources {
		if strings.HasSuffix(s.Path, ".md") && s.Start <= target && s.End >= target+3 {
			return
		}
	}
	t.Fatalf("the documentation paragraph naming --no-descriptions (line %d) was not delivered: %+v", target, sources)
}
