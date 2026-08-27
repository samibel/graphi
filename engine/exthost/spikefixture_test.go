package exthost

// The spike's tests are package-INTERNAL on purpose.
//
// Most of what SW-231 has to prove is containment — "the process was killed",
// "nothing was spawned", "the host is still healthy" — and the evidence for that
// is the process handle, which no exported API returns and none should: a host
// that hands out its child's *os.Process has stopped owning the containment it
// advertises. Testing from inside is the cheaper honesty. (Repo convention:
// *_internal_test.go is reserved for invariants with no exported surface; here
// that is nearly the whole suite, so the whole suite sits inside.)

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/opcatalog"
)

const (
	exampleID        = "example-analyzer"
	exampleVersion   = "0.1.0"
	exampleOperation = "example_symbol_census"
)

// builtExample caches the compiled example analyzer for the whole package run.
// Compiling it once is the difference between a suite that costs one `go build`
// and one that costs fifteen.
var builtExample struct {
	once sync.Once
	path string
	err  error
}

// moduleRoot walks up to the directory holding go.mod.
func moduleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// exampleBinary compiles extensions/example-analyzer once and returns its path.
//
// A real compiled binary, not a re-exec of the test binary: the classic
// TestHelperProcess pattern spawns os.Args[0], which is precisely the shape
// refuseUnsafeBinary refuses (and precisely the shape that kernel-panicked this
// host on 2026-08-27). The spike measures a real process anyway, so building one
// is both safer and more honest.
func exampleBinary(t testing.TB) string {
	t.Helper()
	builtExample.once.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			builtExample.err = fmt.Errorf("go toolchain unavailable: %w", err)
			return
		}
		root := moduleRoot(t)
		out := filepath.Join(os.TempDir(), fmt.Sprintf("graphi-sw231-example-%d", os.Getpid()))
		cmd := exec.Command("go", "build", "-o", out, "./extensions/example-analyzer")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			builtExample.err = fmt.Errorf("build example-analyzer: %w\n%s", err, b)
			return
		}
		builtExample.path = out
	})
	if builtExample.err != nil {
		t.Skipf("%v", builtExample.err)
	}
	return builtExample.path
}

// descriptorOptions tweak the staged descriptor for a negative case.
type descriptorOptions struct {
	// Mutate edits the descriptor document before it is written.
	Mutate func(*Descriptor)
	// CorruptBinary appends a byte to the executable AFTER its hash is recorded,
	// so the descriptor pins bytes that are no longer there.
	CorruptBinary bool
	// BinaryName overrides the staged executable's file name.
	BinaryName string
}

// stageExtension copies the compiled analyzer into a fresh directory and writes
// a matching descriptor beside it. Returns the descriptor path.
func stageExtension(t testing.TB, opts descriptorOptions) string {
	t.Helper()
	src := exampleBinary(t)
	dir := t.TempDir()

	name := opts.BinaryName
	if name == "" {
		name = "example-analyzer"
	}
	payload, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read example binary: %v", err)
	}
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(binPath, payload, 0o755); err != nil {
		t.Fatalf("stage binary: %v", err)
	}
	sum := extpack.HashBytes(payload)
	if opts.CorruptBinary {
		if err := os.WriteFile(binPath, append(payload, 'X'), 0o755); err != nil {
			t.Fatalf("corrupt binary: %v", err)
		}
	}

	d := Descriptor{
		SchemaVersion: extpack.SchemaVersion,
		ID:            exampleID,
		Version:       exampleVersion,
		Kind:          KindProcessAnalyzer,
		API:           extpack.APIRange{Min: "1.0", Max: "1.0"},
		Artifact:      extpack.Artifact{Path: name, SHA256: sum},
		Capabilities:  extpack.Capabilities{Provides: []string{exampleOperation}},
		Ports:         []opcatalog.Port{opcatalog.PortGraphSearch},
		Permissions:   opcatalog.PermissionsFor([]opcatalog.Port{opcatalog.PortGraphSearch}),
		Determinism:   opcatalog.DeterminismDeterministic,
		Limits:        Limits{MaxResponseBytes: 64 << 10, TimeoutMS: 5_000},
	}
	if opts.Mutate != nil {
		opts.Mutate(&d)
	}
	path := filepath.Join(dir, "extension.yaml")
	writeDescriptor(t, path, d)
	return path
}

// writeDescriptor renders a descriptor as the YAML the loader reads.
//
// Hand-rendered rather than yaml.Marshal'd so the test fixture is the document a
// user would write, field for field — a marshalled struct would pass even if the
// field names on the wire were wrong.
func writeDescriptor(t testing.TB, path string, d Descriptor) {
	t.Helper()
	var sb strings.Builder
	fmt.Fprintf(&sb, "schema_version: %q\n", d.SchemaVersion)
	fmt.Fprintf(&sb, "id: %q\n", d.ID)
	fmt.Fprintf(&sb, "version: %q\n", d.Version)
	fmt.Fprintf(&sb, "kind: %q\n", d.Kind)
	fmt.Fprintf(&sb, "api:\n  min: %q\n  max: %q\n", d.API.Min, d.API.Max)
	fmt.Fprintf(&sb, "artifact:\n  path: %q\n  sha256: %q\n", d.Artifact.Path, d.Artifact.SHA256)
	sb.WriteString("capabilities:\n  provides:\n")
	for _, p := range d.Capabilities.Provides {
		fmt.Fprintf(&sb, "    - %q\n", p)
	}
	sb.WriteString("ports:\n")
	for _, p := range d.Ports {
		fmt.Fprintf(&sb, "  - %q\n", p)
	}
	sb.WriteString("permissions:\n")
	for _, p := range d.Permissions {
		fmt.Fprintf(&sb, "  - %q\n", p)
	}
	fmt.Fprintf(&sb, "determinism: %q\n", d.Determinism)
	fmt.Fprintf(&sb, "limits:\n  max_response_bytes: %d\n  timeout_ms: %d\n",
		d.Limits.MaxResponseBytes, d.Limits.TimeoutMS)
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
}

// searchFixture is the graph.search port's canned answer. Fixed data, so a
// determinism failure can only come from the extension.
const searchFixture = `{"matches":[` +
	`{"name":"Hello","path":"pkg/a/greet.go"},` +
	`{"name":"Helper","path":"pkg/b/util.go"},` +
	`{"name":"Held","path":"pkg/a/greet.go"},` +
	`{"name":"Helm","path":"pkg/a/chart.go"}` +
	`]}`

// searchPort answers the declared graph.search port and records what it was
// asked. It stands in for engine/search: the spike's question is whether the
// PORT DISCIPLINE works, not whether search does.
type searchPort struct {
	mu    sync.Mutex
	calls []string
}

func (s *searchPort) handle(_ context.Context, payload json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	s.calls = append(s.calls, string(payload))
	s.mu.Unlock()
	return json.RawMessage(searchFixture), nil
}

func (s *searchPort) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// tryStart activates a staged extension with the graph.search port wired,
// optionally telling the analyzer to misbehave.
//
// The fault reaches the child through Config.Env, not through a wrapper script
// or an argument: the descriptor pins a BINARY and the host spawns it with no
// arguments, so the environment is the only channel. See the example's package
// doc — that is a real limitation of this protocol, not a test convenience.
func tryStart(t testing.TB, descriptor, fault string) (*Extension, *searchPort, error) {
	t.Helper()
	port := &searchPort{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cfg := Config{
		Activated:      true,
		DescriptorPath: descriptor,
		Ports:          map[opcatalog.Port]PortHandler{opcatalog.PortGraphSearch: port.handle},
	}
	if fault != "" {
		cfg.Env = []string{"GRAPHI_SPIKE_FAULT=" + fault}
	}
	ext, err := Start(ctx, cfg)
	if err != nil {
		return nil, port, err
	}
	t.Cleanup(func() { _ = ext.Close() })
	return ext, port, nil
}

// startExample is tryStart for the cases where starting must succeed.
func startExample(t testing.TB, descriptor, fault string) (*Extension, *searchPort) {
	t.Helper()
	ext, port, err := tryStart(t, descriptor, fault)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return ext, port
}

// callCtx is the per-call context used throughout: generous, so a failure is the
// host's limit firing rather than the test's.
func callCtx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newBufReader wraps a literal stream for the frame-codec attack tests.
func newBufReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

// itoa keeps the crafted-frame fixtures readable.
func itoa(n int) string { return strconv.Itoa(n) }
