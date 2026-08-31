package embed

// SW-261 review round 2 (MAJOR 6): the previous revision's encoding
// joined fields on '\n' and the decoder split on '\n'. A value
// containing a newline character therefore could not round-trip —
// the decoder split the encoded string at the embedded newline and
// silently corrupted the typed view. The fix introduces a real
// length-prefixed decoder: each field's declared length is consumed
// exactly, so embedded '|' and '\n' round-trip correctly. These
// tests pin the round-trip for both characters and the previous
// collision case.

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip_AllValues(t *testing.T) {
	cases := []string{
		"plain",
		"with|pipe",
		"with\nnewline",
		"with|pipe\nand newline",
		"",
		"\n",      // only newline
		"|",       // only pipe
		"\n|",     // newline then pipe
		"a\nb\nc", // multiple newlines
		"a|b|c",   // multiple pipes
		strings.Repeat("x", 1024),
	}
	for _, v := range cases {
		parts := []string{v, v, v, v, v, v, v, v} // 8 fields, mimicking Fingerprint's shape
		canonical := encodeCanonical(parts)
		got := decodeCanonical(canonical)
		if len(got) != len(parts) {
			t.Fatalf("decodeCanonical length = %d, want %d for value %q", len(got), len(parts), v)
		}
		for i := range parts {
			if got[i] != parts[i] {
				t.Fatalf("decodeCanonical[%d] = %q, want %q for input %q", i, got[i], parts[i], v)
			}
		}
	}
}

// TestFingerprint_CanonicalIsInjective: two distinct inputs must
// produce distinct canonical strings (AC-2). The previous collision
// case ("a|b","c" vs "a","b|c") is included as a regression
// pin.
func TestFingerprint_CanonicalIsInjective(t *testing.T) {
	cases := [][]string{
		{"a|b", "c"},
		{"a", "b|c"},
		{"plain", "other"},
		{"with|pipe", "no_pipe"},
		{"no_pipe", "with|pipe"},
	}
	for i, a := range cases {
		canonA := encodeCanonical(a)
		for j, b := range cases {
			if i == j {
				continue
			}
			canonB := encodeCanonical(b)
			if slicesEqual(a, b) {
				continue
			}
			if canonA == canonB {
				t.Fatalf("collision: %v and %v both produce %q", a, b, canonA)
			}
		}
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFingerprint_NewlineRoundTrip: a fingerprint whose ModelID
// contains an embedded newline (the regression case the previous
// encoding silently corrupted) round-trips through Canonical and
// decodeCanonical. The canonical ID is sha256-based so it doesn't
// carry the newline, but the canonical string itself must decode
// cleanly.
func TestFingerprint_NewlineRoundTrip(t *testing.T) {
	fp := Fingerprint{
		ModelID:         "with\nnewline",
		Revision:        "rev|1",
		ModelSHA256:     "abc",
		TokenizerSHA256: "def",
		Dim:             4,
		DocumentSchema:  "v2",
		ChunkerConfig:   "config|with|pipe",
		GraphGeneration: "gen\nwith\nnewlines",
	}
	canon := fp.Canonical()
	parts := decodeCanonical(canon)
	if len(parts) != 8 {
		t.Fatalf("decodeCanonical returned %d fields, want 8; canonical was %q", len(parts), canon)
	}
	want := []string{
		fp.ModelID, fp.Revision,
		strings.ToLower(strings.TrimSpace(fp.ModelSHA256)),
		strings.ToLower(strings.TrimSpace(fp.TokenizerSHA256)),
		"4", fp.DocumentSchema, fp.ChunkerConfig, fp.GraphGeneration,
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("parts[%d] = %q, want %q", i, parts[i], want[i])
		}
	}
}
