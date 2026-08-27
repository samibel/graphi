package extpack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/internal/goldenfile"
)

// TestAX10_ScaffoldOutputIsByteStable pins what `graphi extension init` writes.
//
// It is a golden and not a shape assertion on purpose. The scaffold's output is
// the first thing every pack author sees and the thing they build on; a template
// that silently reflowed itself between releases would change what every new
// pack looks like, and a structural test would forgive exactly that. Regenerate
// with GRAPHI_UPDATE_GOLDEN=1 and review the diff, as with every other AX-00
// golden.
func TestAX10_ScaffoldOutputIsByteStable(t *testing.T) {
	for _, kind := range extpack.ScaffoldKinds() {
		t.Run(string(kind), func(t *testing.T) {
			files, err := extpack.Scaffold(extpack.ScaffoldOptions{Kind: kind})
			if err != nil {
				t.Fatalf("scaffold %s: %v", kind, err)
			}
			if len(files) == 0 {
				t.Fatal("the scaffold produced no files")
			}
			for _, f := range files {
				goldenfile.Assert(t, filepath.Join("testdata", "scaffold", string(kind), f.Name), f.Data)
			}
		})
	}
}

// TestAX10_ScaffoldIsAPureFunctionOfItsOptions is the determinism half: two
// renders of the same options are identical bytes, and nothing about the host
// (clock, path, environment) leaks in.
func TestAX10_ScaffoldIsAPureFunctionOfItsOptions(t *testing.T) {
	for _, kind := range extpack.ScaffoldKinds() {
		first, err := extpack.Scaffold(extpack.ScaffoldOptions{Kind: kind, ID: "acme.example"})
		if err != nil {
			t.Fatalf("scaffold: %v", err)
		}
		second, err := extpack.Scaffold(extpack.ScaffoldOptions{Kind: kind, ID: "acme.example"})
		if err != nil {
			t.Fatalf("scaffold: %v", err)
		}
		if len(first) != len(second) {
			t.Fatalf("%s: two renders produced %d and %d files", kind, len(first), len(second))
		}
		for i := range first {
			if first[i].Name != second[i].Name || string(first[i].Data) != string(second[i].Data) {
				t.Errorf("%s: %s differs between two renders of identical options", kind, first[i].Name)
			}
		}
	}
}

// TestAX10_AScaffoldedPackValidatesImmediately is AC-1's load-bearing half. The
// scaffold hashes the artifact it just wrote, so the pack installs without the
// author computing a SHA-256 by hand — which is the step a first-time author
// gets wrong.
func TestAX10_AScaffoldedPackValidatesImmediately(t *testing.T) {
	for _, kind := range extpack.ScaffoldKinds() {
		t.Run(string(kind), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pack")
			written, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{Kind: kind})
			if err != nil {
				t.Fatalf("scaffold into %s: %v", dir, err)
			}
			if len(written) != 4 {
				t.Fatalf("wrote %d files, want manifest + artifact + test + README", len(written))
			}
			candidate, err := extpack.ValidateFile(filepath.Join(dir, extpack.PackManifestName), "")
			if err != nil {
				t.Fatalf("a freshly scaffolded pack does not validate: %v", err)
			}
			if candidate.Manifest.Kind != kind {
				t.Errorf("scaffolded kind = %q, want %q", candidate.Manifest.Kind, kind)
			}
			if diagnostics := extpack.Lint(dir); len(diagnostics) != 0 {
				t.Errorf("a freshly scaffolded pack does not lint clean: %v", diagnostics)
			}
			// And it installs, offline, with the hash `validate` printed.
			root := t.TempDir()
			entry, err := extpack.Install(root, filepath.Join(dir, extpack.PackManifestName), candidate.ManifestSHA256)
			if err != nil {
				t.Fatalf("a freshly scaffolded pack does not install: %v", err)
			}
			if !entry.Enabled {
				t.Error("the installed pack is not enabled")
			}
		})
	}
}

// TestAX10_ScaffoldRefusesToOverwrite: `init` is a scaffold, not a reset.
func TestAX10_ScaffoldRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{}); err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, extpack.PackManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, extpack.PackManifestName),
		append(before, []byte("\n# an author's edit\n")...), 0o644); err != nil {
		t.Fatalf("edit manifest: %v", err)
	}
	if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{}); err == nil {
		t.Fatal("the second scaffold overwrote an existing pack")
	}
	after, err := os.ReadFile(filepath.Join(dir, extpack.PackManifestName))
	if err != nil {
		t.Fatalf("read manifest back: %v", err)
	}
	if !strings.Contains(string(after), "an author's edit") {
		t.Error("the author's edit was destroyed by a refused scaffold")
	}
}

// TestAX10_ScaffoldRejectsAnInvalidID: the id becomes a directory name in the
// pack store, so it is checked at the scaffold, not at install time.
func TestAX10_ScaffoldRejectsAnInvalidID(t *testing.T) {
	for _, id := range []string{"../escape", "UPPER", "trailing-", ".leading", "with/slash"} {
		if _, err := extpack.Scaffold(extpack.ScaffoldOptions{ID: id}); err == nil {
			t.Errorf("scaffold accepted pack id %q", id)
		}
	}
}

// TestAX10_ScaffoldRefusesADeferredKind keeps the "not yet" message distinct
// from "unknown" at the scaffold too — an author asking for a planned kind gets
// the backlog answer rather than a typo answer.
func TestAX10_ScaffoldRefusesADeferredKind(t *testing.T) {
	if _, err := extpack.Scaffold(extpack.ScaffoldOptions{Kind: extpack.Kind("query-presets")}); err == nil {
		t.Fatal("the scaffold produced a pack of a kind this build cannot load")
	}
}
