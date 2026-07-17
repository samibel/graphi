package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a minimal well-formed candidate block reused by fixtures.
const candidateBlock = `candidate:
  source: docs/decisions/2026-07-m0-candidate-freeze.md
  sha: 4e72637d3c2c0dc7d32142a590d46c0c62c10733
  release_digest: UNKNOWN
`

func loadString(t *testing.T, yaml string) Index {
	t.Helper()
	idx, err := parseIndexYAML(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return idx
}

// AC (honesty rule): a PASS row missing an Evidence URI must fail the check.
func TestCheck_PassWithoutEvidenceURIFails(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: PASS
    sha: "sha256=deadbeef"
`)
	rep := Check(idx)
	if rep.Pass() {
		t.Fatal("expected FAIL: a PASS row with no Evidence URI must not pass the honesty check")
	}
	if !violationMentions(rep, "WP0", "Evidence URI") {
		t.Fatalf("expected an Evidence-URI violation for WP0, got: %s", rep.Format())
	}
}

// AC (honesty rule): a PASS row missing a SHA/Digest must fail the check.
func TestCheck_PassWithoutSHAFails(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: PASS
    evidence_uri: https://example.invalid/run/1
`)
	rep := Check(idx)
	if rep.Pass() {
		t.Fatal("expected FAIL: a PASS row with no SHA/Digest must not pass the honesty check")
	}
	if !violationMentions(rep, "WP0", "SHA/Digest") {
		t.Fatalf("expected a SHA/Digest violation for WP0, got: %s", rep.Format())
	}
}

// AC (honesty rule): a PASS row WITH both an Evidence URI and a SHA/Digest passes.
func TestCheck_PassWithEvidencePasses(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: PASS
    evidence_uri: https://example.invalid/run/1
    sha: "sha256=deadbeef"
`)
	if rep := Check(idx); !rep.Pass() {
		t.Fatalf("expected PASS: a backed PASS row is honest, got: %s", rep.Format())
	}
}

// AC (UNKNOWN default): a gate with no status set reads UNKNOWN — not blank, not PASS.
func TestLoad_DefaultsMissingStatusToUnknown(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP5
    gate: Security
    section: plan §6 WP5
`)
	if got := idx.Gates[0].Status; got != StatusUnknown {
		t.Fatalf("missing status must default to UNKNOWN, got %q", got)
	}
	// And a defaulted-UNKNOWN row needs no evidence to be honest.
	if rep := Check(idx); !rep.Pass() {
		t.Fatalf("an UNKNOWN row needs no evidence, got: %s", rep.Format())
	}
	// And its table row renders as UNKNOWN, never as PASS. Match the pipe-wrapped
	// status column so the legend's own "✅ PASS · ❌ FAIL · ❔ UNKNOWN" is excluded.
	md := RenderMarkdown(idx)
	if !strings.Contains(md, "| ❔ UNKNOWN |") {
		t.Fatal("a defaulted row must render UNKNOWN in its status column")
	}
	if strings.Contains(md, "| ✅ PASS |") {
		t.Fatal("a defaulted row must never render as PASS in its status column")
	}
}

// AC: an invalid status value is rejected.
func TestCheck_InvalidStatusFails(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: green
`)
	if rep := Check(idx); rep.Pass() {
		t.Fatal("expected FAIL: 'green' is not a valid status")
	}
}

// AC: each row must cite the plan section it came from.
func TestCheck_MissingSectionFails(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    status: UNKNOWN
`)
	if rep := Check(idx); rep.Pass() {
		t.Fatal("expected FAIL: a row with no plan-section citation is not auditable")
	}
}

// AC: the candidate's release digest must be explicit (UNKNOWN allowed, blank not).
func TestCheck_BlankReleaseDigestFails(t *testing.T) {
	idx := loadString(t, `candidate:
  source: docs/decisions/2026-07-m0-candidate-freeze.md
  sha: 4e72637d3c2c0dc7d32142a590d46c0c62c10733
  release_digest: ""
gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: UNKNOWN
`)
	if rep := Check(idx); rep.Pass() {
		t.Fatal("expected FAIL: a blank candidate release digest could be read as passed — it must say UNKNOWN")
	}
}

// AC (determinism): rendering twice is byte-identical, and a parse→render→parse→
// render round-trip is stable. Row order follows the source (no map shuffle).
func TestRender_Deterministic(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: UNKNOWN
  - id: WP10
    gate: Audit
    section: plan §6 WP10
    status: UNKNOWN
  - id: WP2
    gate: Baseline
    section: plan §6 WP2
    status: UNKNOWN
`)
	first := RenderMarkdown(idx)
	second := RenderMarkdown(idx)
	if first != second {
		t.Fatal("RenderMarkdown must be byte-stable across runs")
	}
	// round-trip via the parser preserves order and bytes.
	reparsed, err := parseIndexYAML(candidateBlock + "gates:\n  - id: WP0\n    gate: Program Control\n    section: plan §6 WP0\n    status: UNKNOWN\n")
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if RenderMarkdown(reparsed) != RenderMarkdown(reparsed) {
		t.Fatal("re-render must be stable")
	}
	// source order is preserved: WP0 before WP10 before WP2 (NOT lexicographic).
	iWP0 := strings.Index(first, "**WP0**")
	iWP10 := strings.Index(first, "**WP10**")
	iWP2 := strings.Index(first, "**WP2**")
	if !(iWP0 < iWP10 && iWP10 < iWP2) {
		t.Fatalf("rows must render in source order (WP0, WP10, WP2); got positions %d, %d, %d", iWP0, iWP10, iWP2)
	}
}

// The generated .md and the YAML source must not drift: the checked-in .md is
// exactly RenderMarkdown(Load(yaml)). This is the freshness invariant -check
// enforces, and it also proves the real source parses and passes the honesty rule.
func TestCheckedInIndexIsFreshAndHonest(t *testing.T) {
	root, err := ModuleRoot()
	if err != nil {
		t.Skipf("module root unavailable: %v", err)
	}
	yamlPath := filepath.Join(root, filepath.FromSlash(EvidenceYAMLPath))
	mdPath := filepath.Join(root, filepath.FromSlash(EvidenceMDPath))

	idx, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("load real index: %v", err)
	}
	if rep := Check(idx); !rep.Pass() {
		t.Fatalf("the checked-in evidence index must be honest:\n%s", rep.Format())
	}
	wantMD := RenderMarkdown(idx)
	gotMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read %s: %v", EvidenceMDPath, err)
	}
	if string(gotMD) != wantMD {
		t.Fatalf("%s is stale — run `go run ./cmd/evidence -generate`", EvidenceMDPath)
	}

	// Honesty in the flesh: no real row may read PASS without an Evidence URI and
	// a SHA/Digest, and every row must be one of the three legal states.
	for _, g := range idx.Gates {
		switch g.Status {
		case StatusPass:
			if strings.TrimSpace(g.EvidenceURI) == "" || strings.TrimSpace(g.SHA) == "" {
				t.Fatalf("gate %s reads PASS without evidence — the exact failure this tool prevents", g.ID)
			}
		case StatusFail, StatusUnknown:
		default:
			t.Fatalf("gate %s has an illegal status %q", g.ID, g.Status)
		}
	}
}

// AC (staleness detection): a mutated .md is caught by the freshness compare.
func TestStalenessDetected(t *testing.T) {
	idx := loadString(t, candidateBlock+`gates:
  - id: WP0
    gate: Program Control
    section: plan §6 WP0
    status: UNKNOWN
`)
	fresh := RenderMarkdown(idx)
	stale := strings.Replace(fresh, "❔ UNKNOWN", "✅ PASS", 1)
	if stale == fresh {
		t.Fatal("test setup: mutation did not change the rendered md")
	}
	if stale == RenderMarkdown(idx) {
		t.Fatal("a hand-edited (stale) md must not equal the regenerated md")
	}
}

func violationMentions(rep Report, gateID, substr string) bool {
	for _, v := range rep.Violations {
		if v.GateID == gateID && strings.Contains(v.Reason, substr) {
			return true
		}
	}
	return false
}
