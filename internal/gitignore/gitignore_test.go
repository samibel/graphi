package gitignore

import (
	"strings"
	"testing"
)

func TestMatch_GitSemanticsSubset(t *testing.T) {
	m, err := Compile([]string{
		"# comment",
		"",
		"*.log",
		"!keep.log",
		"build/",
		"/dist",
		"docs/*.tmp",
		"**/generated",
		"cache/**",
		"a/**/b",
		"[Tt]humbs.db",
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		// unanchored basename glob at any depth; negation wins as last match
		{"app.log", false, true},
		{"deep/nested/app.log", false, true},
		{"keep.log", false, false},
		{"deep/keep.log", false, false},
		// dir-only pattern: the dir and everything below, but not a same-named file
		{"build", true, true},
		{"build/out.bin", false, true},
		{"src/build", true, true},
		{"src/build/x.o", false, true},
		{"build", false, false},
		// anchored: only at the root
		{"dist", true, true},
		{"dist/x.js", false, true},
		{"sub/dist", true, false},
		// anchored glob with directory prefix
		{"docs/a.tmp", false, true},
		{"docs/sub/a.tmp", false, false},
		// leading **
		{"generated", true, true},
		{"x/y/generated", true, true},
		{"x/y/generated/file.go", false, true},
		// trailing /**
		{"cache/anything/below.txt", false, true},
		{"cache", true, false},
		// middle **
		{"a/b", false, true},
		{"a/x/y/b", false, true},
		{"a/x/c", false, false},
		// character class
		{"Thumbs.db", false, true},
		{"thumbs.db", false, true},
		{"crumbs.db", false, false},
	}
	for _, c := range cases {
		if got := m.Match(c.path, c.isDir); got != c.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

func TestMatch_NoReincludeBelowExcludedDir(t *testing.T) {
	m, err := Compile([]string{"vendor/", "!vendor/important.go"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !m.Match("vendor/important.go", false) {
		t.Fatal("negation below an excluded directory must not re-include (git rule)")
	}
}

func TestCompile_EmptyAndNil(t *testing.T) {
	if m, err := Compile(nil); err != nil || m != nil {
		t.Fatal("Compile(nil) should return nil matcher")
	}
	if m, err := Compile([]string{"", "# only comments"}); err != nil || m != nil {
		t.Fatal("comment-only input should return nil matcher")
	}
	var m *Matcher
	if m.Match("anything", false) {
		t.Fatal("nil matcher must ignore nothing")
	}
}

func TestCompile_InvalidPatternFailsClosedWithLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		line  string
	}{
		{name: "unterminated class", lines: []string{"# comment", "[secret"}, line: "line 2"},
		{name: "invalid range", lines: []string{"[z-a]"}, line: "line 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Compile(tc.lines)
			if err == nil {
				t.Fatal("malformed pattern must return an error")
			}
			if m != nil {
				t.Fatalf("malformed pattern returned a matcher: %#v", m)
			}
			if !strings.Contains(err.Error(), tc.line) {
				t.Fatalf("error %q does not identify %s", err, tc.line)
			}
		})
	}
}

func TestCompileErrorDoesNotEchoRawPattern(t *testing.T) {
	const secret = "SECRET_PATTERN_CONTENT"
	_, err := Compile([]string{"[" + secret})
	if err == nil {
		t.Fatal("Compile accepted malformed pattern")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Compile error leaked raw pattern: %q", err)
	}
}
