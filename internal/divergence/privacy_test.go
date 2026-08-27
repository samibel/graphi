package divergence

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// SW-232 AC-1 / AC-7: writing the record performs ZERO network egress.
//
// The repository-wide guarantee is enforced by internal/canary's static
// zero-telemetry gate over the default build graph, which now includes this
// package (cmd/graphi wires it). This test is the local, always-on half: the
// writer may not acquire a network import at all, so the egress question never
// depends on whether a reviewer noticed. Observability does not get to be the
// exception to the zero-egress posture.
func TestWriterImportsNoNetwork(t *testing.T) {
	forbidden := []string{"net", "net/http", "net/url", "os/exec", "log/syslog"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, imp.Path.Value)
			}
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %q — the divergence record is local file I/O only", name, path)
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no source files scanned; the import guard is vacuous")
	}
}

// The segments hold graphi's own operation ids and bounded renderings, and they
// are written owner-only: the state directory is private state, not a shared
// artifact.
func TestSegmentsAreWrittenOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("dead_code", false, "", "", "")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat segment: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("segment mode = %04o, want 0600", perm)
	}
	dirInfo, err := os.Stat(Dir(dir))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("segment directory mode = %04o, want 0700", perm)
	}
}

// A store that never observes anything leaves NOTHING on disk — a diagnostic
// that creates state just by existing would be a different kind of dishonesty
// (and `graphi doctor` is documented read-only).
func TestUnusedStoreCreatesNoState(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewStore(dir); err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := os.Stat(Dir(dir)); !os.IsNotExist(err) {
		t.Fatalf("an unused store created %s (err=%v)", Dir(dir), err)
	}
}
