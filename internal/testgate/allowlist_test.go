package testgate

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/release"
)

// TestAllowlist_ExactlyTwo_NoWildcard asserts the carve-out is exactly two
// fully-qualified tests, no wildcard, in internal/mcpconfig (SW-055 AC#3/AC#7).
func TestAllowlist_ExactlyTwo_NoWildcard(t *testing.T) {
	root := ExpectedFailures(0) // under root: the carve-out is active
	if len(root) != 2 {
		t.Fatalf("expected exactly 2 allowlisted carve-out tests, got %d", len(root))
	}
	wantTests := map[string]bool{
		"TestFixture_Unwritable_FailsAndLeavesOriginalIntact": false,
		"TestBackupFailureAbortsBeforeTouchingConfig":         false,
	}
	for _, e := range root {
		if e.Package != "github.com/samibel/graphi/internal/mcpconfig" {
			t.Errorf("carve-out test in unexpected package: %s", e.Package)
		}
		if strings.ContainsAny(e.Test, "*?") {
			t.Errorf("carve-out must be exact, not a wildcard: %q", e.Test)
		}
		if _, ok := wantTests[e.Test]; !ok {
			t.Errorf("unexpected carve-out test: %q", e.Test)
		} else {
			wantTests[e.Test] = true
		}
	}
	for name, seen := range wantTests {
		if !seen {
			t.Errorf("missing expected carve-out test: %q", name)
		}
	}
}

// TestAllowlist_PrivilegeConditional asserts the carve-out is EMPTY for a non-root
// euid (the two tests are expected to PASS as a normal user) and active under root.
func TestAllowlist_PrivilegeConditional(t *testing.T) {
	if got := ExpectedFailures(1000); len(got) != 0 {
		t.Fatalf("non-root carve-out must be empty (tests expected to pass), got %v", got)
	}
	if got := ExpectedFailures(0); len(got) != 2 {
		t.Fatalf("root carve-out must be the two tests, got %d", len(got))
	}
}

// TestEvaluate_GreenWhenOnlyAllowlistedFail_Root proves a run is GREEN when only
// the two allowlisted tests fail under root.
func TestEvaluate_GreenWhenOnlyAllowlistedFail_Root(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"pass","Package":"github.com/samibel/graphi/core/parse","Test":"TestX"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/release","Test":"TestY"}`,
	}, "\n")
	res, err := Evaluate(strings.NewReader(stream), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Green {
		t.Fatalf("expected GREEN, got: %s", FormatVerdict(res, 0))
	}
	if len(res.MatchedExpected) != 2 {
		t.Fatalf("expected both allowlisted tests matched, got %v", res.MatchedExpected)
	}
}

// TestEvaluate_RegressionCannotHide proves a third failing test (a regression) is
// surfaced even though the two allowlisted carve-outs also failed — it cannot hide.
func TestEvaluate_RegressionCannotHide(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
		`{"Action":"fail","Package":"github.com/samibel/graphi/engine/ingest","Test":"TestSomethingRegressed"}`,
	}, "\n")
	res, err := Evaluate(strings.NewReader(stream), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Green {
		t.Fatal("a regression must NOT be GREEN")
	}
	if len(res.UnexpectedFails) != 1 || !strings.Contains(res.UnexpectedFails[0], "TestSomethingRegressed") {
		t.Fatalf("regression must be named as an unexpected failure, got %v", res.UnexpectedFails)
	}
}

// TestEvaluate_AllowlistedStartedPassing_NotGreen proves a stale carve-out cannot
// mask a now-passing test: if an allowlisted test does NOT fail under root, the
// gate is not green and names it (fail loudly).
func TestEvaluate_AllowlistedStartedPassing_NotGreen(t *testing.T) {
	stream := strings.Join([]string{
		// Only ONE of the two allowlisted tests failed; the other "started passing".
		`{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
	}, "\n")
	res, err := Evaluate(strings.NewReader(stream), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Green {
		t.Fatal("a stale carve-out (allowlisted test now passing) must NOT be GREEN")
	}
	if len(res.MissingExpected) != 1 || !strings.Contains(res.MissingExpected[0], "TestBackupFailureAbortsBeforeTouchingConfig") {
		t.Fatalf("expected the now-passing carve-out named as missing, got %v", res.MissingExpected)
	}
}

// TestEvaluate_NonRoot_NoCarveOut proves that as a normal user, the two mcpconfig
// tests are expected to PASS — so if they pass, the run is green, and if one FAILS
// it is treated as a real regression (no carve-out applies off-root).
func TestEvaluate_NonRoot_NoCarveOut(t *testing.T) {
	// Non-root, both pass → green.
	pass := strings.Join([]string{
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestBackupFailureAbortsBeforeTouchingConfig"}`,
	}, "\n")
	res, _ := Evaluate(strings.NewReader(pass), 1000)
	if !res.Green {
		t.Fatalf("non-root all-pass must be GREEN, got %s", FormatVerdict(res, 1000))
	}
	// Non-root, one of them FAILS → NOT green (it's a regression off-root).
	fail := `{"Action":"fail","Package":"github.com/samibel/graphi/internal/mcpconfig","Test":"TestFixture_Unwritable_FailsAndLeavesOriginalIntact"}`
	res2, _ := Evaluate(strings.NewReader(fail), 1000)
	if res2.Green {
		t.Fatal("non-root: a carve-out test failing is a real regression, must NOT be GREEN")
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
	// Languages registered by the default tier, mapped to their subset-tag suffix.
	// The suffix differs from the canonical language id only for c_sharp (tag
	// grammar_subset_c_sharp) — which already matches — so the mapping is identity.
	registered := parse.RegisterDefaults(parse.NewRegistry()).Languages()

	// stdlib parsers carry no gotreesitter blob → no subset tag.
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
			continue // umbrella tag, not a per-language tag
		}
		haveTags[tg] = struct{}{}
	}

	// Every registered (non-stdlib) language must have its tag.
	for tg := range wantTags {
		if _, ok := haveTags[tg]; !ok {
			t.Errorf("drift: registered language tag %q missing from DefaultGrammarSubsetTags", tg)
		}
	}
	// Every per-language tag must correspond to a registered language.
	for tg := range haveTags {
		if _, ok := wantTags[tg]; !ok {
			t.Errorf("drift: subset tag %q has no corresponding registered default-tier language", tg)
		}
	}
}
