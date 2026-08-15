package jvmgroundtruth_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/semantic"
	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// TestGroundTruth_Java_LiveJDK is the WP-J9 soundness gate, run END TO END with
// a real JDK: it builds a graphi graph with the JVM binder live, compiles the
// SAME sources with javac, extracts the bytecode call facts via javap, and
// asserts every confirmed graphi call is backed by bytecode (zero tolerance).
// It skips when javac/javap are absent — CI installs them; the sandbox already
// has them, so this runs here. Kotlin ground truth needs kotlinc and is the CI
// workflow's job; this hermetic test is Java-only.
//
// This is the first proof that the binder is SOUND on real bytecode, not just
// self-consistent: graphi and javac are independent implementations of the
// same static-binding contract (ADR 0008 D1), and this asserts graphi never
// claims a call javac's bytecode does not make.
func TestGroundTruth_Java_LiveJDK(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable; the jvm-groundtruth CI workflow installs it")
	}
	javap, err := exec.LookPath("javap")
	if err != nil {
		t.Skip("javap unavailable")
	}

	// The fixture deliberately includes an AMBIGUOUS overload (apply(int) /
	// apply(String)): graphi DROPS the r.apply(1) call (D6), so confirmed is a
	// strict subset of the bytecode — the most instructive soundness case,
	// where the conservative drop still leaves graphi ⊆ truth.
	files := map[string]string{
		"tax/Rate.java": `package tax;
public class Rate {
    public int rate() { return 7; }
    public int scaled(Rate other) { return other.rate(); }
    public int apply(int x) { return x; }
    public int apply(String s) { return 0; }
}
`,
		"shop/Cart.java": `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.rate() + r.apply(1); }
}
`,
	}

	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// (1) graphi confirmed calls, binder live.
	confirmed := confirmedJavaCalls(t, root)

	// (2) bytecode truth via javac + javap.
	truth := bytecodeTruth(t, javac, javap, root, files)

	// (3) the zero-tolerance soundness verdict + measured recall.
	res := jvmgroundtruth.Compare(confirmed, truth)
	t.Log(strings.TrimSpace(res.Format()))
	if !res.Sound() {
		t.Fatalf("SOUNDNESS FAILURE:\n%s\nconfirmed: %+v\ntruth: %+v", res.Format(), confirmed, truth)
	}

	// The two provable calls (checkout→rate, scaled→rate) must be confirmed;
	// the ambiguous checkout→apply must be DROPPED (present in bytecode,
	// absent from confirmed) — so recall is strictly below 100% and that is
	// the honest, sound outcome.
	if got := len(confirmed); got != 2 {
		t.Fatalf("expected exactly 2 confirmed calls (checkout→rate, scaled→rate), got %d: %+v", got, confirmed)
	}
	if res.TruthIntra <= res.Matched {
		t.Fatalf("the ambiguous overload must leave a recall gap (truth %d > matched %d)", res.TruthIntra, res.Matched)
	}
}

// confirmedJavaCalls builds the graph with the binder live and projects its
// confirmed calls edges.
func confirmedJavaCalls(t *testing.T, root string) []jvmgroundtruth.Call {
	t.Helper()
	t.Setenv(semantic.EnvJVM, "1")
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	return jvmgroundtruth.ConfirmedCalls(nodes, edges)
}

// bytecodeTruth compiles the fixture with javac -g and disassembles every
// class with javap -c -p, then parses the call facts.
func bytecodeTruth(t *testing.T, javac, javap, root string, files map[string]string) []jvmgroundtruth.Call {
	t.Helper()
	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	var srcs []string
	for rel := range files {
		srcs = append(srcs, filepath.Join(root, filepath.FromSlash(rel)))
	}
	compile := exec.Command(javac, append([]string{"-g", "-d", out}, srcs...)...)
	if b, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}

	// Enumerate compiled classes (dir-relative path → dotted class name).
	var classes []string
	err := filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".class") {
			return err
		}
		rel, rerr := filepath.Rel(out, p)
		if rerr != nil {
			return rerr
		}
		cls := strings.TrimSuffix(filepath.ToSlash(rel), ".class")
		classes = append(classes, strings.ReplaceAll(cls, "/", "."))
		return nil
	})
	if err != nil {
		t.Fatalf("walk classes: %v", err)
	}
	if len(classes) == 0 {
		t.Fatal("javac produced no classes")
	}

	disasm := exec.Command(javap, append([]string{"-c", "-p", "-classpath", out}, classes...)...)
	b, err := disasm.Output()
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	truth, err := jvmgroundtruth.ParseJavap(b)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	return truth
}
