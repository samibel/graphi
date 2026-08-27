package extpack

import (
	"strings"
	"testing"
)

// SW-230 round 1 — the scaffold's renderer is hand-rolled instead of
// text/template, because text/template cost 3,322,416 bytes of shipped binary
// (see renderScaffold's comment). These tests pin the two properties that make
// that trade safe: it renders what the templates need, and it FAILS LOUDLY on
// everything it does not implement. A silent renderer is how a hand-rolled
// substituter turns into a bug that ships in the first file every pack author
// reads.

// TestSW230_RenderScaffoldSubstitutesFields is the control: without it the
// failure cases below could all be passing vacuously against a renderer that
// rejects everything.
func TestSW230_RenderScaffoldSubstitutesFields(t *testing.T) {
	got, err := renderScaffold("t", "id: {{.ID}}\nkind: {{.Kind}}\nid again: {{.ID}}\n",
		map[string]string{"ID": "example.arch-rules", "Kind": "architecture-rules"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	const want = "id: example.arch-rules\nkind: architecture-rules\nid again: example.arch-rules\n"
	if string(got) != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}

// TestSW230_RenderScaffoldRendersATemplateWithNoActions: a template that is all
// literal text is passed through unchanged, byte for byte.
func TestSW230_RenderScaffoldRendersATemplateWithNoActions(t *testing.T) {
	const text = "version: \"1\"\nrules: []\n"
	got, err := renderScaffold("t", text, map[string]string{"Unused": "x"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(got) != text {
		t.Fatalf("rendered %q, want %q", got, text)
	}
}

// TestSW230_RenderScaffoldRefusesWhatItDoesNotImplement is the fail-closed half.
//
// Every case here is something text/template would have accepted or papered
// over — an unknown field renders "<no value>", a range action renders a list.
// This renderer implements field substitution ONLY, so each of these must be an
// ERROR rather than a scaffold that is quietly wrong.
func TestSW230_RenderScaffoldRefusesWhatItDoesNotImplement(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"unknown field", "id: {{.Nope}}\n", "no value for {{.Nope}}"},
		{"range action", "{{range .Provides}}x{{end}}", "unsupported action"},
		{"bare dot", "{{.}}", "unsupported action"},
		{"pipeline", "{{.ID | printf \"%q\"}}", "no value for {{.ID | printf \"%q\"}}"},
		{"unterminated action", "id: {{.ID\n", "unterminated {{ action"},
	}
	data := map[string]string{"ID": "example.arch-rules"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderScaffold("t", tc.text, data)
			if err == nil {
				t.Fatalf("rendered %q with no error; want a refusal mentioning %q", got, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if got != nil {
				t.Fatalf("a refused render returned %q; a caller must not be handed partial output", got)
			}
		})
	}
}

// TestSW230_EveryScaffoldTemplateRenders walks the real compiled-in templates
// through the real Scaffold path for every kind. It is the guard that matters
// when someone edits a template: renderScaffold's strictness is only protective
// if a template using an unimplemented construct is caught by a test rather than
// by an author running `graphi extension init`.
func TestSW230_EveryScaffoldTemplateRenders(t *testing.T) {
	for _, kind := range ScaffoldKinds() {
		files, err := Scaffold(ScaffoldOptions{Kind: kind})
		if err != nil {
			t.Fatalf("scaffold %s: %v", kind, err)
		}
		for _, f := range files {
			if len(f.Data) == 0 {
				t.Errorf("%s/%s rendered empty", kind, f.Name)
			}
			if strings.Contains(string(f.Data), "{{") {
				t.Errorf("%s/%s still contains an unrendered action", kind, f.Name)
			}
		}
	}
}
