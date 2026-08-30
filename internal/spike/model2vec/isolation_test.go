package model2vec

// AC-8, mechanically, on the SW-231 spike's precedent: the spike must not be
// imported by anything outside internal/spike/, must register nothing in
// engine/embed, and must not alter shipped bytes. All three reduce to "nothing
// on the default path refers to this package", which is a `go list` away, and
// "every reference lives where deleting the spike would take it", which is a
// `git grep` away. Both live INSIDE the spike so that deleting it deletes its
// own guard.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpike_NotInShippedImportClosure: a package absent from
// `go list -deps ./cmd/graphi` contributes no byte to the linked binary, so the
// shipped build is byte-identical by construction.
func TestSpike_NotInShippedImportClosure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/samibel/graphi/cmd/graphi").Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/graphi: %v", err)
	}
	sawControl := false
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/samibel/graphi/engine/embed" {
			sawControl = true
		}
		if strings.HasPrefix(dep, "github.com/samibel/graphi/internal/spike/") {
			t.Errorf("the shipped binary imports %s. SW-259 is a throwaway spike; the production embedder is SW-262's, gated on this spike's GO record.", dep)
		}
	}
	if !sawControl {
		t.Fatal("go list -deps did not list engine/embed — the scan is broken, not the binary")
	}
}

// TestSpike_ConfinedToItsDirectory: every file that names the spike package
// path lives under internal/spike/ or is the decision record.
func TestSpike_ConfinedToItsDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not inside a git checkout: %v", err)
	}
	cmd := exec.Command("git", "grep", "-l", "--untracked", "-e", "internal/spike", "--", ".")
	cmd.Dir = strings.TrimSpace(string(root))
	out, _ := cmd.Output() // exit 1 = no matches
	allowedPrefixes := []string{"internal/spike/"}
	allowedFiles := map[string]bool{"docs/rc/model2vec-spike.md": true}
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		file = strings.TrimSpace(filepath.ToSlash(file))
		if file == "" {
			continue
		}
		ok := allowedFiles[file]
		for _, p := range allowedPrefixes {
			if strings.HasPrefix(file, p) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s refers to internal/spike; the spike must be removable by `rm -r internal/spike` plus its decision record", file)
		}
	}
}
