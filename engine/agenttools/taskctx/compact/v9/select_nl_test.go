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

// TestNamedNaturalLanguageLeadReclaimsCompletedLowerCitations pins the
// depth-first contract for an explicitly named implementation. The lead fits
// whole, but the anchor lines of five lower-ranked, already-complete helpers
// initially consume enough budget to cut it. Those helpers must not become
// undeletable merely because their declarations are one line long.
func TestNamedNaturalLanguageLeadReclaimsCompletedLowerCitations(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	lead := longBody("Execute", 65, 0, "")
	nlEvidence(&evidence, &items, "lead", "execute.go", "p.Execute", "function", 1, lead, 1)
	for i := 0; i < 5; i++ {
		params := make([]string, 12)
		for j := range params {
			params[j] = fmt.Sprintf("arg%d int", j)
		}
		text := fmt.Sprintf("func Helper%d(%s) {}", i, strings.Join(params, ", "))
		nlEvidence(&evidence, &items, fmt.Sprintf("helper-%d", i), "helpers.go", fmt.Sprintf("p.Helper%d", i), "function", 10+i, text, i+2)
	}

	sources, _, err := compactTaskContextSelect("how does Execute coordinate the workflow", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := strings.Count(lead, "\n") + 1
	for _, source := range sources {
		if source.Path == "execute.go" && source.Start == 1 && source.End == wantEnd {
			return
		}
	}
	t.Fatalf("named lead was cut despite fitting the budget: want execute.go:1-%d, sources=%+v", wantEnd, sources)
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

// TestNaturalLanguageLeadCompletesAffordableMarkdownSection keeps the source
// budget, rather than a smaller arbitrary unit limit, as the final authority
// for a documentation lead. A coherent section just over 230 words is still
// cheaper and more useful than returning only its middle.
func TestNaturalLanguageLeadCompletesAffordableMarkdownSection(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	var section strings.Builder
	section.WriteString("### Completion descriptions\n")
	for i := 0; i < 58; i++ {
		fmt.Fprintf(&section, "context filler words %d\n", i)
	}
	section.WriteString("disable completion descriptions here")
	text := section.String()
	nlEvidence(&evidence, &items, "docs", "completion.md", "", "", 10, text, 1)
	for i := 0; i < 5; i++ {
		params := make([]string, 10)
		for j := range params {
			params[j] = fmt.Sprintf("arg%d int", j)
		}
		helper := fmt.Sprintf("func Helper%d(%s) {}", i, strings.Join(params, ", "))
		nlEvidence(&evidence, &items, fmt.Sprintf("helper-%d", i), "helpers.go", fmt.Sprintf("p.Helper%d", i), "function", 100+i, helper, i+2)
	}

	sources, _, err := compactTaskContextSelect("how do I disable completion descriptions", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := 10 + strings.Count(text, "\n")
	if len(sources) == 0 || sources[0].Start != 10 || sources[0].End != wantEnd {
		t.Fatalf("affordable Markdown lead was fragmented: want 10-%d, sources=%+v", wantEnd, sources)
	}
}

func TestNaturalLanguageDedupePrefersHydratedMarkdownSection(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	raw := "intro zero\nintro one\n### Usage template\nProvide your own usage template.\n\n```go\ncmd.SetUsageTemplate(s)"
	hydrated := "### Usage template\nProvide your own usage template.\n\n```go\ncmd.SetUsageTemplate(s)\n```\n"
	nlEvidence(&evidence, &items, "raw", "guide.md", "content.Usage", "type", 1, raw, 1)
	nlEvidence(&evidence, &items, "hydrated-001", "guide.md", "content.Usage", "type", 3, hydrated, 1)

	sources, _, err := compactTaskContextSelect("how do I provide my own usage template", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Path == "guide.md" && source.Start == 3 && source.End == 9 {
			return
		}
	}
	t.Fatalf("hydrated Markdown section lost to overlapping raw window: %+v", sources)
}

func TestNaturalLanguageDedupePrefersSmallHydratedDeclaration(t *testing.T) {
	raw := nlCandidate{compactTaskContextCandidate: compactTaskContextCandidate{
		item:  contract.Evidence{RefID: "grepread-read-1", Path: "completion.go"},
		lines: make([]string, 40), start: 820, anchor: 21,
	}}
	hydrated := nlCandidate{compactTaskContextCandidate: compactTaskContextCandidate{
		item:  contract.Evidence{RefID: "grepread-hydrated-1", Path: "completion.go"},
		lines: []string{"func findFlag() {", "\tlookup()", "}"}, start: 841, anchor: 0,
	}, density: 1000}
	got := nlDedupe([]nlCandidate{hydrated, raw})
	if len(got) != 1 || got[0].item.RefID != "grepread-hydrated-1" {
		t.Fatalf("arbitrary read window replaced the coherent declaration: %+v", got)
	}
}

func TestNaturalLanguageFallbackPrefersDenseSmallDeclaration(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	var broad strings.Builder
	broad.WriteString("func ParseFlags() {\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&broad, "\tparse%d(arguments) // parsed arguments before separator\n", i)
	}
	broad.WriteString("}")
	nlEvidence(&evidence, &items, "grepread-hydrated-broad", "command.go", "p.ParseFlags", "function", 100, broad.String(), 0)
	nlEvidence(&evidence, &items, "grepread-hydrated-dense", "command.go", "p.ArgsLenAtDash", "method", 200,
		"// ArgsLenAtDash reports parsed arguments before the separator.\nfunc (c *Command) ArgsLenAtDash() int {\n\treturn c.Flags().ArgsLenAtDash()\n}", 0)

	sources, _, err := compactTaskContextSelect("how many parsed arguments appeared before a separator", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 || sources[0].Start != 200 {
		t.Fatalf("dense small declaration did not win the one fallback slot: %+v", sources)
	}
}

func TestNaturalLanguageSeedCapKeepsDirectCallee(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	for i := 0; i < 4; i++ {
		nlEvidence(&evidence, &items, fmt.Sprintf("generic-%d", i), fmt.Sprintf("generic%d.go", i), fmt.Sprintf("p.Generic%d", i), "function", 1,
			fmt.Sprintf("func Generic%d() { work() }", i), i+1)
	}
	nlEvidence(&evidence, &items, "caller", "chain.go", "p.Flag", "method", 10,
		"func Flag() {\n\tpersistentFlag()\n}", 5)
	nlEvidence(&evidence, &items, "distractor", "distractor.go", "p.Distractor", "function", 1,
		"func Distractor() { work() }", 6)
	nlEvidence(&evidence, &items, "callee", "chain.go", "p.persistentFlag", "method", 13,
		"\nfunc persistentFlag() {\n\tfindParent()\n}", 7)

	sources, _, err := compactTaskContextSelect("how does flag lookup climb the parent chain", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Path == "chain.go" && source.Start == 10 && source.End == 16 {
			return
		}
	}
	t.Fatalf("direct caller/callee chain was cut at the seed cap: %+v", sources)
}

func TestSpecificRetrievalSeedCanLeadGenericSeed(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	nlEvidence(&evidence, &items, "generic", "guide.md", "content.Generating", "type", 1,
		"## Documentation\nGeneral overview.", 1)
	nlEvidence(&evidence, &items, "specific", "rest.md", "content.Customize", "type", 10,
		"## Customize ReST links\nUse linkHandler to customize generated ReST documentation links.", 5)

	sources, _, err := compactTaskContextSelect("how do I customize links in generated ReST documentation", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 || sources[0].Path != "rest.md" {
		t.Fatalf("specific retrieval seed did not lead generic documentation: %+v", sources)
	}
}

func TestNaturalLanguageGrowthFollowsNextQuerySignal(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	var body strings.Builder
	body.WriteString("func workflow() {\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&body, "\tbefore%d := prepare(value)\n", i)
	}
	body.WriteString("\tuse(sharedMarker)\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&body, "\tmiddle%d := prepare(value)\n", i)
	}
	body.WriteString("\tfinish(sharedMarker)\n}")
	nlEvidence(&evidence, &items, "lead", "workflow.go", "p.workflow", "function", 1, body.String(), 1)

	sources, _, err := compactTaskContextSelect("how does sharedMarker connect both stages", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	want := 1 + 1 + 45 + 1 + 45 + 1
	for _, source := range sources {
		if source.Path == "workflow.go" && source.Start <= want && source.End >= want {
			return
		}
	}
	t.Fatalf("growth stopped before the next query signal at line %d: %+v", want, sources)
}

func TestNamedLongFunctionReclaimsItsDepthQuota(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	nlEvidence(&evidence, &items, "lead", "completion.go", "p.checkIfFlagCompletion", "function", 1,
		longBody("checkIfFlagCompletion", 150, 0, ""), 1)
	for i := 0; i < 5; i++ {
		nlEvidence(&evidence, &items, fmt.Sprintf("helper-%d", i), fmt.Sprintf("helper%d.go", i), fmt.Sprintf("p.Helper%d", i), "function", 10,
			longBody(fmt.Sprintf("Helper%d", i), 25, 0, ""), i+2)
	}

	sources, _, err := compactTaskContextSelect("how does checkIfFlagCompletion split the current flag value", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Path == "completion.go" && len(strings.Fields(source.Text)) >= 190 {
			return
		}
	}
	t.Fatalf("named long function was starved below its depth quota: %+v", sources)
}

func TestNamedLongFunctionAnchorsOnBestQuestionCluster(t *testing.T) {
	var evidence []contract.Evidence
	var items []contract.Item
	var body strings.Builder
	body.WriteString("func (c *Command) getCompletions(args []string) error {\n")
	body.WriteString("\tflagCompletion := true\n")
	for i := 0; i < 110; i++ {
		fmt.Fprintf(&body, "\tstep%d := prepare(step%d)\n", i, i)
	}
	answerStart := 1 + 1 + 1 + 110
	body.WriteString("\tvar completionFn func(*Command)\n")
	body.WriteString("\tif flag != nil && flagCompletion {\n")
	body.WriteString("\t\tcompletionFn = flagCompletionFunctions[flag]\n")
	body.WriteString("\t} else {\n")
	body.WriteString("\t\tcompletionFn = c.ValidArgsFunction\n")
	body.WriteString("\t}\n\treturn nil\n}")
	nlEvidence(&evidence, &items, "grepread-hydrated-lead", "completions.go", "p.Command.getCompletions", "method", 1, body.String(), 0)

	sources, _, err := compactTaskContextSelect(
		"how does getCompletions decide between the flag completion function and ValidArgsFunction", evidence, items, 325,
	)
	if err != nil {
		t.Fatal(err)
	}
	answerEnd := answerStart + 5
	for _, source := range sources {
		if source.Path == "completions.go" && source.Start <= answerStart && source.End >= answerEnd {
			return
		}
	}
	t.Fatalf("named declaration missed its strongest question cluster %d-%d: %+v", answerStart, answerEnd, sources)
}

func TestNLHasSeparatedStrongSignals(t *testing.T) {
	if !nlHasSeparatedStrongSignals([]int{20, 0, 0, 0, 0, 0, 0, 0, 20}) {
		t.Fatal("separated strong query lines were not detected")
	}
	if nlHasSeparatedStrongSignals([]int{20, 0, 20}) {
		t.Fatal("nearby lines were treated as separate answer regions")
	}
}

func TestNLContainsPatternMatchesSilentEInflection(t *testing.T) {
	if !nlContainsPattern("enables combining existing checks", "combine") {
		t.Fatal("base verb did not match its ing inflection")
	}
	if nlContainsPattern("unrelated words", "combine") {
		t.Fatal("base verb matched unrelated prose")
	}
}

func TestNLMarkdownParagraphWithFence(t *testing.T) {
	lines := []string{
		"## Positional arguments",
		"Earlier overview.",
		"",
		"Moreover, MatchAll combines positional argument validators.",
		"The following example applies both checks:",
		"",
		"```go",
		"Args: MatchAll(ExactArgs(2), OnlyValidArgs),",
		"```",
		"",
		"Unrelated next paragraph.",
	}
	from, to, ok := nlMarkdownParagraphWithFence(lines, 3)
	if !ok || from != 3 || to != 8 {
		t.Fatalf("paragraph plus example = %d-%d, %t; want 3-8", from, to, ok)
	}
}
