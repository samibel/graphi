package v9

import (
	"context"
	"testing"
	"testing/fstest"
)

func identifierDiscoveryRepository() fstest.MapFS {
	return fstest.MapFS{
		"command.go": {Data: []byte("package p\n\n// Command is a command.\ntype Command struct {\n\t// Aliases is an array of aliases.\n\tAliases []string\n\n\t// PostRun runs after Run.\n\tPostRun func(cmd *Command, args []string)\n\t// PostRunE is PostRun with an error.\n\tPostRunE func(cmd *Command, args []string) error\n}\n\nfunc use(c *Command) {\n\tc.Aliases = append(c.Aliases, \"x\")\n\tif c.PostRun != nil {\n\t\tc.PostRun(c, nil)\n\t}\n}\n")},
	}
}

// TestDiscoveryTreatsAStructFieldAsADeclarationOfItsName pins the compact/14
// discovery rule: a line that declares a struct field (or any `name type`
// line) declares that name, so an exact-identifier question about a field
// reads the field's line first, not a use of it. Before this the only
// declarations were func/type/var/const, and a field's own line lost to
// every call site that mentioned it.
func TestDiscoveryTreatsAStructFieldAsADeclarationOfItsName(t *testing.T) {
	transcript, _, err := grepReadV2WithFiles(context.Background(), identifierDiscoveryRepository(), "Aliases")
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Mode != GrepReadV2ExactIdentifier || len(transcript.Reads) == 0 {
		t.Fatalf("transcript = %+v", transcript)
	}
	first := transcript.Reads[0]
	if first.Path != "command.go" || first.StartLine > 6 || first.EndLine < 6 {
		t.Fatalf("first read = %+v, want the field line 6", first)
	}
	if grepReadV2DeclarationLine([]byte("\tc.Aliases = append(c.Aliases, \"x\")"), "Aliases") || grepReadV2DeclarationLine([]byte("\tAliases = other"), "Aliases") || grepReadV2DeclarationLine([]byte("\tAliases: []string{},"), "Aliases") {
		t.Fatal("an assignment or a keyed literal is not a declaration")
	}
}

// TestDiscoveryFoldsASeparatedTokenToItsIdentifier: "post-run" is a question
// about PostRun. A token made of identifier parts joined by '-' or '_' is an
// exact-identifier search whose hits are whole identifiers equal to the
// joined form ignoring case; PostRunE is a different identifier.
func TestDiscoveryFoldsASeparatedTokenToItsIdentifier(t *testing.T) {
	mode, patterns := grepReadV2QueryPlan("post-run")
	if mode != GrepReadV2ExactIdentifier || len(patterns) != 1 || patterns[0] != "post-run" {
		t.Fatalf("plan = %s %v", mode, patterns)
	}
	transcript, _, err := grepReadV2WithFiles(context.Background(), identifierDiscoveryRepository(), "post-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Reads) == 0 || transcript.Reads[0].StartLine > 9 || transcript.Reads[0].EndLine < 9 {
		t.Fatalf("reads = %+v, want the PostRun field line 9 first", transcript.Reads)
	}
	column, actual := grepReadV2FoldedIdentifierColumn([]byte("\tPostRunE func() error // PostRun"), "postrun")
	if column == 0 || actual != "PostRun" {
		t.Fatalf("folded match = %d %q, want the whole identifier PostRun only", column, actual)
	}
	if column, _ := grepReadV2FoldedIdentifierColumn([]byte("\tPostRunE func() error"), "postrun"); column != 0 {
		t.Fatal("PostRunE matched the folded token postrun")
	}
	if mode, _ := grepReadV2QueryPlan("post-run hooks"); mode != GrepReadV2NaturalLanguage {
		t.Fatalf("a multi-word question is not an identifier: %s", mode)
	}
}

func TestQueryPlanDistinguishesCamelCaseFromSentenceCapitalization(t *testing.T) {
	_, patterns := grepReadV2QueryPlan("How do Flags reach getCompletions and ValidArgsFunction")
	want := []string{"flag", "reach", "getcompletions", "validargsfunction"}
	if len(patterns) != len(want) {
		t.Fatalf("patterns = %v, want %v", patterns, want)
	}
	for i := range want {
		if patterns[i] != want[i] {
			t.Fatalf("patterns = %v, want %v", patterns, want)
		}
	}
}
