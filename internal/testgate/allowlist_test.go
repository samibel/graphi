package testgate

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/release"
)

func TestEvaluate_AllPassAndPortableSkipsAreGreen(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/mcpconfig"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/mcpconfig"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(stream), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Green {
		t.Fatalf("pass/skip-only suite must be GREEN, got: %s", FormatVerdict(res))
	}
	if got := FormatVerdict(res); !strings.Contains(got, "contains no failures") {
		t.Fatalf("green verdict is ambiguous: %q", got)
	}
}

// The two permission tests used to be accepted as expected failures under
// root. This regression guard proves that the gate now treats either one like
// every other failure, with no UID-dependent path.
func TestEvaluate_FormerPermissionCarveOutIsFailure(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/mcpconfig"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(stream), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green {
		t.Fatal("permission-test failure must never be accepted")
	}
	if len(res.UnexpectedFails) != 1 || !strings.Contains(res.UnexpectedFails[0], "TestFixture_Unwritable") {
		t.Fatalf("permission-test failure not reported exactly: %v", res.UnexpectedFails)
	}
}

func TestEvaluate_PackageLevelFailWithoutNamedTest_NotGreen(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/broken"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/broken","Output":"FAIL setup failed\n"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/broken"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(stream), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green {
		t.Fatal("an unstructured package failure must NOT be GREEN")
	}
	if len(res.UnexpectedFails) != 1 || !strings.Contains(res.UnexpectedFails[0], "package-level failure") {
		t.Fatalf("package failure must be reported explicitly, got %v", res.UnexpectedFails)
	}
}

func TestEvaluate_BuildFail_NotGreen(t *testing.T) {
	stream := strings.Join([]string{
		`{"ImportPath":"github.com/samibel/graphi/broken","Action":"build-output","Output":"undefined: nope\n"}`,
		`{"ImportPath":"github.com/samibel/graphi/broken","Action":"build-fail"}`,
		`{"Action":"start","Package":"github.com/samibel/graphi/broken"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/broken","FailedBuild":"github.com/samibel/graphi/broken"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(stream), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green {
		t.Fatal("a compile/build failure must NOT be GREEN")
	}
	if got := strings.Join(res.UnexpectedFails, "\n"); !strings.Contains(got, "build failure") {
		t.Fatalf("build failure must be classified, got %v", res.UnexpectedFails)
	}
}

func TestEvaluate_EmptyAndInvalidStreamsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream string
		want   string
	}{
		{name: "empty", stream: "\n\t\n", want: "empty"},
		{name: "invalid json", stream: `{"Action":"pass"`, want: "invalid"},
		{name: "non json", stream: "go test exploded", want: "invalid"},
		{name: "missing action", stream: `{}`, want: "missing Action"},
		{name: "build prelude only", stream: `{"ImportPath":"github.com/samibel/graphi/example","Action":"build-output"}`, want: "truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Evaluate(strings.NewReader(tc.stream)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Evaluate() error = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestEvaluate_SemanticallyTruncatedStreamFailsClosed(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/example","Test":"TestStillRunning"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/example","Test":"TestStillRunning","Output":"=== RUN TestStillRunning\n"}`,
	}, "\n")
	if _, err := Evaluate(strings.NewReader(stream)); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("Evaluate() error = %v, want truncated-stream error", err)
	}
}

func TestEvaluateWithProducer_FailsOnExitAndStderrInconsistency(t *testing.T) {
	passStream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/example"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(passStream), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green || len(res.ProducerFailures) == 0 || !strings.Contains(res.ProducerFailures[0], "exited 1") {
		t.Fatalf("non-zero producer without structured failure must fail closed, got %+v", res)
	}

	res, err = EvaluateWithProducer(strings.NewReader(passStream), ProducerStatus{ExitCode: 0, Stderr: "toolchain failed out of band"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green || len(res.ProducerFailures) == 0 || !strings.Contains(res.ProducerFailures[0], "stderr") {
		t.Fatalf("producer stderr must fail closed, got %+v", res)
	}

	failStream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/example","Test":"TestBroken"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/example","Test":"TestBroken"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/example"}`,
	}, "\n")
	res, err = EvaluateWithProducer(strings.NewReader(failStream), ProducerStatus{ExitCode: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Green || len(res.ProducerFailures) == 0 || !strings.Contains(strings.Join(res.ProducerFailures, " "), "unsupported status 2") {
		t.Fatalf("unsupported producer exit status must fail closed, got %+v", res)
	}
}

func TestEvaluateWithProducer_InvalidStreamStillReportsProducerStatus(t *testing.T) {
	_, err := EvaluateWithProducer(strings.NewReader(""), ProducerStatus{ExitCode: 2, Stderr: "toolchain unavailable"})
	if err == nil {
		t.Fatal("empty stream with failed producer must return an error")
	}
	got := err.Error()
	if !strings.Contains(got, "empty") || !strings.Contains(got, "exit 2") || !strings.Contains(got, "toolchain unavailable") {
		t.Fatalf("error must retain stream, exit, and stderr diagnostics, got %q", got)
	}
}

// TestSubsetTags_RegisterDefaults_DriftGuard locks DefaultGrammarSubsetTags ↔
// RegisterDefaults in lock-step (SW-055 Slice 5): every gotreesitter language
// registered in the default tier has exactly one grammar_subset_<lang> tag and
// vice-versa. The two stdlib parsers (go via go/ast, json via encoding/json) carry
// no grammar blob and therefore NO tag; HTML is absent from both. A drift in either
// direction (a registered language missing its tag, or a tag with no registered
// language) fails here.
func TestSubsetTags_RegisterDefaults_DriftGuard(t *testing.T) {
	registered := parse.RegisterDefaults(parse.NewRegistry()).Languages()
	stdlibNoBlob := map[string]struct{}{"go": {}, "json": {}}

	wantTags := map[string]struct{}{}
	for _, lang := range registered {
		if _, ok := stdlibNoBlob[lang]; ok {
			continue
		}
		wantTags["grammar_subset_"+lang] = struct{}{}
	}

	haveTags := map[string]struct{}{}
	for _, tg := range release.DefaultGrammarSubsetTags {
		if tg == "grammar_subset" {
			continue
		}
		haveTags[tg] = struct{}{}
	}

	for tg := range wantTags {
		if _, ok := haveTags[tg]; !ok {
			t.Errorf("drift: registered language tag %q missing from DefaultGrammarSubsetTags", tg)
		}
	}
	for tg := range haveTags {
		if _, ok := wantTags[tg]; !ok {
			t.Errorf("drift: subset tag %q has no corresponding registered default-tier language", tg)
		}
	}
}
