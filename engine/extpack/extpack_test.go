package extpack_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/extpack"
)

// The two shipped example packs (AC-6). They are read from testdata rather than
// generated, so the fixtures a reader can open are the fixtures the tests prove.
const (
	layeringPack = "layering"
	taintPack    = "acme-taint"
)

func packPath(name string) string { return filepath.Join("testdata", "packs", name, "pack.yaml") }

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// installFixture installs one of the example packs into root and returns its
// lockfile entry.
func installFixture(t *testing.T, root, name string) extpack.LockEntry {
	t.Helper()
	path := packPath(name)
	entry, err := extpack.Install(root, path, sha256File(t, path))
	if err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	return entry
}

// TestAC1_ValidateAcceptsTheShippedExamplePacks is the floor every other test
// stands on: if the two example packs did not validate, nothing below would be
// evidence of anything.
func TestAC1_ValidateAcceptsTheShippedExamplePacks(t *testing.T) {
	for _, name := range []string{layeringPack, taintPack} {
		path := packPath(name)
		candidate, err := extpack.ValidateFile(path, sha256File(t, path))
		if err != nil {
			t.Fatalf("validate %s: %v", name, err)
		}
		if candidate.Manifest.SchemaVersion != extpack.SchemaVersion {
			t.Errorf("%s: schema_version = %q, want %q", name, candidate.Manifest.SchemaVersion, extpack.SchemaVersion)
		}
		if candidate.ManifestSHA256 == "" || candidate.ArtifactSHA256 == "" {
			t.Errorf("%s: validate produced no hashes: %+v", name, candidate)
		}
	}
}

// TestAC2_InstallVerifiesBeforeAnyWriteAndRecordsTheLockfile pins the three
// halves of AC-2 that are about the install itself: the hash is required, it is
// checked before a byte is written, and the result is recorded.
func TestAC2_InstallVerifiesBeforeAnyWriteAndRecordsTheLockfile(t *testing.T) {
	root := t.TempDir()
	entry := installFixture(t, root, layeringPack)

	if entry.ID != "graphi.layering" || entry.Version != "1.0.0" {
		t.Fatalf("entry = %+v", entry)
	}
	if !entry.Enabled {
		t.Error("a freshly installed pack must be enabled")
	}
	lock, err := extpack.LoadLock(root)
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	if len(lock.Packs) != 1 || lock.Packs[0].ID != entry.ID {
		t.Fatalf("lockfile = %+v", lock)
	}
	if lock.Packs[0].ManifestSHA256 != sha256File(t, packPath(layeringPack)) {
		t.Errorf("lockfile records manifest hash %q, want the file's own hash", lock.Packs[0].ManifestSHA256)
	}
	// The store holds the two files under FIXED names — the pack's own
	// artifact.path is never a path after install.
	for _, name := range []string{"manifest", "artifact"} {
		if _, statErr := os.Stat(filepath.Join(extpack.PackDir(root, entry.ID), name)); statErr != nil {
			t.Errorf("expected %s in the pack dir: %v", name, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(extpack.PackDir(root, entry.ID), "rules.yaml")); statErr == nil {
		t.Error("the pack's own artifact.path must not appear in the store: a pack-supplied name is never a stored path")
	}
}

// TestAC2_InstallWithoutAHashIsRefused: an optional pin is not a pin.
func TestAC2_InstallWithoutAHashIsRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := extpack.Install(root, packPath(layeringPack), ""); !errors.Is(err, extpack.ErrHashRequired) {
		t.Fatalf("install without --sha256 = %v, want ErrHashRequired", err)
	}
	if _, statErr := os.Stat(extpack.LockPath(root)); statErr == nil {
		t.Error("a refused install wrote a lockfile")
	}
}

// TestAC4_MergeOrderIsIndependentOfInstallOrder is the determinism contract,
// proved by installing the same two packs in both orders into two separate roots
// and byte-comparing the lockfile AND the merged rule data.
func TestAC4_MergeOrderIsIndependentOfInstallOrder(t *testing.T) {
	render := func(order []string) (string, string) {
		root := t.TempDir()
		for _, name := range order {
			installFixture(t, root, name)
		}
		lockBytes, err := os.ReadFile(extpack.LockPath(root))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}
		set, err := extpack.Load(root)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		var b strings.Builder
		for _, r := range set.Refs() {
			fmt.Fprintf(&b, "ref %s\n", r)
		}
		for _, r := range set.ArchRules() {
			fmt.Fprintf(&b, "arch %s %s->%s %s\n", r.ID, r.From, r.To, r.Pack)
		}
		for _, d := range set.TaintSources() {
			fmt.Fprintf(&b, "source %s %s %v %s\n", d.ID, d.Label, d.NamePatterns, d.Pack)
		}
		for _, d := range set.TaintSinks() {
			fmt.Fprintf(&b, "sink %s %s %v %s\n", d.ID, d.Category, d.NamePatterns, d.Pack)
		}
		for _, d := range set.TaintSanitizers() {
			fmt.Fprintf(&b, "sanitizer %s %v %v %s\n", d.ID, d.NamePatterns, d.RemoveLabels, d.Pack)
		}
		fmt.Fprintf(&b, "fingerprint %s\n", set.Fingerprint())
		return string(lockBytes), b.String()
	}

	lockA, mergedA := render([]string{layeringPack, taintPack})
	lockB, mergedB := render([]string{taintPack, layeringPack})
	if lockA != lockB {
		t.Errorf("lockfile depends on install order:\n--- A ---\n%s\n--- B ---\n%s", lockA, lockB)
	}
	if mergedA != mergedB {
		t.Errorf("merged pack data depends on install order:\n--- A ---\n%s\n--- B ---\n%s", mergedA, mergedB)
	}
	if !strings.Contains(mergedA, "arch no-core-to-engine") || !strings.Contains(mergedA, "source acme_ingest_header") {
		t.Fatalf("the permutation comparison is vacuous — it merged nothing:\n%s", mergedA)
	}
}

// TestAC3_DisablingAPackRestoresTheExactPrePackState is ADR 0013 §4.1's tier-A
// rollback, byte-compared rather than eyeballed.
func TestAC3_DisablingAPackRestoresTheExactPrePackState(t *testing.T) {
	root := t.TempDir()

	before, err := extpack.Load(root)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if !before.Empty() || before.Fingerprint() != "" {
		t.Fatalf("a repository with no packs must load an empty set with an empty fingerprint: %+v", before)
	}

	installFixture(t, root, layeringPack)
	enabled, err := extpack.Load(root)
	if err != nil {
		t.Fatalf("load enabled: %v", err)
	}
	if len(enabled.ArchRules()) != 2 || enabled.Fingerprint() == "" {
		t.Fatalf("the enabled pack contributed nothing: rules=%d fingerprint=%q", len(enabled.ArchRules()), enabled.Fingerprint())
	}

	if _, err := extpack.SetEnabled(root, "graphi.layering", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	after, err := extpack.Load(root)
	if err != nil {
		t.Fatalf("load disabled: %v", err)
	}
	if !after.Empty() {
		t.Error("a disabled pack is still being loaded")
	}
	if got := after.Fingerprint(); got != before.Fingerprint() {
		t.Errorf("fingerprint after disable = %q, want the pre-pack %q", got, before.Fingerprint())
	}
	if len(after.ArchRules()) != 0 || len(after.Refs()) != 0 {
		t.Errorf("disabled pack still contributes: rules=%v refs=%v", after.ArchRules(), after.Refs())
	}

	// AC-8: removal needs no schema hack either.
	if err := extpack.Remove(root, "graphi.layering"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, statErr := os.Stat(extpack.PackDir(root, "graphi.layering")); !os.IsNotExist(statErr) {
		t.Errorf("remove left the pack dir behind: %v", statErr)
	}
	removed, err := extpack.Load(root)
	if err != nil || !removed.Empty() {
		t.Errorf("after remove: set=%+v err=%v", removed, err)
	}
}

// TestAC5_ProvenanceNamesIDVersionAndHashAndIsLengthBounded covers both halves
// of the provenance requirement: it is complete, and it is bounded.
func TestAC5_ProvenanceNamesIDVersionAndHashAndIsLengthBounded(t *testing.T) {
	root := t.TempDir()
	entry := installFixture(t, root, layeringPack)
	set, err := extpack.Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rules := set.ArchRules()
	if len(rules) == 0 {
		t.Fatal("no rules loaded")
	}
	for _, r := range rules {
		if r.Pack.ID != entry.ID || r.Pack.Version != entry.Version || r.Pack.SHA256 != entry.ManifestSHA256 {
			t.Errorf("rule %q provenance = %+v, want id/version/hash of %+v", r.ID, r.Pack, entry)
		}
		rendered := r.Pack.String()
		for _, want := range []string{entry.ID, entry.Version, entry.ManifestSHA256} {
			if !strings.Contains(rendered, want) {
				t.Errorf("rendered provenance %q omits %q", rendered, want)
			}
		}
	}

	long := strings.Repeat("x", extpack.MaxFieldLength*3)
	bounded := extpack.Bound(long)
	if len(bounded) != extpack.MaxFieldLength {
		t.Errorf("Bound produced %d bytes, want exactly %d", len(bounded), extpack.MaxFieldLength)
	}
	if !strings.HasSuffix(bounded, "[truncated]") {
		t.Errorf("a truncated value must say so: %q", bounded)
	}
	if got := extpack.Bound("short"); got != "short" {
		t.Errorf("Bound shortened a value that fits: %q", got)
	}
}

// TestInstallingTheSamePackTwiceIsErrDuplicate reuses SW-222's vocabulary rather
// than inventing a second word for the same failure.
func TestInstallingTheSamePackTwiceIsErrDuplicate(t *testing.T) {
	root := t.TempDir()
	installFixture(t, root, layeringPack)
	path := packPath(layeringPack)
	_, err := extpack.Install(root, path, sha256File(t, path))
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("second install = %v, want registry.ErrDuplicate", err)
	}
}

// TestDoctorDiagnosesHashMismatchOrphansAndDisabledPacks is AC-2's `doctor`
// half. Each row is produced by actually creating the condition, not by calling
// a classifier with a hand-built input.
func TestDoctorDiagnosesHashMismatchOrphansAndDisabledPacks(t *testing.T) {
	root := t.TempDir()
	installFixture(t, root, layeringPack)
	installFixture(t, root, taintPack)

	// 1. Edit an installed pack in place → hash mismatch.
	tampered := filepath.Join(extpack.PackDir(root, "acme.taint-rules"), "artifact")
	data, err := os.ReadFile(tampered)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if err := os.WriteFile(tampered, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	// 2. Disable the other one.
	if _, err := extpack.SetEnabled(root, "graphi.layering", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 3. Leave an untracked directory in the store.
	if err := os.MkdirAll(filepath.Join(extpack.Dir(root), "stray"), 0o700); err != nil {
		t.Fatalf("mkdir stray: %v", err)
	}

	rows, err := extpack.Diagnose(root)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	got := map[string]extpack.DiagnosisKind{}
	for _, r := range rows {
		got[r.ID] = r.Kind
	}
	want := map[string]extpack.DiagnosisKind{
		"acme.taint-rules": extpack.DiagnosisHashMismatch,
		"graphi.layering":  extpack.DiagnosisDisabled,
		"stray":            extpack.DiagnosisUntracked,
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("diagnosis for %q = %q, want %q (all rows: %+v)", id, got[id], kind, rows)
		}
	}

	// 4. An orphaned lockfile entry: files gone, entry left.
	if err := os.RemoveAll(extpack.PackDir(root, "acme.taint-rules")); err != nil {
		t.Fatalf("remove pack dir: %v", err)
	}
	rows, err = extpack.Diagnose(root)
	if err != nil {
		t.Fatalf("diagnose after orphaning: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == "acme.taint-rules" && r.Kind == extpack.DiagnosisOrphaned {
			found = true
		}
	}
	if !found {
		t.Errorf("an orphaned lockfile entry was not diagnosed: %+v", rows)
	}
}

// TestLockfileIsCanonicalAndReReadsIdentically pins the reviewable-artifact
// property: the lockfile is stable bytes, so a diff of it means something.
func TestLockfileIsCanonicalAndReReadsIdentically(t *testing.T) {
	root := t.TempDir()
	installFixture(t, root, taintPack)
	installFixture(t, root, layeringPack)

	raw, err := os.ReadFile(extpack.LockPath(root))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lock, err := extpack.ParseLock(raw)
	if err != nil {
		t.Fatalf("parse lock: %v", err)
	}
	again, err := lock.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(again) != string(raw) {
		t.Errorf("lockfile does not round-trip byte-identically:\n--- on disk ---\n%s\n--- re-encoded ---\n%s", raw, again)
	}
	if lock.Packs[0].ID > lock.Packs[1].ID {
		t.Errorf("lockfile entries are not sorted by id: %+v", lock.Packs)
	}
}
