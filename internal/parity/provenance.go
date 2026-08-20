package parity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/samibel/graphi/internal/parityreport"
)

func runtimeGOOS() string   { return runtime.GOOS }
func runtimeGOARCH() string { return runtime.GOARCH }

// ProductPaths are the trees whose bytes define the measured artifact. A
// difference here between the run SHA and the candidate means the harness is no
// longer measuring the candidate's product.
var ProductPaths = []string{"engine", "core", "surfaces", "cmd/graphi", "internal/evalreport", "cmd/eval"}

// CollectProvenance builds the run-level provenance block and FAILS CLOSED on
// every precondition it can check.
//
// The four refusals, and why each one is a refusal rather than a warning:
//
//	dirty worktree      — the run would measure source that is in no commit, so
//	                      the result could never be reproduced from a SHA.
//	product diff        — a changed product tree means the harness is measuring
//	                      something that is not the candidate, while the report
//	                      would still carry the candidate's name.
//	missing runner class— an unattributed measurement is an anecdote; PRD §12's
//	                      figures are only meaningful against a named machine.
//	pin mismatch        — checked per repository in Report.Finalize.
//
// PROVENANCE IS STATED HONESTLY AND NEVER OVERSTATED. The run may happen at a
// commit other than the candidate, so the statement this produces is "product
// source byte-identical to the ADR 0013 candidate at <sha>" and both SHAs are
// recorded. No record may say the run happened AT the candidate —
// parityreport.NewProvenance owns that sentence so no caller can phrase it any
// other way. (Under the original P0 candidate the harness did not even exist
// at the candidate SHA; since the 2026-08-16 candidate move it does, and the
// separation of the two SHAs is kept for the same reason regardless.)
func CollectProvenance(ctx context.Context, repoRoot string) parityreport.Provenance {
	head, _ := gitHead(ctx, repoRoot)
	p := parityreport.NewProvenance(head)

	dirty := gitOut(ctx, repoRoot, "status", "--porcelain")
	p.WorktreeClean = strings.TrimSpace(dirty) == ""
	if !p.WorktreeClean {
		p.WorktreeDirtyDetail = firstLines(dirty, 10)
	}

	args := []string{"diff", "--stat", parityreport.CandidateSHA + "..HEAD", "--"}
	args = append(args, ProductPaths...)
	args = append(args, ":!*_test.go")
	diff := gitOut(ctx, repoRoot, args...)
	pathDiffEmpty := strings.TrimSpace(diff) == ""
	if !pathDiffEmpty {
		p.ProductDiffDetail = "path diff (informational): " + firstLines(diff, 20)
	}

	// THE AUTHORITATIVE CHECK IS THE BUILT BINARY, NOT THE PATH DIFF.
	//
	// The path diff is a cheap tripwire and it is deliberately OVER-BROAD: it
	// flags any change under engine/, core/ or surfaces/, including packages
	// that cannot reach the product binary at all (engine/conformance, for
	// instance, is a test-only package whose sole non-test file is a package
	// doc). Refusing to publish because a doc comment moved in a package
	// cmd/graphi does not link would make the gate unusable without making it
	// safer.
	//
	// So the decisive signal is: does ./cmd/graphi build to the SAME BYTES at
	// HEAD and at the candidate? -trimpath is what makes that comparison
	// meaningful across two different build directories — without it the
	// absolute source path lands in DWARF comp_dir and two identical trees
	// hash differently purely because they were built somewhere else.
	head, cand, err := productBinaryDigests(ctx, repoRoot)
	p.ProductBinaryHead, p.ProductBinaryCandidate = head, cand
	switch {
	case err != nil:
		// FAIL CLOSED: an unverifiable boundary is not a satisfied one.
		p.ProductDiffEmpty = false
		p.ProductDiffDetail = "product-binary comparison UNAVAILABLE (" + err.Error() +
			"); the boundary is unverified, so publication is refused. " + p.ProductDiffDetail
	case head != "" && head == cand:
		p.ProductDiffEmpty = true
		if !pathDiffEmpty {
			p.ProductDiffDetail = "product binary is byte-identical to the candidate (" + short(head) +
				"); the path diff below touches only code the product binary does not link. " + p.ProductDiffDetail
		}
	default:
		p.ProductDiffEmpty = false
		p.ProductDiffDetail = "PRODUCT BINARY DIFFERS: HEAD builds to " + short(head) +
			", candidate to " + short(cand) + ". " + p.ProductDiffDetail
	}
	return p
}

// productBinaryDigests builds ./cmd/graphi at HEAD and at the candidate and
// returns both sha256 digests.
//
// The candidate tree is materialized with `git worktree add --detach`, which
// needs the candidate commit to be present — hence fetch-depth: 0 in the
// workflow. Both builds use -trimpath and -buildvcs=false so neither the build
// directory nor the VCS stamp can make two identical trees hash differently.
func productBinaryDigests(ctx context.Context, repoRoot string) (string, string, error) {
	tmp, err := os.MkdirTemp("", "graphi-parity-prov-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	build := func(dir, out string) error {
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./cmd/graphi")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, berr := cmd.CombinedOutput(); berr != nil {
			return fmt.Errorf("build in %s: %v: %s", dir, berr, strings.TrimSpace(string(b)))
		}
		return nil
	}

	headBin := filepath.Join(tmp, "head")
	if err := build(repoRoot, headBin); err != nil {
		return "", "", err
	}

	wt := filepath.Join(tmp, "candidate")
	if b, werr := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "--detach", "-f",
		wt, parityreport.CandidateSHA).CombinedOutput(); werr != nil {
		return fileDigest(headBin), "", fmt.Errorf("materialize candidate %s: %v: %s",
			parityreport.CandidateSHA[:12], werr, strings.TrimSpace(string(b)))
	}
	defer func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", wt).Run()
	}()

	candBin := filepath.Join(tmp, "cand")
	if err := build(wt, candBin); err != nil {
		return fileDigest(headBin), "", err
	}
	return fileDigest(headBin), fileDigest(candBin), nil
}

func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return digest(b)
}

func gitOut(ctx context.Context, dir string, args ...string) string {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "; ")
}
