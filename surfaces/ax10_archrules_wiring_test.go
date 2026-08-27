// SW-230 (AX-10) — the end-to-end wiring proof `architecture-rules` was missing.
//
// SW-229 shipped both pack kinds but only ONE of them had a test that follows a
// pack all the way from the store into an answer: engine/analysis/taint/pack_test.go
// proves a taint pack changes what the analyzer finds. The architecture-rules
// kind had unit coverage of its merge and of archintel's rule evaluation, but
// nothing exercising surfaces/client.Direct.archRules() — the seam that decides
// whether an installed pack reaches the operation at all. That gap was filed in
// the backlog and is closed here, on the pack the SDK itself scaffolds, so the
// path a new author actually walks is the path under test.
//
// Three claims, and the third is the one ADR 0013 §4.1 calls a testable contract
// rather than an expectation: a DISABLED pack restores byte-identical pre-pack
// behaviour.
package surfaces_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/surfaces/client"
)

func TestAX10_AScaffoldedArchitecturePackReachesTheOperationAndRollsBack(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)

	// The repository root the pack store lives under. It is deliberately NOT the
	// fixture directory: archRules() keys on the bound root, and using a
	// throwaway one proves the rules come from the pack store rather than from
	// anything that happens to sit beside the source.
	root := t.TempDir()
	direct := charClient(store).WithRepoRoot(root)

	baseline, err := direct.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{})
	if err != nil {
		t.Fatalf("baseline ArchitectureViolations: %v", err)
	}
	if len(baseline) == 0 {
		t.Fatal("the pack-free baseline produced no bytes; this case proves nothing")
	}

	// Install the pack `graphi extension init` writes, unedited.
	packDir := filepath.Join(t.TempDir(), "pack")
	if _, err := extpack.ScaffoldInto(packDir, extpack.ScaffoldOptions{Kind: extpack.KindArchitectureRules}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	manifestPath := filepath.Join(packDir, extpack.PackManifestName)
	candidate, err := extpack.ValidateFile(manifestPath, "")
	if err != nil {
		t.Fatalf("validate the scaffolded pack: %v", err)
	}
	entry, err := extpack.Install(root, manifestPath, candidate.ManifestSHA256)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	withPack, err := direct.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{})
	if err != nil {
		t.Fatalf("ArchitectureViolations with a pack installed: %v", err)
	}
	if bytes.Equal(withPack, baseline) {
		t.Fatal("installing an architecture-rules pack changed nothing; Direct.archRules() is not wired " +
			"into the operation, which is exactly the gap this test exists to close")
	}
	// The pack's influence is VISIBLE in the answer, not merely present in the
	// store. The scaffolded rule does not fire on this fixture — its units are
	// `ui` and `storage`, which the Go corpus has no communities for — so what is
	// asserted here is the clause archintel emits either way: how many declared
	// rules from how many packs the answer was computed under. Per-finding
	// attribution when a rule DOES fire (`rule:<pack-id>:<rule-id>:…`) is pinned
	// by engine/agenttools/archintel's own tests; what was untested before this
	// file is that an installed pack reaches the operation at all.
	for _, want := range []string{"declared rule(s) from 1 pack(s)"} {
		if !strings.Contains(string(withPack), want) {
			t.Errorf("the answer does not mention %q:\n%s", want, withPack)
		}
	}
	if strings.Contains(string(baseline), "declared rule") {
		t.Errorf("the pack-FREE baseline already mentions declared rules; the comparison proves nothing:\n%s", baseline)
	}

	// Rollback: a disabled pack is not merely inert, it is byte-for-byte absent.
	if _, err := extpack.SetEnabled(root, entry.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled, err := direct.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{})
	if err != nil {
		t.Fatalf("ArchitectureViolations with the pack disabled: %v", err)
	}
	if !bytes.Equal(disabled, baseline) {
		t.Errorf("disabling the pack did not restore the pre-pack bytes (%d vs %d)", len(disabled), len(baseline))
	}

	// And removal is the same state, reached the other way.
	if err := extpack.Remove(root, entry.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed, err := direct.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{})
	if err != nil {
		t.Fatalf("ArchitectureViolations after removal: %v", err)
	}
	if !bytes.Equal(removed, baseline) {
		t.Errorf("removing the pack did not restore the pre-pack bytes (%d vs %d)", len(removed), len(baseline))
	}
}

// TestAX10_ABrokenPackFailsTheOperationClosed: a pack that cannot be loaded is
// an ERROR, not a skip. Answering an architecture question under rules the
// caller believes are in force and silently were not is the false-green this
// tree fails closed against everywhere else.
func TestAX10_ABrokenPackFailsTheOperationClosed(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)

	root := t.TempDir()
	direct := charClient(store).WithRepoRoot(root)

	packDir := filepath.Join(t.TempDir(), "pack")
	if _, err := extpack.ScaffoldInto(packDir, extpack.ScaffoldOptions{Kind: extpack.KindArchitectureRules}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	manifestPath := filepath.Join(packDir, extpack.PackManifestName)
	candidate, err := extpack.ValidateFile(manifestPath, "")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	entry, err := extpack.Install(root, manifestPath, candidate.ManifestSHA256)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Tamper with the INSTALLED bytes, which is the shape of the threat: the
	// lockfile still pins the hash the user approved.
	installed := filepath.Join(extpack.PackDir(root, entry.ID), "artifact")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read the installed artifact: %v", err)
	}
	if err := os.WriteFile(installed, append(data, []byte("# tampered\n")...), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	if _, err := direct.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{}); err == nil {
		t.Fatal("a tampered pack did not fail the operation; the answer would have been produced under " +
			"rules nobody approved")
	} else if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("the failure does not name the hash mismatch: %v", err)
	}
}
