package extpack_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
)

// scaffoldPack writes a fresh, conformant pack and returns its directory.
func scaffoldPack(t *testing.T, kind extpack.Kind) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pack")
	if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{Kind: kind}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return dir
}

func editPack(t *testing.T, dir, file, old, replacement string) {
	t.Helper()
	path := filepath.Join(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	updated := strings.Replace(string(data), old, replacement, 1)
	if updated == string(data) {
		t.Fatalf("%s does not contain %q; the fixture edit is a no-op", path, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// repinArtifact rewrites artifact.sha256 so an artifact edit is tested for the
// schema violation it introduces rather than for the hash it breaks.
func repinArtifact(t *testing.T, dir, artifact string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, artifact))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	manifestPath := filepath.Join(dir, extpack.PackManifestName)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	lines := strings.Split(string(manifest), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "sha256:") {
			lines[i] = "  sha256: " + extpack.HashBytes(data)
		}
	}
	if err := os.WriteFile(manifestPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestAX10_LintIsCleanOnAConformantPack is the control every case below is
// measured against.
func TestAX10_LintIsCleanOnAConformantPack(t *testing.T) {
	for _, kind := range extpack.ScaffoldKinds() {
		dir := scaffoldPack(t, kind)
		if d := extpack.Lint(dir); len(d) != 0 {
			t.Errorf("%s: a conformant pack produced diagnostics: %v", kind, d)
		}
		// A directory and its manifest are the same subject.
		if d := extpack.Lint(filepath.Join(dir, extpack.PackManifestName)); len(d) != 0 {
			t.Errorf("%s: linting the manifest directly produced diagnostics: %v", kind, d)
		}
	}
}

// TestAX10_ManifestDiagnosticsArePositionful is AC-2: a rejection names the
// field, the file, the line and the column.
func TestAX10_ManifestDiagnosticsArePositionful(t *testing.T) {
	for _, tc := range []struct {
		name        string
		old, update string
		wantField   string
		wantText    string
	}{
		{"schema version", "schema_version: graphi.extension/v1alpha1", "schema_version: graphi.extension/v0", "schema_version", "unsupported schema_version"},
		{"pack id", "id: example.arch-rules", "id: Example..Rules", "id", "not a valid identifier"},
		{"kind", "kind: architecture-rules", "kind: query-presets", "kind", "does not implement yet"},
		{"api range out of reach", "  min: \"1.0\"\n  max: \"1.0\"", "  min: \"4.0\"\n  max: \"4.0\"", "api", "this graphi speaks"},
		{"api range inverted", "  min: \"1.0\"", "  min: \"4.0\"", "api", "is empty (min is above max)"},
		{"artifact path", "path: rules.yaml", "path: ../rules.yaml", "artifact.path", "bare file name"},
		{"artifact hash", "sha256: ", "sha256: not-a-hash # ", "artifact.sha256", "a hex sha256 is 64"},
		{"permission", "- graph:read", "- net:connect", "permissions[0]", "not available to a declarative pack"},
		{"determinism", "determinism: deterministic", "determinism: sometimes", "determinism", "is not accepted"},
		{"limits", "max_output_bytes: 65536", "max_output_bytes: 99999999", "limits.max_output_bytes", "exceeds the host ceiling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scaffoldPack(t, extpack.KindArchitectureRules)
			editPack(t, dir, extpack.PackManifestName, tc.old, tc.update)

			diagnostics := extpack.Lint(dir)
			if len(diagnostics) == 0 {
				t.Fatalf("no diagnostic for %s", tc.name)
			}
			d := findField(t, diagnostics, tc.wantField)
			if !strings.Contains(d.Message, tc.wantText) {
				t.Errorf("message %q does not contain %q", d.Message, tc.wantText)
			}
			if d.Line <= 0 || d.Column <= 0 {
				t.Errorf("diagnostic has no position: %+v", d)
			}
			if filepath.Base(d.File) != extpack.PackManifestName {
				t.Errorf("diagnostic file = %q, want the manifest", d.File)
			}
			if !strings.HasPrefix(d.String(), fmt.Sprintf("%s:%d:%d: ", d.File, d.Line, d.Column)) {
				t.Errorf("rendered form is not file:line:col — %q", d.String())
			}
			assertPointsAtTheEditedLine(t, filepath.Join(dir, extpack.PackManifestName), d, tc.update)
		})
	}
}

// TestAX10_ArtifactDiagnosticsArePositionfulAndScopedToTheArtifactFile is the
// half `validate` never had: the artifact is a SECOND document with its own
// schema, and a rule with an empty field is located at that rule's own line.
func TestAX10_ArtifactDiagnosticsArePositionfulAndScopedToTheArtifactFile(t *testing.T) {
	dir := scaffoldPack(t, extpack.KindTaintRules)
	editPack(t, dir, "taint.yaml", "      - user-input\n", "")
	repinArtifact(t, dir, "taint.yaml")

	diagnostics := extpack.Lint(dir)
	if len(diagnostics) == 0 {
		t.Fatal("a sanitizer with no remove_labels produced no diagnostic")
	}
	d := diagnostics[0]
	if filepath.Base(d.File) != "taint.yaml" {
		t.Errorf("diagnostic file = %q, want the artifact", d.File)
	}
	if !strings.Contains(d.Message, "universal sanitizer") {
		t.Errorf("message = %q", d.Message)
	}
	if d.Line <= 0 {
		t.Errorf("artifact diagnostic has no line: %+v", d)
	}
	if !strings.HasPrefix(d.Field, "sanitizers[0]") {
		t.Errorf("field = %q, want the offending sanitizer", d.Field)
	}
}

// TestAX10_LintReportsEveryManifestProblemAtOnce is the difference between the
// linter and `validate`: an author editing a manifest sees the whole list, not
// one problem per round trip.
func TestAX10_LintReportsEveryManifestProblemAtOnce(t *testing.T) {
	dir := scaffoldPack(t, extpack.KindArchitectureRules)
	editPack(t, dir, extpack.PackManifestName, "determinism: deterministic", "determinism: sometimes")
	editPack(t, dir, extpack.PackManifestName, "- graph:read", "- net:connect")
	editPack(t, dir, extpack.PackManifestName, "max_output_bytes: 65536", "max_output_bytes: 0")

	diagnostics := extpack.Lint(dir)
	if len(diagnostics) < 3 {
		t.Fatalf("lint reported %d diagnostic(s) for three independent problems: %v", len(diagnostics), diagnostics)
	}
	for _, field := range []string{"permissions[0]", "determinism", "limits.max_output_bytes"} {
		findField(t, diagnostics, field)
	}

	// And Validate is still the head of the same list, so a linter and a
	// validator cannot disagree about whether the pack is valid.
	if _, err := extpack.ValidateFile(filepath.Join(dir, extpack.PackManifestName), ""); err == nil {
		t.Fatal("ValidateFile accepted a manifest lint rejected")
	} else if err.Error() != diagnostics[0].Message {
		t.Errorf("Validate reported %q, the first diagnostic is %q", err, diagnostics[0].Message)
	}
}

// TestAX10_LintNeverPanicsAndAlwaysSpeaks: the worst case — a pack so broken it
// cannot be opened or parsed — must still produce a diagnostic rather than
// silence.
func TestAX10_LintNeverPanicsAndAlwaysSpeaks(t *testing.T) {
	t.Run("no such path", func(t *testing.T) {
		if d := extpack.Lint(filepath.Join(t.TempDir(), "nope.yaml")); len(d) == 0 {
			t.Fatal("a missing pack produced no diagnostic")
		}
	})
	t.Run("directory with no manifest", func(t *testing.T) {
		if d := extpack.Lint(t.TempDir()); len(d) == 0 {
			t.Fatal("an empty directory produced no diagnostic")
		}
	})
	t.Run("unparseable yaml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, extpack.PackManifestName), []byte("id: [unclosed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d := extpack.Lint(dir)
		if len(d) == 0 {
			t.Fatal("an unparseable manifest produced no diagnostic")
		}
		if d[0].Line <= 0 {
			t.Errorf("a YAML parse failure lost its line: %+v", d[0])
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		dir := scaffoldPack(t, extpack.KindArchitectureRules)
		editPack(t, dir, extpack.PackManifestName, "determinism: deterministic",
			"determinism: deterministic\nphone_home: https://example.invalid")
		d := extpack.Lint(dir)
		if len(d) == 0 {
			t.Fatal("an unknown manifest field produced no diagnostic")
		}
		if !strings.Contains(d[0].Message, "field phone_home not found") {
			t.Errorf("message = %q, want the unknown-field rejection", d[0].Message)
		}
	})
}

// TestAX10_LintDetectsAnUnpinnedEdit is the mistake every pack author makes
// exactly once: edit the artifact, forget to re-hash it.
func TestAX10_LintDetectsAnUnpinnedEdit(t *testing.T) {
	dir := scaffoldPack(t, extpack.KindArchitectureRules)
	editPack(t, dir, "rules.yaml", "    from: ui", "    from: web")

	diagnostics := extpack.Lint(dir)
	if len(diagnostics) == 0 {
		t.Fatal("an unpinned artifact edit produced no diagnostic")
	}
	d := findField(t, diagnostics, "artifact.sha256")
	if !strings.Contains(d.Message, "sha256 mismatch") {
		t.Errorf("message = %q", d.Message)
	}
	if d.Line <= 0 {
		t.Errorf("the mismatch diagnostic has no position: %+v", d)
	}
}

func findField(t *testing.T, diagnostics []extpack.Diagnostic, field string) extpack.Diagnostic {
	t.Helper()
	for _, d := range diagnostics {
		if d.Field == field {
			return d
		}
	}
	t.Fatalf("no diagnostic for field %q; got %v", field, diagnostics)
	return extpack.Diagnostic{}
}

// assertPointsAtTheEditedLine checks the reported line actually contains the
// text the fixture edited in. A position that is merely non-zero proves nothing;
// this is what makes "positionful" mean "correct".
func assertPointsAtTheEditedLine(t *testing.T, path string, d extpack.Diagnostic, edited string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if d.Line > len(lines) {
		t.Fatalf("diagnostic points at line %d of a %d-line file", d.Line, len(lines))
	}
	// The edit may land on the key line or on the line under it (api.min sits
	// under `api:`), so accept the reported line or the two after it. Empty
	// lines are skipped: "" is a substring of everything and would make this
	// assertion pass for free.
	want := strings.TrimSpace(strings.Split(edited, "#")[0])
	for i := d.Line - 1; i < len(lines) && i < d.Line+2; i++ {
		got := strings.TrimSpace(lines[i])
		if got == "" {
			continue
		}
		if strings.Contains(got, want) || strings.Contains(want, got) {
			return
		}
	}
	t.Errorf("diagnostic points at %s:%d (%q), which is not where %q was written",
		path, d.Line, strings.TrimSpace(lines[d.Line-1]), edited)
}
