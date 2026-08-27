package exthost

// AC-4 and AC-6, mechanically.
//
// AC-4 requires the default build and default runtime behaviour to be
// byte-unchanged when the spike is not activated. AC-6 requires the spike to be
// removable without trace if the decision is no-go. Both reduce to one checkable
// property — NOTHING ON THE DEFAULT PATH REFERS TO THIS PACKAGE — and it is
// checked here rather than asserted in a document, because a document cannot
// notice the import somebody adds next month.
//
// The shape is cmd/graphi/binary_weight_test.go's, deliberately: that test exists
// because a 3.5 MB binary regression reached CI through a gate no local suite
// measured, and its lesson was to assert the CAUSE (a named import) locally
// rather than the symptom (a size) remotely. Same here: "engine/exthost is not
// in the shipped import closure" is a `go list` away, and it is the fact both
// acceptance criteria actually rest on.
//
// This test lives INSIDE the spike, not in cmd/graphi. That is the point:
// deleting engine/exthost deletes its own guard, leaving no orphaned assertion
// about a package that no longer exists.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	spikeHostPackage = "github.com/samibel/graphi/engine/exthost"
	spikeExamplePkg  = "github.com/samibel/graphi/extensions/example-analyzer"
)

// TestSW231_AC4_SpikeIsNotInTheShippedImportClosure is the byte-unchanged proof.
//
// A package absent from `go list -deps ./cmd/graphi` contributes nothing to the
// linked binary — not a byte of code, not a symbol, not an init. So the default
// build is byte-identical to the pre-spike build BY CONSTRUCTION, and the
// measured comparison in the decision document confirms rather than establishes
// it.
func TestSW231_AC4_SpikeIsNotInTheShippedImportClosure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/samibel/graphi/cmd/graphi").Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/graphi: %v", err)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		switch strings.TrimSpace(dep) {
		case spikeHostPackage:
			t.Errorf("the shipped binary imports %s.\n\n"+
				"SW-231 is a disposable spike (ADR 0013 D2: \"no-go, graphi stays on rule packs and "+
				"static modules\" is a planned outcome). AC-4 requires the default build to be "+
				"byte-unchanged when the spike is not activated and AC-6 requires it to be removable "+
				"without trace; a default-path import breaks both at once. If tier C is being shipped "+
				"for real, that is a new ADR and this test is the wrong thing to delete first.",
				spikeHostPackage)
		case spikeExamplePkg:
			t.Errorf("the shipped binary imports %s — the example extension is a SEPARATE "+
				"executable and must never be linked into graphi", spikeExamplePkg)
		}
	}
}

// TestSW231_AC6_SpikeIsConfinedToItsOwnDirectories is the removability proof.
//
// `rm -r engine/exthost extensions/example-analyzer` plus the decision document
// must leave a tree that still builds. That holds only while no file OUTSIDE
// those directories names the package, so the test enumerates every reference in
// the repository and allows exactly the ones deletion would also remove.
func TestSW231_AC6_SpikeIsConfinedToItsOwnDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := moduleRoot(t)
	cmd := exec.Command("git", "grep", "-l", "-e", "engine/exthost", "-e", "example-analyzer", "--", ".")
	cmd.Dir = root
	out, _ := cmd.Output() // exit status 1 means "no matches", which is not an error here.

	allowedPrefixes := []string{
		"engine/exthost/",
		"extensions/example-analyzer/",
		// The decision document. It is the story's deliverable and is removed
		// with the spike only if the spike is removed; a no-go record that
		// mentions what it declined is not a dependency.
		"docs/decisions/",
	}
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		file = strings.TrimSpace(filepath.ToSlash(file))
		if file == "" {
			continue
		}
		allowed := false
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(file, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("%s refers to the SW-231 spike. AC-6 requires the spike to be removable without "+
				"trace: every reference must live under a directory that `rm -r` would take with it. "+
				"Allowed roots: %v", file, allowedPrefixes)
		}
	}
}
