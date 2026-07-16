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

// TestCommittedGateIsLifted pins the shipped posture SINCE the RC-01 Go
// (2026-07-15): the checked-in gate file is UNLOCKED, so the release DAG may
// publish an unpublished CHANGELOG version once every gate is green. Before the
// Go this test pinned the inverse (locked) — flipping it is the reviewed lock
// lift itself, so the gate file and this pin can never silently disagree. To
// re-engage the lock (incident/freeze), flip both back together.
func TestCommittedGateIsLifted(t *testing.T) {
	d := Evaluate(committedGate)
	if d.Locked {
		t.Fatalf("committed %s must be LIFTED (unlocked) after the RC-01 Go; got %+v", committedGate, d)
	}
	if d.OutputLine() != "locked=false" {
		t.Fatalf("lifted gate must emit locked=false, got %q", d.OutputLine())
	}
}

// TestReleaseDAG_RedGateYieldsNoTagOrRelease is the SW-120 (REL-01) red-gate
// proof, asserted statically (the `act`-free equivalent of running the DAG
// with a failing gate): the ONLY mutating job (`publish`) `needs:` every prior
// stage — gate, build, sbom — and GitHub skips needs-failed dependents, so a
// deliberately red gate can never reach a tag push or a release. On top of the
// needs-chain, publish is conditioned on the CT-01 lock output and on a
// CHANGELOG release version. Existing releases are re-entered safely so a
// partial downstream packaging run can resume; remote integrity preflight
// prevents blind mutation.
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
		"needs.gate.outputs.locked == 'false'",
		"needs.gate.outputs.tag != ''",
	} {
		if !strings.Contains(publishIf, cond) {
			t.Fatalf("publish `if:` must contain %q; got %q", cond, publishIf)
		}
	}
	if strings.Contains(publishIf, "needs.gate.outputs.locked != 'true'") {
		t.Fatal("publish lock must fail closed when the output is missing or malformed")
	}
}

// TestReleaseDAG_PublishesOnlyFromMainPush prevents a privileged manual
// dispatch from publishing an arbitrary side-branch SHA. Trigger filtering is
// enforced twice: at the workflow boundary and as a job-level fail-closed
// condition, including on the only job with write/OIDC permissions.
func TestReleaseDAG_PublishesOnlyFromMainPush(t *testing.T) {
	yaml := readDAG(t)
	if regexp.MustCompile(`(?m)^\s*workflow_dispatch\s*:`).MatchString(yaml) {
		t.Fatal("release DAG must not expose workflow_dispatch; it is a publish-capable side-branch bypass")
	}
	if !regexp.MustCompile(`(?ms)^on:\s*\n\s+push:\s*\n\s+branches:\s*\[main\]`).MatchString(yaml) {
		t.Fatal("release DAG must be triggered only by pushes to main")
	}

	const eventGuard = "github.event_name == 'push'"
	const branchGuard = "github.ref == 'refs/heads/main'"
	for _, job := range []string{"gate", "build", "sbom", "publish"} {
		expr := jobIfExpression(t, yaml, job)
		if !strings.Contains(expr, eventGuard) || !strings.Contains(expr, branchGuard) {
			t.Fatalf("job %q must fail closed outside a main push; got `if: %s`", job, expr)
		}
	}
}

// TestReleaseDAG_ExistingReleaseIsVerifiedNotBlindlySkipped locks the resumable
// state machine: an existing tag must be peeled to the gated SHA, the complete
// asset contract must verify, and `exists` alone must never skip publication.
func TestReleaseDAG_ExistingReleaseIsVerifiedNotBlindlySkipped(t *testing.T) {
	yaml := readDAG(t)
	for _, required := range []string{
		`refs/tags/$TAG^{}`,
		`peeled_sha" != "$GITHUB_SHA`,
		`go run ./cmd/release -list-release-assets`,
		`go run ./cmd/release -verify-assets`,
		`release_state: ${{ steps.release_state.outputs.state }}`,
	} {
		if !strings.Contains(yaml, required) {
			t.Fatalf("release DAG missing integrity state-machine fragment %q", required)
		}
	}
	if strings.Contains(yaml, "needs.gate.outputs.exists") {
		t.Fatal("release DAG must not treat tag+release existence as proof of completeness")
	}
}

func TestReleaseDAG_DraftsAreDiscoveredAndHistoricalReleaseIsSkipped(t *testing.T) {
	yaml := readDAG(t)
	// GitHub's get-by-tag endpoint only exposes published releases. Draft-safe
	// resumption must use the list endpoint and select the exact tag.
	if n := strings.Count(yaml, `gh api --paginate --slurp "repos/$GITHUB_REPOSITORY/releases?per_page=100"`); n < 4 {
		t.Fatalf("draft-safe release lookup must be used in every pre-publish state transition, got %d", n)
	}
	if n := strings.Count(yaml, `gh api "repos/$GITHUB_REPOSITORY/releases/tags/$TAG"`); n != 1 {
		t.Fatalf("published-only by-tag lookup is valid only for final published verification, got %d", n)
	}
	for _, fragment := range []string{
		"state=already-published",
		"needs.gate.outputs.release_state != 'already-published'",
		"bump CHANGELOG before the next release",
		"VERSION_INTRODUCED",
		"introduced by this candidate but is already published at conflicting SHA",
		`[ "$TAG" = v0.5.0 ]`,
		"go run ./cmd/release -list-v050-assets",
		"go run ./cmd/release -list-v050-provenance-assets",
		"go run ./cmd/release -verify-v050-assets",
		"go run ./cmd/release -verify-historical-assets",
	} {
		if !strings.Contains(yaml, fragment) {
			t.Fatalf("release DAG missing historical-release no-op contract %q", fragment)
		}
	}
}

func TestReleaseDAG_ExistingRemoteAssetsNeedValidProvenanceBeforeReuse(t *testing.T) {
	yaml := readDAG(t)
	for _, fragment := range []string{
		`gh attestation verify "$remote_assets/$name"`,
		`--signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release-dag.yml"`,
		`--source-digest "$GITHUB_SHA"`,
		`--source-digest "$peeled_sha"`,
	} {
		if !strings.Contains(yaml, fragment) {
			t.Fatalf("remote release reuse must be provenance-gated; missing %q", fragment)
		}
	}
}

func TestReleaseDAG_PublishedReleasesAreNeverRedrafted(t *testing.T) {
	yaml := readDAG(t)
	if strings.Contains(yaml, "redraft=") || regexp.MustCompile(`gh release edit "\$TAG" --draft(?:\s|$)`).MatchString(yaml) {
		t.Fatal("a published release must fail closed on drift; the DAG must never move it back to draft")
	}
	for _, fragment := range []string{
		"published releases are never mutated automatically",
		"refusing to mutate a published release",
	} {
		if !strings.Contains(yaml, fragment) {
			t.Fatalf("immutable published-release policy missing %q", fragment)
		}
	}
}

func TestReleaseDAG_ListProducersCannotFailInsideProcessSubstitution(t *testing.T) {
	yaml := readDAG(t)
	if strings.Contains(yaml, "< <(") {
		t.Fatal("process substitution hides producer exit status; materialize generated release lists first")
	}
	if n := strings.Count(yaml, `go run ./cmd/release -list-release-assets >"$assets"`); n < 4 {
		t.Fatalf("release asset lists must be materialized before loops, got %d guarded producers", n)
	}
}

func TestReleaseDAG_AttestationVerificationIsIdentityAndCommitBound(t *testing.T) {
	yaml := readDAG(t)
	verifies := strings.Count(yaml, "gh attestation verify")
	workflows := strings.Count(yaml, `--signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release-dag.yml"`)
	sources := strings.Count(yaml, "--source-digest")
	if verifies == 0 || verifies != workflows || verifies != sources {
		t.Fatalf("each of %d attestation verifications needs workflow and source binding (workflow=%d source=%d)", verifies, workflows, sources)
	}
	if !regexp.MustCompile(`(?s)uses: actions/attest-build-provenance@[^\n]+\n\s+if: steps\.preflight\.outputs\.complete != 'true'`).MatchString(yaml) {
		t.Fatal("already-published assets must not receive a fresh attestation")
	}
	gate := jobBlock(t, yaml, "gate")
	if !strings.Contains(gate, "attestations: read") {
		t.Fatal("gate verifies remote attestations and therefore needs attestations: read")
	}
}

// TestReleaseDAG_AttestTagThenDraftAndPublish asserts the fail-closed public
// transition order and supply-chain subject set. No tag exists before local
// integrity and provenance pass, and gh cannot auto-create one while creating
// the draft because the exact remote tag is established first and --verify-tag
// is mandatory.
func TestReleaseDAG_AttestTagThenDraftAndPublish(t *testing.T) {
	yaml := readDAG(t)
	ordered := []string{
		"assemble and checksum the complete release asset set",
		"verify assembled local release assets",
		"preflight remote tag/release state",
		"subject-path: release-assets/*",
		"verify provenance for every release asset",
		"create and push the tag at the gated SHA",
		"create/resume a draft and replace its assets",
		"verify the uploaded draft byte-for-byte",
		"publish the verified draft",
		"verify the published tag and release",
	}
	last := -1
	for _, fragment := range ordered {
		i := strings.Index(yaml, fragment)
		if i < 0 {
			t.Fatalf("release DAG missing %q", fragment)
		}
		if i <= last {
			t.Fatalf("release DAG order violation at %q", fragment)
		}
		last = i
	}
	createLines := 0
	for _, line := range strings.Split(yaml, "\n") {
		if !strings.Contains(line, `gh release create "$TAG"`) {
			continue
		}
		createLines++
		if !strings.Contains(line, "--verify-tag") || !strings.Contains(line, "--draft") {
			t.Fatalf("every release creation must require the pre-existing exact tag and create only a draft: %s", strings.TrimSpace(line))
		}
	}
	if createLines != 1 {
		t.Fatalf("expected exactly one guarded gh release create command, got %d", createLines)
	}
	publishBlock := yaml[strings.Index(yaml, "      - name: publish the verified draft"):]
	publishEdit := strings.Index(publishBlock, `gh release edit "$TAG" --draft=false`)
	prePublishTagCheck := strings.Index(publishBlock, "$TAG moved or disappeared before publication")
	prePublishDraftCheck := strings.Index(publishBlock, "$TAG is no longer the verified draft")
	if publishEdit < 0 || prePublishTagCheck < 0 || prePublishDraftCheck < 0 ||
		prePublishTagCheck >= publishEdit || prePublishDraftCheck >= publishEdit {
		t.Fatal("tag SHA and draft state must be revalidated immediately before public release")
	}
	if !strings.Contains(yaml, `gh release edit "$TAG" --draft=false`) {
		t.Fatal("verified draft must be explicitly published")
	}
	if strings.Contains(yaml, "subject-path: dist/*") {
		t.Fatal("attestation must cover SBOM and capability manifest, not only dist/*")
	}
}

func TestReleaseDAG_VerifiesPublishedWebUIFlavorAndArtifactSBOM(t *testing.T) {
	yaml := readDAG(t)
	verify := `go run ./cmd/release -webui -version "${{ steps.ver.outputs.version }}" -verify-only`
	build := `go run ./cmd/release -dist dist -webui -version "${{ steps.ver.outputs.version }}"`
	vi, bi := strings.Index(yaml, verify), strings.Index(yaml, build)
	if vi < 0 || bi < 0 || vi >= bi {
		t.Fatalf("published webui flavor must be reproducibility-verified before matrix build (verify=%d build=%d)", vi, bi)
	}
	if !strings.Contains(yaml, "path: release-assets") {
		t.Fatal("SBOM must scan assembled release artifacts")
	}
	if strings.Contains(yaml, "path: .\n          format: spdx-json") {
		t.Fatal("SBOM must not scan the source tree")
	}
}

func TestReleaseDAG_OneAuthoritativeReleaseGate(t *testing.T) {
	yaml := readDAG(t)
	if n := strings.Count(yaml, "go run ./cmd/release-gate -publish"); n != 1 {
		t.Fatalf("release DAG must have exactly one authoritative release-gate run, got %d", n)
	}
	if strings.Contains(yaml, "go run ./cmd/testgate") || strings.Contains(yaml, "go run ./cmd/coverage -check") {
		t.Fatal("release DAG must not duplicate constituent gates outside cmd/release-gate")
	}
}

// TestReleaseDAG_VulnerabilityGatesBlockPublish proves that publication does
// not trust an independently scheduled dependency-security workflow. The
// pinned Go reachability scan and both npm production-lockfile audits run in
// the exact-SHA gate job before the authoritative release decision; any red or
// unavailable scanner therefore skips every downstream job through `needs`.
func TestReleaseDAG_VulnerabilityGatesBlockPublish(t *testing.T) {
	yaml := readDAG(t)
	gate := jobBlock(t, yaml, "gate")

	for _, required := range []string{
		"ref: ${{ github.sha }}",
		"Go vulnerability reachability gate (exact SHA)",
		"go run golang.org/x/vuln/cmd/govulncheck@v1.6.0",
		"*/node_modules/*",
		"npm production vulnerability gates (exact SHA)",
		"for project in web extensions/vscode",
		"npm ci --omit=dev --ignore-scripts",
		"npm audit --omit=dev --audit-level=high",
	} {
		if !strings.Contains(gate, required) {
			t.Fatalf("exact-SHA release gate missing vulnerability control %q", required)
		}
	}
	if strings.Contains(gate, "continue-on-error: true") {
		t.Fatal("release gate vulnerability checks must fail closed, never continue on error")
	}

	goVuln := strings.Index(gate, "go run golang.org/x/vuln/cmd/govulncheck@v1.6.0")
	npmVuln := strings.Index(gate, "npm audit --omit=dev --audit-level=high")
	releaseGate := strings.Index(gate, "go run ./cmd/release-gate -publish")
	version := strings.Index(gate, "resolve the release version from CHANGELOG.md")
	if goVuln < 0 || npmVuln < 0 || releaseGate < 0 || version < 0 ||
		goVuln >= releaseGate || npmVuln >= releaseGate || releaseGate >= version {
		t.Fatalf("vulnerability checks must precede release verdict and version resolution (go=%d npm=%d release=%d version=%d)", goVuln, npmVuln, releaseGate, version)
	}
	if !regexp.MustCompile(`(?m)^\s*needs:\s*\[gate,\s*build,\s*sbom\]\s*$`).MatchString(jobBlock(t, yaml, "publish")) {
		t.Fatal("publish must transitively depend on the exact-SHA vulnerability gate")
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

func jobBlock(t *testing.T, yaml, job string) string {
	t.Helper()
	lines := strings.Split(yaml, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " ") == "  "+job+":" {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("job %q not found", job)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if regexp.MustCompile(`^  \S`).MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
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
// DAG exists to close. TestEveryWorkflowActionIsSHAPinned applies the same
// invariant repository-wide; this focused test additionally requires the
// release-DAG's human-auditable trailing tag comments.
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
