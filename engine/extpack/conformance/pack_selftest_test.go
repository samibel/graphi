package conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/extpack/conformance"
)

// TestAX10_TheScaffoldedPackPassesTheHarness is AC-1 and AC-5 meeting: what
// `graphi extension init` writes is not merely `validate`-clean, it is
// CONFORMANT — schema, host API, deterministic merge and provenance — on the
// first run, offline, with nothing edited.
//
// It runs for every kind the scaffold offers, because a scaffold that is
// conformant for one kind and broken for another is a scaffold whose promise
// depends on which flag the author passed.
func TestAX10_TheScaffoldedPackPassesTheHarness(t *testing.T) {
	kinds := extpack.ScaffoldKinds()
	if len(kinds) == 0 {
		t.Fatal("the scaffold offers no kinds; this test is not looking at anything")
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pack")
			if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{Kind: kind}); err != nil {
				t.Fatalf("scaffold %s: %v", kind, err)
			}
			report := conformance.VerifyPack(dir)
			if err := report.Err(); err != nil {
				t.Fatalf("a freshly scaffolded %s pack is not conformant:\n%v\n%s", kind, err, report)
			}
		})
	}
}

// TestAX10_TheSW229FixturePacksPassTheHarness is AC-5's other half: the packs
// SW-229 already ships as fixtures are held to the harness in the ordinary test
// run, so the harness and the product cannot drift apart without the suite
// noticing.
func TestAX10_TheSW229FixturePacksPassTheHarness(t *testing.T) {
	root := filepath.Join("..", "testdata", "packs")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			report := conformance.VerifyPack(dir)
			if err := report.Err(); err != nil {
				t.Fatalf("SW-229 fixture pack %s is not conformant:\n%v\n%s", e.Name(), err, report)
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatalf("no fixture pack was found under %s; this test proves nothing", root)
	}
}

// TestAX10_ABrokenPackFailsTheHarness is the pack half of the fail-closed proof.
// Each case breaks exactly one thing and must fail on the check that owns it.
func TestAX10_ABrokenPackFailsTheHarness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
		check  string
		want   string
	}{
		{
			name: "the artifact no longer hashes to what the manifest pins",
			mutate: func(t *testing.T, dir string) {
				appendLine(t, filepath.Join(dir, "rules.yaml"), "# an edit nobody re-pinned\n")
			},
			check: conformance.CheckManifest,
			want:  "sha256 mismatch",
		},
		{
			name: "the manifest declares a host api this build does not speak",
			mutate: func(t *testing.T, dir string) {
				replace(t, filepath.Join(dir, extpack.PackManifestName), `min: "1.0"`, `min: "9.0"`)
				replace(t, filepath.Join(dir, extpack.PackManifestName), `max: "1.0"`, `max: "9.0"`)
			},
			check: conformance.CheckManifest,
			want:  "this graphi speaks",
		},
		{
			name: "the artifact defines a rule the manifest never declared",
			mutate: func(t *testing.T, dir string) {
				appendLine(t, filepath.Join(dir, "rules.yaml"),
					"  - id: undeclared-rule\n    from: api\n    to: storage\n    description: A rule the manifest does not declare.\n")
				repin(t, dir, "rules.yaml")
			},
			check: conformance.CheckManifest,
			want:  "does not match its artifact",
		},
		{
			name: "a rule is missing a required field",
			mutate: func(t *testing.T, dir string) {
				replace(t, filepath.Join(dir, "rules.yaml"), "    from: ui\n", "    from: \"\"\n")
				repin(t, dir, "rules.yaml")
			},
			check: conformance.CheckArtifactSchema,
			want:  "is empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pack")
			if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{Kind: extpack.KindArchitectureRules}); err != nil {
				t.Fatalf("scaffold: %v", err)
			}
			// Non-vacuity: the pack was conformant before the mutation, so the
			// failure below is caused by the mutation and by nothing else.
			if err := conformance.VerifyPack(dir).Err(); err != nil {
				t.Fatalf("the scaffolded control was already failing: %v", err)
			}
			tc.mutate(t, dir)
			assertFails(t, conformance.VerifyPack(dir), tc.check, tc.want)
		})
	}
}

// TestAX10_PackDiagnosticsArePositionful is AC-2 seen from the harness: a
// failing pack check does not merely say what is wrong, it says where.
func TestAX10_PackDiagnosticsArePositionful(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if _, err := extpack.ScaffoldInto(dir, extpack.ScaffoldOptions{Kind: extpack.KindArchitectureRules}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	replace(t, filepath.Join(dir, extpack.PackManifestName), "determinism: deterministic", "determinism: whenever")
	report := conformance.VerifyPack(dir)
	if report.OK() {
		t.Fatal("a manifest declaring a determinism the tier cannot host passed")
	}
	detail := report.Failures()[0].Detail
	if !strings.Contains(detail, extpack.PackManifestName+":") {
		t.Errorf("the diagnostic carries no file:line position:\n%s", detail)
	}
	if !strings.Contains(detail, "determinism") {
		t.Errorf("the diagnostic does not name the field:\n%s", detail)
	}
}

func appendLine(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, []byte(text)...), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func replace(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	updated := strings.Replace(string(data), old, new, 1)
	if updated == string(data) {
		t.Fatalf("%s does not contain %q; the fixture mutation is a no-op", path, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// repin rewrites the manifest's artifact.sha256 so a mutation is tested for the
// reason it was written for rather than for the hash it incidentally broke.
func repin(t *testing.T, dir, artifact string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, artifact))
	if err != nil {
		t.Fatalf("read %s: %v", artifact, err)
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
