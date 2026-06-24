package parse

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// sentinelSecret is a recognizable token planted in source for the source-leak
// negative tests. If it ever appears in an error/log string, sanitization failed.
const sentinelSecret = "SUPER_SECRET_API_KEY_a1b2c3d4e5f6_DO_NOT_LEAK"

// TestSanitizedError_NeverEchoesRawSource is the default-deny source-sanitization
// negative test (SW-055 AC#6): a SanitizedError carries ONLY structured provenance
// (file/lang/byte-span/node-kind) and NEVER the raw source bytes, even when the
// cause's provenance describes a span that contained the secret.
func TestSanitizedError_NeverEchoesRawSource(t *testing.T) {
	for _, sentinel := range []error{ErrFileTooLarge, ErrParseTimeout, ErrMaxDepthExceeded, errors.New("grammar: unexpected token")} {
		err := SanitizedError(Provenance{
			File:      "shop/secrets.py",
			Language:  "python",
			ByteStart: 10,
			ByteEnd:   55,
			NodeKind:  "assignment",
		}, sentinel)
		msg := err.Error()
		if strings.Contains(msg, sentinelSecret) {
			t.Fatalf("sanitized error leaked raw source secret: %q", msg)
		}
		// Provenance MUST be present.
		for _, want := range []string{"shop/secrets.py", "python", "assignment"} {
			if !strings.Contains(msg, want) {
				t.Errorf("sanitized error missing provenance %q in %q", want, msg)
			}
		}
		// errors.Is must still see through to the wrapped sentinel.
		if !errors.Is(err, sentinel) {
			t.Errorf("SanitizedError broke errors.Is for %v", sentinel)
		}
	}
}

// TestGuardCSTDepth_FailsClosedOnDeepNesting builds a deeply-nested (billion-laughs
// style) source for several gotreesitter languages and asserts the parse fails
// closed with ErrMaxDepthExceeded — and that the resulting error never echoes the
// (sentinel-bearing) raw source.
func TestParse_FailsClosedOnDeepNesting(t *testing.T) {
	prev := SetMaxParseDepth(40) // tight bound for the test
	defer SetMaxParseDepth(prev)

	reg := RegisterDefaults(NewRegistry())
	cases := []struct {
		file string
		src  string
	}{
		// Deeply nested parentheses in a Python expression; secret embedded.
		{"deep.py", "x = " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + "  # " + sentinelSecret + "\n"},
		// Deeply nested JS array literal.
		{"deep.js", "var z = " + strings.Repeat("[", 300) + "1" + strings.Repeat("]", 300) + "; // " + sentinelSecret + "\n"},
	}
	for _, c := range cases {
		_, err := reg.Parse(context.Background(), c.file, []byte(c.src))
		if err == nil {
			t.Fatalf("%s: expected fail-closed depth error, got nil", c.file)
		}
		if !errors.Is(err, ErrMaxDepthExceeded) {
			t.Fatalf("%s: expected ErrMaxDepthExceeded, got %v", c.file, err)
		}
		if strings.Contains(err.Error(), sentinelSecret) {
			t.Fatalf("%s: depth error leaked raw source: %q", c.file, err.Error())
		}
	}
}

// TestParse_WithinDepthBound_Succeeds proves the depth guard is non-vacuous: an
// ordinary (shallow) file parses fine under the same tight bound that rejects the
// deep one.
func TestParse_WithinDepthBound_Succeeds(t *testing.T) {
	prev := SetMaxParseDepth(40)
	defer SetMaxParseDepth(prev)

	reg := RegisterDefaults(NewRegistry())
	if _, err := reg.Parse(context.Background(), "ok.py", []byte("def f():\n    return 1\n")); err != nil {
		t.Fatalf("shallow python file should parse under depth bound: %v", err)
	}
}

func TestRuntime_IsPureGo(t *testing.T) {
	for _, rt := range []Runtime{RuntimeGoAST, RuntimeStdlib, RuntimeGoTreeSitter} {
		if !rt.IsPureGo() {
			t.Errorf("%q should be pure-Go", rt)
		}
	}
	if RuntimeCGOForest.IsPureGo() {
		t.Error("go-sitter-forest CGO runtime must NOT be pure-Go")
	}
	if Runtime("unknown-future-runtime").IsPureGo() {
		t.Error("unknown runtime must default-deny (not pure-Go)")
	}
}

func TestSetMaxParseDepth_RoundTrip(t *testing.T) {
	orig := SetMaxParseDepth(123)
	if got := SetMaxParseDepth(orig); got != 123 {
		t.Fatalf("SetMaxParseDepth round-trip: got %d, want 123", got)
	}
	// Restore default for other tests in the package.
	SetMaxParseDepth(DefaultResourceBounds().MaxDepth)
}
