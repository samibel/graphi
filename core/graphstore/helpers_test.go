package graphstore_test

import "os"

// readFileHelper / writeRaw are thin os wrappers kept in one place so the test
// files read cleanly.
func readFileHelper(p string) ([]byte, error) { return os.ReadFile(p) } //nolint:gosec // test fixture path

func writeRaw(t interface{ Fatalf(string, ...any) }, p string, content []byte) {
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// makeSymlink creates a symlink at linkPath pointing to target.
func makeSymlink(target, linkPath string) error { return os.Symlink(target, linkPath) }
