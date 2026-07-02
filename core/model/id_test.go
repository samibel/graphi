package model

import (
	"regexp"
	"testing"

	"github.com/cespare/xxhash/v2"
)

var hexID16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

// TestXXHashCanonical pins that the underlying digest is canonical xxhash64
// (seed 0). ef46db3751d8e999 is the official empty-input vector; if this ever
// changes, every persisted ID would silently drift.
func TestXXHashCanonical(t *testing.T) {
	if got := FormatID(xxhash.Sum64([]byte(""))); got != "ef46db3751d8e999" {
		t.Fatalf("xxhash64('') = %s, want ef46db3751d8e999", got)
	}
}

func TestFormatIDFixedWidth(t *testing.T) {
	cases := []uint64{0, 1, 0xff, 0xffffffffffffffff, 0x123}
	for _, c := range cases {
		s := FormatID(c)
		if !hexID16.MatchString(s) {
			t.Errorf("FormatID(%d) = %q, not 16-char lowercase hex", c, s)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"pkg/foo.go":             "pkg/foo.go",
		"/abs/pkg/foo.go":        "abs/pkg/foo.go",
		"./pkg/foo.go":           "pkg/foo.go",
		"pkg/../pkg/foo.go":      "pkg/foo.go",
		"../../escape/foo.go":    "escape/foo.go", // .. traversal removed
		"a/b/../c/foo.go":        "a/c/foo.go",
		`C:\Users\x\repo\foo.go`: "Users/x/repo/foo.go",
		`pkg\sub\foo.go`:         "pkg/sub/foo.go",
		"/":                      "",
		".":                      "",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizePathNoEscape ensures no normalized path ever escapes the repo
// root via leading "..".
func TestNormalizePathNoEscape(t *testing.T) {
	for _, in := range []string{"../x", "../../x", "a/../../x", "/../x"} {
		got := NormalizePath(in)
		if len(got) >= 2 && got[:2] == ".." {
			t.Errorf("NormalizePath(%q) = %q escapes repo root", in, got)
		}
	}
}
