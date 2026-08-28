package testgate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/release"
)

// The GREEN half of this contract predates SW-249 and is unchanged: a suite of
// nothing but passes and skips is GREEN. SW-249 only adds the second half — the
// same GREEN verdict must now also name what it skipped, so that a skip stops
// being indistinguishable from a pass.
func TestEvaluate_AllPassAndPortableSkipsAreGreen(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/mcpconfig"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact","Output":"=== RUN   TestFixture_Unwritable_FailsAndLeavesOriginalIntact\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact","Output":"    fixture_test.go:41: filesystem does not enforce mode bits; denial cannot be exercised\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact","Output":"--- SKIP: TestFixture_Unwritable_FailsAndLeavesOriginalIntact (0.00s)\n"}`,
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
	if len(res.UnexpectedFails) != 0 || len(res.ProducerFailures) != 0 {
		t.Fatalf("a skip must never become a failure, got %+v", res)
	}

	// SW-249: the same GREEN verdict must now say what it did not measure.
	want := []SkippedTest{{
		Package: "github.com/samibel/graphi/internal/mcpconfig",
		Test:    "TestFixture_Unwritable_FailsAndLeavesOriginalIntact",
		Reason:  "fixture_test.go:41: filesystem does not enforce mode bits; denial cannot be exercised",
	}}
	if !reflect.DeepEqual(res.SkippedTests, want) {
		t.Fatalf("SkippedTests = %+v, want %+v", res.SkippedTests, want)
	}
	verdict := FormatVerdict(res)
	for _, fragment := range []string{
		"skipped tests: 1",
		"TestFixture_Unwritable_FailsAndLeavesOriginalIntact",
		"denial cannot be exercised",
	} {
		if !strings.Contains(verdict, fragment) {
			t.Fatalf("green verdict does not name its skips (missing %q):\n%s", fragment, verdict)
		}
	}
	if strings.Contains(verdict, "TestBackupFailureAbortsBeforeTouchingConfig") {
		t.Fatalf("a passing test must not be listed as skipped:\n%s", verdict)
	}
}

// AC-4: a package that skipped as a whole and a package whose individual tests
// skipped are different observations and must not render the same way.
func TestEvaluate_WholePackageSkipIsDistinguishableFromTestSkips(t *testing.T) {
	individualSkips := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/example"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/example","Test":"TestNeedsKotlinc"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestNeedsKotlinc","Output":"=== RUN   TestNeedsKotlinc\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestNeedsKotlinc","Output":"    example_test.go:12: kotlinc unavailable\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestNeedsKotlinc","Output":"--- SKIP: TestNeedsKotlinc (0.00s)\n"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/example","Test":"TestNeedsKotlinc"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/example"}`,
	}, "\n")
	wholePackageSkip := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/example"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Output":"?   \tgithub.com/samibel/graphi/internal/example\t[no test files]\n"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/example"}`,
	}, "\n")

	tests, err := EvaluateWithProducer(strings.NewReader(individualSkips), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := EvaluateWithProducer(strings.NewReader(wholePackageSkip), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !tests.Green || !pkg.Green {
		t.Fatalf("neither shape may change the verdict: tests.Green=%v pkg.Green=%v", tests.Green, pkg.Green)
	}

	wantTests := []SkippedTest{{
		Package: "github.com/samibel/graphi/internal/example",
		Test:    "TestNeedsKotlinc",
		Reason:  "example_test.go:12: kotlinc unavailable",
	}}
	if !reflect.DeepEqual(tests.SkippedTests, wantTests) || len(tests.SkippedPackages) != 0 {
		t.Fatalf("individual test skips misclassified: %+v", tests)
	}
	wantPackages := []SkippedPackage{{
		Package: "github.com/samibel/graphi/internal/example",
		Reason:  "no test files",
	}}
	if !reflect.DeepEqual(pkg.SkippedPackages, wantPackages) || len(pkg.SkippedTests) != 0 {
		t.Fatalf("whole-package skip misclassified: %+v", pkg)
	}

	testsVerdict, pkgVerdict := FormatVerdict(tests), FormatVerdict(pkg)
	if testsVerdict == pkgVerdict {
		t.Fatalf("the two skip shapes render identically:\n%s", testsVerdict)
	}
	if !strings.Contains(testsVerdict, "skipped tests: 1") || strings.Contains(testsVerdict, "skipped packages") {
		t.Fatalf("individual test skips rendered as a package skip:\n%s", testsVerdict)
	}
	if !strings.Contains(pkgVerdict, "skipped packages: 1") || strings.Contains(pkgVerdict, "skipped tests") {
		t.Fatalf("whole-package skip rendered as test skips:\n%s", pkgVerdict)
	}
	if !strings.Contains(pkgVerdict, "no test files") {
		t.Fatalf("whole-package skip lost its reason:\n%s", pkgVerdict)
	}
}

// AC-3: a consumer reads the skip summary from the result, never from the prose.
func TestEvaluate_SkipSummaryIsMachineReadable(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/example"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/example","Test":"TestSilentSkip"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestSilentSkip","Output":"=== RUN   TestSilentSkip\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestSilentSkip","Output":"--- SKIP: TestSilentSkip (0.00s)\n"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/example","Test":"TestSilentSkip"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/example","Test":"TestExplainedSkip"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestExplainedSkip","Output":"=== RUN   TestExplainedSkip\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestExplainedSkip","Output":"    example_test.go:40: runner too degraded to measure\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestExplainedSkip","Output":"--- SKIP: TestExplainedSkip (0.00s)\n"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/example","Test":"TestExplainedSkip"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/example"}`,
	}, "\n")
	res, err := EvaluateWithProducer(strings.NewReader(stream), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Green {
		t.Fatalf("skips must not change the verdict: %s", FormatVerdict(res))
	}
	// Sorted, so a consumer gets a stable order across runs.
	want := []SkippedTest{
		{Package: "github.com/samibel/graphi/internal/example", Test: "TestExplainedSkip", Reason: "example_test.go:40: runner too degraded to measure"},
		{Package: "github.com/samibel/graphi/internal/example", Test: "TestSilentSkip"},
	}
	if !reflect.DeepEqual(res.SkippedTests, want) {
		t.Fatalf("SkippedTests = %+v, want %+v", res.SkippedTests, want)
	}

	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Green        bool `json:"green"`
		SkippedTests []struct {
			Package string `json:"package"`
			Test    string `json:"test"`
			Reason  string `json:"reason"`
		} `json:"skipped_tests"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Green || len(decoded.SkippedTests) != 2 {
		t.Fatalf("machine-readable summary lost the skips: %s", encoded)
	}
	if decoded.SkippedTests[0].Test != "TestExplainedSkip" || decoded.SkippedTests[0].Reason != "example_test.go:40: runner too degraded to measure" {
		t.Fatalf("machine-readable summary lost the reason: %s", encoded)
	}
	if decoded.SkippedTests[1].Test != "TestSilentSkip" || decoded.SkippedTests[1].Reason != "" {
		t.Fatalf("a reasonless skip must be reported as reasonless: %s", encoded)
	}
	// A missing reason must still be visible to a human, not silently blank.
	if !strings.Contains(FormatVerdict(res), "TestSilentSkip: (no reason in stream)") {
		t.Fatalf("reasonless skip is invisible in the verdict:\n%s", FormatVerdict(res))
	}
}

// AC-5: adding skips to a stream changes what is reported and nothing else.
// The verdict, the failure classification and the producer accounting are
// byte-identical with and without them.
func TestEvaluate_SkipsChangeReportingOnlyNotTheDecision(t *testing.T) {
	withoutSkips := []string{
		`{"Action":"start","Package":"github.com/samibel/graphi/internal/example"}`,
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/example","Test":"TestBroken"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/example","Test":"TestBroken"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/example"}`,
	}
	withSkips := []string{
		withoutSkips[0],
		`{"Action":"run","Package":"github.com/samibel/graphi/internal/example","Test":"TestSkipped"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestSkipped","Output":"    example_test.go:9: platform guard\n"}`,
		`{"Action":"output","Package":"github.com/samibel/graphi/internal/example","Test":"TestSkipped","Output":"--- SKIP: TestSkipped (0.00s)\n"}`,
		`{"Action":"skip","Package":"github.com/samibel/graphi/internal/example","Test":"TestSkipped"}`,
		withoutSkips[1], withoutSkips[2], withoutSkips[3],
	}

	bare, err := EvaluateWithProducer(strings.NewReader(strings.Join(withoutSkips, "\n")), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := EvaluateWithProducer(strings.NewReader(strings.Join(withSkips, "\n")), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bare.Green != skipped.Green || skipped.Green {
		t.Fatalf("skips changed the verdict: bare=%v skipped=%v", bare.Green, skipped.Green)
	}
	if !reflect.DeepEqual(bare.UnexpectedFails, skipped.UnexpectedFails) {
		t.Fatalf("skips changed what counts as a failure: %v vs %v", bare.UnexpectedFails, skipped.UnexpectedFails)
	}
	if !reflect.DeepEqual(bare.ProducerFailures, skipped.ProducerFailures) {
		t.Fatalf("skips changed producer accounting: %v vs %v", bare.ProducerFailures, skipped.ProducerFailures)
	}
	if len(bare.SkippedTests) != 0 {
		t.Fatalf("a stream with no skips must report none, got %+v", bare.SkippedTests)
	}
	if len(skipped.SkippedTests) != 1 || skipped.SkippedTests[0].Reason != "example_test.go:9: platform guard" {
		t.Fatalf("skip not summarised beside the failure: %+v", skipped.SkippedTests)
	}
	if !strings.Contains(FormatVerdict(skipped), "NOT GREEN") || !strings.Contains(FormatVerdict(skipped), "TestBroken") {
		t.Fatalf("failure reporting regressed:\n%s", FormatVerdict(skipped))
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
