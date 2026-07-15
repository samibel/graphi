package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	committedGate = "../../.github/publish-lock.json"
	releaseDAG    = "../../.github/workflows/release-dag.yml"
	workflowsDir  = "../../.github/workflows"
)

// TestCommittedGateIsEngaged pins the shipped posture: the checked-in gate file
// is LOCKED, so the release DAG publishes nothing until RC-01 lifts it. This is
// the standing state the whole G0 program depends on.
func TestCommittedGateIsEngaged(t *testing.T) {
	d := Evaluate(committedGate)
	if !d.Locked {
		t.Fatalf("committed %s must be engaged (locked); got %+v", committedGate, d)
	}
	if d.OutputLine() != "locked=true" {
		t.Fatalf("committed gate must emit locked=true, got %q", d.OutputLine())
	}
}

// TestReleaseDAG_RedGateYieldsNoTagOrRelease is the SW-120 (REL-01) red-gate
// proof, asserted statically (the `act`-free equivalent of running the DAG
// with a failing gate): the ONLY mutating job (`publish`) `needs:` every prior
// stage — gate, build, sbom — and GitHub skips needs-failed dependents, so a
// deliberately red gate can never reach a tag push or a release. On top of the
// needs-chain, publish is conditioned on the CT-01 lock output and on an
// unpublished CHANGELOG version.
func TestReleaseDAG_RedGateYieldsNoTagOrRelease(t *testing.T) {
	yaml := readDAG(t)

	// The gate job evaluates the CT-01 lock and exposes it as an output.
	if !strings.Contains(yaml, "go run ./cmd/publish-lock") {
		t.Fatal("release-dag.yml must run `go run ./cmd/publish-lock` in its gate job")
	}
	if !regexp.MustCompile(`(?m)^\s*id:\s*lock\s*$`).MatchString(yaml) {
		t.Fatal("the publish-lock step must have `id: lock` so the gate job can output it")
	}
	if !strings.Contains(yaml, "locked: ${{ steps.lock.outputs.locked }}") {
		t.Fatal("the gate job must export the lock decision as a job output")
	}

	// The publish job depends on EVERY prior stage and is lock-conditioned.
	if !regexp.MustCompile(`(?m)^\s*needs:\s*\[gate,\s*build,\s*sbom\]\s*$`).MatchString(yaml) {
		t.Fatal("publish must `needs: [gate, build, sbom]` — the red-gate skip chain")
	}
	publishIf := jobIfExpression(t, yaml, "publish")
	for _, cond := range []string{
		"needs.gate.outputs.locked != 'true'",
		"needs.gate.outputs.tag != ''",
		"needs.gate.outputs.exists != 'true'",
	} {
		if !strings.Contains(publishIf, cond) {
			t.Fatalf("publish `if:` must contain %q; got %q", cond, publishIf)
		}
	}
}

// TestReleaseDAG_EveryCheckoutPinsTheGatedSHA: the tag must point at the commit
// the gates ran on — never a moved branch head. Every checkout in the DAG pins
// ref: github.sha, and the tag is created AT that SHA explicitly.
func TestReleaseDAG_EveryCheckoutPinsTheGatedSHA(t *testing.T) {
	yaml := readDAG(t)
	checkouts := strings.Count(yaml, "uses: actions/checkout")
	pinned := strings.Count(yaml, "ref: ${{ github.sha }}")
	if checkouts == 0 || checkouts != pinned {
		t.Fatalf("all %d checkout(s) must pin `ref: ${{ github.sha }}`; found %d pinned", checkouts, pinned)
	}
	if !strings.Contains(yaml, `git tag -a "$TAG" "${{ github.sha }}"`) {
		t.Fatal("the tag must be created explicitly AT the gated SHA")
	}
}

// TestReleaseDAG_IsTheOnlyPublishPath scans EVERY workflow: tag pushes and
// release creation exist exactly once each, inside release-dag.yml's publish
// job — no alternative tag/dispatch pipeline can bypass the DAG (the failure
// mode of the deleted auto-release.yml workflow_run chain, which could tag a
// commit whose release-gate never ran).
func TestReleaseDAG_IsTheOnlyPublishPath(t *testing.T) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatal(err)
	}
	tagPushes, releaseCreates := 0, 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(workflowsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		inDAG := e.Name() == filepath.Base(releaseDAG)
		// "push origin" catches both spellings: the tag push (`git push origin
		// "$TAG"`) and the tap/bucket clone push (`git -C "$dir" push origin HEAD`).
		if n := strings.Count(content, "push origin"); n > 0 {
			tagPushes += n
			if !inDAG {
				t.Errorf("%s pushes a tag/ref outside the release DAG", e.Name())
			}
		}
		if n := strings.Count(content, "gh release create"); n > 0 {
			releaseCreates += n
			if !inDAG {
				t.Errorf("%s creates a release outside the release DAG", e.Name())
			}
		}
		if strings.Contains(content, "gh workflow run") {
			t.Errorf("%s dispatches another workflow — the decoupled publish chain must stay dead", e.Name())
		}
	}
	// The tap/bucket publisher pushes to its own clones with `git push origin
	// HEAD` inside the SAME gated publish job; the tag push is the other one.
	if tagPushes != 2 {
		t.Fatalf("expected exactly 2 `git push origin` occurrences (tag + tap/bucket, both in the gated publish job), found %d", tagPushes)
	}
	if releaseCreates != 1 {
		t.Fatalf("expected exactly one `gh release create` (in the gated publish job), found %d", releaseCreates)
	}
}

func readDAG(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(releaseDAG)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// jobIfExpression returns the (possibly folded multi-line) `if:` expression of
// the named top-level job.
func jobIfExpression(t *testing.T, yaml, job string) string {
	t.Helper()
	lines := strings.Split(yaml, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimRight(ln, " ") == "  "+job+":" {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("job %q not found", job)
	}
	for i := start + 1; i < len(lines); i++ {
		ln := lines[i]
		if regexp.MustCompile(`^  \S`).MatchString(ln) {
			break // next top-level job
		}
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "if:") {
			// `if: >` folds the following more-indented lines.
			expr := strings.TrimSpace(strings.TrimPrefix(trimmed, "if:"))
			if expr == ">" || expr == "|" || expr == "" {
				var parts []string
				for j := i + 1; j < len(lines); j++ {
					if !strings.HasPrefix(lines[j], "      ") {
						break
					}
					parts = append(parts, strings.TrimSpace(lines[j]))
				}
				expr = strings.Join(parts, " ")
			}
			return expr
		}
	}
	t.Fatalf("job %q has no `if:` expression", job)
	return ""
}

// TestReleaseDAG_EveryActionIsSHAPinned (ADR 0005 U1, armed by SW-124): every
// `uses:` in the release DAG references a full 40-hex commit SHA with a
// trailing `# <tag>` comment naming the tag it was resolved from. A mutable
// tag reference in the PUBLISH path would let a re-pointed upstream tag swap
// the action's code under the DAG — the exact supply-chain hole the SHA-bound
// DAG exists to close. Other workflows may keep tag pins (they publish
// nothing); this file may not.
func TestReleaseDAG_EveryActionIsSHAPinned(t *testing.T) {
	yaml := readDAG(t)
	pinned := regexp.MustCompile(`(?m)uses:\s+[\w./-]+@[0-9a-f]{40} # \S+$`)
	anyUse := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s+(\S+.*)$`)
	for _, m := range anyUse.FindAllString(yaml, -1) {
		if !pinned.MatchString(m) {
			t.Errorf("release-dag action not pinned to a full commit SHA with a `# tag` comment: %s", strings.TrimSpace(m))
		}
	}
	if n := len(anyUse.FindAllString(yaml, -1)); n == 0 {
		t.Fatal("no uses: lines found — the DAG scan is broken")
	}
}
