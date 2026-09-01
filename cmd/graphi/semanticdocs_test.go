package main

// SW-267 reviewer fix C4: the production fileDocumentSource must
// NOT launder admission errors into legitimate exclusions. The
// previous shape discarded the error from BuildDocuments with `_`,
// then classified every missing declaration as no_span (Excluded).
// The build would silently publish a partial generation as Ready.
//
// The test below exercises the regression path: a fileDocumentSource
// wired to an Admitter that fails on a specific input sees its
// nodes' Result return DocumentFailed, NOT DocumentExcluded. A
// DocumentExcluded classification would let GenerateAndPersist
// commit a partial generation; DocumentFailed aborts the build.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
)

// alwaysFailAdmitter is the test Admitter that fails every input.
// It reproduces the failure mode BuildDocuments surfaces — the
// production wiring must catch this and fail the build, not launder
// the resulting missing declaration as a legitimate no_span.
type alwaysFailAdmitter struct{ tokenLimit int }

func (a alwaysFailAdmitter) Admit(_ context.Context, _ string) (embed.Admitted, error) {
	return embed.Admitted{}, &embed.AdmissionError{Limit: a.tokenLimit, Actual: a.tokenLimit + 1}
}

// TestFileDocumentSource_AdmissionFailureIsFailedNotExcluded is
// the regression test for reviewer fix C4. It writes a Go file with
// a single declaration, wires a fileDocumentSource with an Admitter
// that fails every input, and asserts:
//   - The node's Result is DocumentFailed (NOT DocumentExcluded).
//   - The Document method returns false (no document was produced).
//   - curAdmit is set so subsequent Result calls for declarations
//     in the same file also see DocumentFailed.
//
// The test's failure mode is unambiguous: with the previous shape
// (discard BuildDocuments error, classify missing as Excluded), the
// build would silently publish a partial generation as Ready.
func TestFileDocumentSource_AdmissionFailureIsFailedNotExcluded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "input.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package pkg\n\nfunc Fails() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Result triggers load(), which parses the real file and calls
	// BuildDocuments with the failing admitter. If load discards the returned
	// error, this exact node is misclassified as a no_span exclusion.
	srcFS := newFileDocumentSource(context.Background(), root, failingEmbedder{
		adm: alwaysFailAdmitter{tokenLimit: 8},
	})
	n, _ := model.NewNode("function", "pkg.Fails", "pkg/input.go", 3, 6)
	if got := srcFS.Result(n); got != embed.DocumentFailed {
		t.Errorf("Result = %v, want DocumentFailed (C4: admission failure must not launder into no_span). Without the C4 fix the missing node id returns DocumentExcluded and the build commits a partial generation as Ready.", got)
	}
	if !srcFS.curAdmit {
		t.Fatal("load did not capture BuildDocuments' admission error")
	}

	// Sticky across calls: every subsequent Result for declarations
	// in the same file returns DocumentFailed.
	for i := 0; i < 3; i++ {
		if got := srcFS.Result(n); got != embed.DocumentFailed {
			t.Errorf("Result call %d = %v, want DocumentFailed (curAdmit sticky)", i, got)
		}
	}

	// Document method agrees: returns false (no document) AND
	// increments the unreadable counter so the operator sees the
	// failure.
	d, ok := srcFS.Document(n)
	if ok {
		t.Errorf("Document returned ok=true, want false (no document produced; admission failed)")
	}
	if d.NodeID != "" || d.Text != "" {
		t.Errorf("Document returned non-zero SemanticDocument %+v, want zero value", d)
	}
}

// TestFileDocumentSource_NoCurAdmitMeansExcluded is the negative
// counterpart: when the load did NOT surface an admission error
// (curAdmit=false), a missing node IS legitimately DocumentExcluded
// (no_span). The C4 fix only changes what happens when curAdmit is
// true — the no_span path remains.
func TestFileDocumentSource_NoCurAdmitMeansExcluded(t *testing.T) {
	srcFS := &fileDocumentSource{
		cur:     "x/y.go",
		curDocs: nil, // empty — no documents produced
	}
	n, _ := model.NewNode("function", "p.P", "x/y.go", 1, 1)
	if got := srcFS.Result(n); got != embed.DocumentExcluded {
		t.Errorf("Result = %v, want DocumentExcluded (declared no_span when no admission error)", got)
	}
}

// TestFileDocumentSource_DeclaredExclusionStaysExcluded is the
// positive counterpart: file/package/external kinds and nodes with
// no source path remain DocumentExcluded (declared legitimate
// exclusions). The C4 fix must NOT regress these — it only changes
// how MISSING declarations are classified, not how declared
// exclusions are.
func TestFileDocumentSource_DeclaredExclusionStaysExcluded(t *testing.T) {
	srcFS := &fileDocumentSource{
		cur:     "x/y.go",
		curDocs: nil,
	}
	cases := []struct {
		name string
		kind string
		path string
		want embed.DocumentResult
	}{
		{"file kind", parse.KindFile, "a/b.go", embed.DocumentExcluded},
		{"package kind", parse.KindPackage, "", embed.DocumentExcluded},
		{"external kind", parse.KindExternal, "", embed.DocumentExcluded},
		{"no source path", "function", "", embed.DocumentExcluded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, _ := model.NewNode(tc.kind, "x", tc.path, 1, 1)
			if got := srcFS.Result(n); got != tc.want {
				t.Errorf("Result = %v, want %v (declared exclusion)", got, tc.want)
			}
		})
	}
}

// failingEmbedder and alwaysFailAdmitter are used by the unit tests
// above; the package main entry point above references them through
// the fileDocumentSource unit-test path.
type failingEmbedder struct{ adm embed.Admission }

func (f failingEmbedder) ID() string { return "failing" }
func (f failingEmbedder) Dim() int   { return 8 }
func (f failingEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, &embed.AdmissionError{Limit: 1, Actual: 9999}
}
func (f failingEmbedder) Admit(ctx context.Context, text string) (embed.Admitted, error) {
	return f.adm.Admit(ctx, text)
}
