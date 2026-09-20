package retrieval

import (
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGrepReadV2_IsStructurallyJudgementBlindAndDeterministic(t *testing.T) {
	signature := reflect.TypeOf(GrepReadV2)
	if signature.NumIn() != 2 || signature.In(0) != reflect.TypeOf((*fs.FS)(nil)).Elem() || signature.In(1).Kind() != reflect.String || signature.NumOut() != 1 || signature.Out(0) != reflect.TypeOf(GrepReadV2Transcript{}) {
		t.Fatalf("GrepReadV2 signature = %s, want func(fs.FS, string) GrepReadV2Transcript", signature)
	}

	repository := fstest.MapFS{
		"answer.go": {Data: []byte("package sample\n\nfunc ValidateRequiredFlags() {}\n")},
		"other.go":  {Data: []byte("package sample\n\nvar Command = 1\n")},
	}
	left := Query{Text: "where are required flags validated before a command runs", Judgements: []Judgement{{Path: "answer.go", StartLine: 3, EndLine: 3, Grade: 3}}}
	right := left
	right.Judgements = []Judgement{{Path: "poison.go", StartLine: 999, EndLine: 999, Grade: 0}}

	first := GrepReadV2(repository, left.Text)
	second := GrepReadV2(repository, right.Text)
	if !reflect.DeepEqual(first, second) || first.DigestSHA256() != second.DigestSHA256() {
		t.Fatal("judgement change altered query-only transcript")
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.Ledger.DigestSHA256() != second.Ledger.DigestSHA256() {
		t.Fatal("identical execution changed exact response bytes")
	}
}

func TestGrepReadV2_ExactDeclarationRanksBeforeEarlyUses(t *testing.T) {
	repository := fstest.MapFS{
		"a_test.go": {Data: []byte("package sample\n\n" + strings.Repeat("func testUse() { ExecuteC() }\n", GrepReadV2SearchLimit+5))},
		"z.go":      {Data: []byte("package sample\n\nfunc ExecuteC() error { return nil }\n")},
	}

	transcript := GrepReadV2(repository, "ExecuteC")
	if transcript.Mode != GrepReadV2ExactIdentifier || len(transcript.Reads) == 0 {
		t.Fatalf("mode/reads = %q/%d", transcript.Mode, len(transcript.Reads))
	}
	if got := transcript.Reads[0]; got.Path != "z.go" || got.StartLine != 3 {
		t.Fatalf("first read = %+v, want exact declaration z.go:3", got)
	}
	grep := string(transcript.Ledger.Responses[0].Bytes)
	if first := strings.Split(grep, "\n")[0]; first != "z.go:3:6:func ExecuteC() error { return nil }" {
		t.Fatalf("first ranked grep row = %q", first)
	}
}

func TestGrepReadV2_NaturalLanguageRanksCoverageAndDiversifiesReads(t *testing.T) {
	repository := fstest.MapFS{
		"a.go": {Data: []byte("package sample\n\n// command\n" + strings.Repeat("// command command command\n", 100))},
		"b.go": {Data: []byte("package sample\n\nfunc validateRequiredFlags() {}\n")},
		"c.go": {Data: []byte("package sample\n\n// required flags belong here\n")},
		"d.go": {Data: []byte("package sample\n\n// runs validation\n")},
		"e.go": {Data: []byte("package sample\n\n// validated command\n")},
	}

	transcript := GrepReadV2(repository, "where are required flags validated before a command runs")
	if transcript.Mode != GrepReadV2NaturalLanguage {
		t.Fatalf("mode = %q", transcript.Mode)
	}
	if len(transcript.Reads) < grepReadV2DiverseReads {
		t.Fatalf("reads = %d, want at least diversity prefix", len(transcript.Reads))
	}
	if transcript.Reads[0].Path != "b.go" {
		t.Fatalf("first read = %+v, want multi-term implementation", transcript.Reads[0])
	}
	seen := map[string]bool{}
	for _, read := range transcript.Reads[:grepReadV2DiverseReads] {
		if seen[read.Path] {
			t.Fatalf("first %d reads are not file-diverse: %+v", grepReadV2DiverseReads, transcript.Reads)
		}
		seen[read.Path] = true
	}
	if len(transcript.Patterns) >= len(grepReadPatterns(transcript.Query)) {
		t.Fatalf("informative patterns were not narrowed: %v", transcript.Patterns)
	}
}

func TestGrepReadV2_ExactPathReadsNamedFile(t *testing.T) {
	repository := fstest.MapFS{
		"doc/man_docs.go": {Data: []byte("package doc\n\nfunc GenManTree() {}\n")},
		"man_docs.go":     {Data: []byte("package other\n")},
	}
	transcript := GrepReadV2(repository, "doc/man_docs.go")
	if transcript.Mode != GrepReadV2ExactPath || len(transcript.Reads) != 2 {
		t.Fatalf("mode/reads = %q/%d", transcript.Mode, len(transcript.Reads))
	}
	if transcript.Reads[0].Path != "doc/man_docs.go" || transcript.Reads[0].StartLine != 1 {
		t.Fatalf("first read = %+v", transcript.Reads[0])
	}
}

func TestGrepReadV2_QueryPlanUsesCodeMorphologyWithoutRepositoryJudgements(t *testing.T) {
	mode, patterns := grepReadV2QueryPlan("how to check what command was chosen for the given arguments")
	if mode != GrepReadV2NaturalLanguage {
		t.Fatalf("mode = %q", mode)
	}
	for _, want := range []string{"check", "command", "chosen", "arg", "called"} {
		if !slices.Contains(patterns, want) {
			t.Fatalf("patterns %v do not contain %q", patterns, want)
		}
	}
}

func TestGrepReadV2_ProcedurePrefersProductionDeclarationOverTestName(t *testing.T) {
	repository := fstest.MapFS{
		"command.go":      {Data: []byte("package sample\n\n// SetArgs sets arguments and is useful when testing.\nfunc (c *Command) SetArgs(args []string) {}\n")},
		"command_test.go": {Data: []byte("package sample\n\nfunc TestFlagSet(t *testing.T) {}\n")},
	}
	transcript := GrepReadV2(repository, "How to set flags in test?")
	if len(transcript.Reads) == 0 || transcript.Reads[0].Path != "command.go" || transcript.Reads[0].StartLine != 3 {
		t.Fatalf("first read = %+v, want production SetArgs declaration", transcript.Reads)
	}
}

func TestGrepReadV2_PreservesActualFortyLineResponse(t *testing.T) {
	var source strings.Builder
	for i := 1; i <= 60; i++ {
		if i == 10 {
			source.WriteString("func Needle() {}\n")
		} else {
			source.WriteString("// filler\n")
		}
	}
	repository := fstest.MapFS{"answer.go": {Data: []byte(source.String())}}
	transcript := GrepReadV2(repository, "Needle")
	if err := transcript.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(transcript.Reads) != 1 || transcript.Reads[0].StartLine != 10 || transcript.Reads[0].EndLine != 49 {
		t.Fatalf("read provenance = %+v", transcript.Reads)
	}
	payload := transcript.Ledger.Responses[1]
	if got := strings.Count(string(payload.Bytes), "\n"); got != GrepReadWindowLines {
		t.Fatalf("actual payload lines = %d, want %d", got, GrepReadWindowLines)
	}
	if payload.ByteCount != len(payload.Bytes) || payload.SHA256 != SHA256Hex(payload.Bytes) {
		t.Fatal("payload metadata was not derived from exact response bytes")
	}
}

func TestGrepReadV2_LongDeclarationReadsDocAndBodyInBoundedChunks(t *testing.T) {
	var source strings.Builder
	source.WriteString("package sample\n\n// LongOperation explains the operation.\nfunc LongOperation() {\n")
	for range 80 {
		source.WriteString("\t// body\n")
	}
	source.WriteString("}\n")
	transcript := GrepReadV2(fstest.MapFS{"long.go": {Data: []byte(source.String())}}, "LongOperation")
	if len(transcript.Reads) != 3 {
		t.Fatalf("reads = %+v, want three 40-line chunks", transcript.Reads)
	}
	for i, read := range transcript.Reads {
		wantStart := 3 + i*GrepReadWindowLines
		if read.Path != "long.go" || read.StartLine != wantStart || read.EndLine-read.StartLine+1 > GrepReadWindowLines {
			t.Fatalf("read %d = %+v", i+1, read)
		}
	}
	if err := transcript.Validate(); err != nil {
		t.Fatal(err)
	}
	transcript.Reads[0].EndLine = transcript.Reads[0].StartLine + GrepReadWindowLines
	if err := transcript.Validate(); err == nil {
		t.Fatal("validation accepted a read wider than 40 lines")
	}
}
