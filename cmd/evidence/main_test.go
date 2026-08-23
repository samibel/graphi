package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The cmd-level pins for SW-205 AC-9 ("one gate, not two") and AC-10
// ("-generate refuses too"). They build the real binary once and run it against a
// hermetic fixture repository, because what is being pinned is exactly the wiring
// between the rule set and the two entry points — which a package-level test of
// internal/evidence cannot see.

func buildEvidence(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "evidence")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/evidence: %v\n%s", err, out)
	}
	return bin
}

// fixture builds a tiny repository with an evidence index whose single PASS row
// cites a file that does not exist, plus a matching rendered .md so the freshness
// rule is satisfied and the ONLY thing left to fail on is the citation.
func fixture(t *testing.T, bin string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "gate@example.invalid")
	git("config", "user.name", "gate")
	write("docs/rc/evidence-index.yaml", strings.Join([]string{
		"candidate:",
		"  source: docs/decisions/freeze.md",
		"  sha: 4e72637d3c2c0dc7d32142a590d46c0c62c10733",
		"  release_digest: UNKNOWN",
		"gates:",
		"  - id: ROW",
		"    gate: A gate",
		"    section: plan §1",
		"    status: PASS",
		"    evidence_uri: docs/rc/never-published.json",
		"    sha: 4e72637d3c2c0dc7d32142a590d46c0c62c10733",
		"",
	}, "\n"))
	git("add", "-A")
	git("commit", "-q", "-m", "index")
	return root
}

func run(t *testing.T, bin, root string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, append(args, "-root", root)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return string(out), code
}

// AC-9: the citation rules run inside -check. CI invokes nothing else.
func TestAC9_CitationRulesRunInsideCheck(t *testing.T) {
	bin := buildEvidence(t)
	root := fixture(t, bin)

	// Make the rendered .md fresh so the citation rule is the only thing wrong.
	if _, code := run(t, bin, root, "-generate"); code == 0 {
		t.Fatal("-generate must refuse while the citation is unresolvable (AC-10)")
	}

	out, code := run(t, bin, root, "-check")
	if code != 1 {
		t.Fatalf("-check must exit 1 on an unresolvable citation, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "never-published.json") {
		t.Fatalf("-check must report the citation itself, got:\n%s", out)
	}
	if !strings.Contains(out, "citation check") {
		t.Fatalf("-check must run the citation rules, not just the presence rules:\n%s", out)
	}
}

// AC-10: -generate refuses to write a dashboard that would render a false citation.
func TestAC10_GenerateRefusesOnAFailingCitation(t *testing.T) {
	bin := buildEvidence(t)
	root := fixture(t, bin)

	out, code := run(t, bin, root, "-generate")
	if code != 1 {
		t.Fatalf("-generate must exit 1, got %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "rc", "evidence-index.md")); !os.IsNotExist(err) {
		t.Fatal("-generate must not have written the dashboard")
	}
	if !strings.Contains(out, "never-published.json") {
		t.Fatalf("-generate must say which citation stopped it:\n%s", out)
	}
}

// AC-9: -check-citations is a focused SUBSET, never the only place the rules run.
// It reports the same violation -check does, and it skips the freshness rule.
func TestAC9_CheckCitationsIsASubsetNotASecondGate(t *testing.T) {
	bin := buildEvidence(t)
	root := fixture(t, bin)

	out, code := run(t, bin, root, "-check-citations")
	if code != 1 {
		t.Fatalf("-check-citations must exit 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "never-published.json") {
		t.Fatalf("-check-citations must report the citation:\n%s", out)
	}
	if strings.Contains(out, "freshness") {
		t.Fatalf("-check-citations must skip the freshness rule:\n%s", out)
	}
}

// A well-formed fixture passes end to end: -generate writes, -check goes green.
func TestCheckIsGreenWhenTheCitationResolves(t *testing.T) {
	bin := buildEvidence(t)
	root := fixture(t, bin)

	// Publish the cited artifact and point the row's sha at its real blob.
	p := filepath.Join(root, "docs", "rc", "never-published.json")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = root
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitc("add", "-A")
	gitc("commit", "-q", "-m", "publish")
	blob := gitc("rev-parse", "HEAD:docs/rc/never-published.json")

	yamlPath := filepath.Join(root, "docs", "rc", "evidence-index.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.Replace(string(raw), "sha: 4e72637d3c2c0dc7d32142a590d46c0c62c10733\n", "sha: "+blob+"\n", 2)
	if err := os.WriteFile(yamlPath, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc("commit", "-qam", "re-record")

	if out, code := run(t, bin, root, "-generate"); code != 0 {
		t.Fatalf("-generate must succeed once the citation resolves, got %d:\n%s", code, out)
	}
	gitc("add", "-A")
	gitc("commit", "-qam", "render")
	out, code := run(t, bin, root, "-check")
	if code != 0 {
		t.Fatalf("-check must be green, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "citation check PASS") {
		t.Fatalf("expected a green citation report:\n%s", out)
	}
}
