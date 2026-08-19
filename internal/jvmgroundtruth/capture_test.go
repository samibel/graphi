package jvmgroundtruth_test

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// captureFixture is the three-class shape SW-172's reviewer used to demonstrate
// the incomplete-capture forge: App calls seed() through an INTERFACE-typed
// parameter, so the invoke's symbolic owner is a.Iface. Omit a.Iface from the
// capture and resolveOwner's `!known` branch reads it as an EXTERNAL owner, the
// truth fact loses its source path, and graphi's correct confirmed call is
// accused with no abstention and no counter.
var captureFixture = map[string]string{
	"a/Iface.java": `package a;
public interface Iface {
    int seed();
}
`,
	"a/Impl.java": `package a;
public class Impl implements Iface {
    public int seed() { return 3; }
}
`,
	"a/App.java": `package a;
public class App {
    public int run(Iface i) { return i.seed(); }
}
`,
}

// TestIncompleteCapture_ForgesWithoutTheGate_RefusedWithIt is the SW-173
// anti-forge proof, and it is deliberately structured as red-without-fix:
//
//	(1) the WHOLE capture scores sound, matched=1 — the correct answer;
//	(2) the same capture with ONE class omitted, fed straight to ParseJavap the
//	    way a naive sharded driver would, FORGES a soundness violation at every
//	    precision against that same correct code;
//	(3) NewCapture REFUSES that omission, naming the missing class;
//	(4) a properly sharded capture — split across two javap execs, nothing
//	    omitted — passes the gate and yields facts identical to (1).
//
// (2) is the part that matters. Without it this test would only show a gate
// accepting good input, which proves nothing about what the gate prevents.
func TestIncompleteCapture_ForgesWithoutTheGate_RefusedWithIt(t *testing.T) {
	javac, javap := toolchain(t)

	root := writeFixture(t, captureFixture)
	out := compile(t, javac, root, captureFixture)

	classes := compiledClasses(t, out)

	// The confirmed side is the BINDER's own decisions, verified against javac's
	// declared-method table — the stage-2 path, because the graph store cannot
	// carry arity and so abstains at the two finer precisions by construction.
	// The forge must be shown at every precision, which needs this side.
	_, declared := disassembleWithDeclared(t, javap, out)
	confirmed := declared.Verify(jvmgroundtruth.BinderCalls(sourceBytes(captureFixture)))
	if len(confirmed) == 0 {
		t.Fatal("graphi confirmed nothing on this fixture — the forge cannot be demonstrated against an empty confirmed set")
	}

	// (1) The WHOLE capture: the correct answer, at every precision.
	whole := javapOver(t, javap, out, classes)
	wholeTruth, err := jvmgroundtruth.ParseJavap(whole)
	if err != nil {
		t.Fatalf("ParseJavap(whole): %v", err)
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, wholeTruth, p)
		if !res.Sound() {
			t.Fatalf("the WHOLE capture must be sound at %s; got:\n%s", p, res.Format())
		}
		if res.Matched == 0 {
			t.Fatalf("the WHOLE capture judged nothing at %s — a vacuous baseline:\n%s", p, res.Format())
		}
	}

	// (2) THE FORGE. Omit exactly one class and score it anyway.
	const omitted = "a.Iface"
	kept := without(classes, omitted)
	if len(kept) != len(classes)-1 {
		t.Fatalf("fixture drift: %q is not among the compiled classes %v", omitted, classes)
	}
	short := javapOver(t, javap, out, kept)
	shortTruth, err := jvmgroundtruth.ParseJavap(short)
	if err != nil {
		t.Fatalf("ParseJavap(short): %v", err)
	}
	forged := 0
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, shortTruth, p)
		if !res.Sound() {
			forged++
			t.Logf("FORGE reproduced at %s (this is the defect, not a failure):\n%s", p, strings.TrimSpace(res.Format()))
		}
	}
	if forged == 0 {
		t.Fatal("the incomplete capture did NOT forge a violation — this test no longer demonstrates the hazard it gates, " +
			"so the gate below is unproven. Do not delete this assertion; find out what changed.")
	}

	// (3) The gate REFUSES that capture, and says which class is missing.
	if _, err := jvmgroundtruth.NewCapture([][]byte{short}, classes); err == nil {
		t.Fatal("NewCapture accepted a capture missing a required class — the forge is wide open")
	} else {
		var inc *jvmgroundtruth.IncompleteCaptureError
		if !errors.As(err, &inc) {
			t.Fatalf("NewCapture must refuse with *IncompleteCaptureError, got %T: %v", err, err)
		}
		if want := "a/Iface"; !contains(inc.Missing, want) {
			t.Fatalf("IncompleteCaptureError must name the missing class %q; got %v", want, inc.Missing)
		}
		if !strings.Contains(err.Error(), "forge") {
			t.Fatalf("the error must say what refusing prevents; got %q", err.Error())
		}
	}

	// (4) A PROPERLY sharded capture: two execs, nothing omitted, gate passes,
	// and the facts are identical to the whole-capture baseline.
	batches := jvmgroundtruth.ShardClasses(classes, 8) // deliberately tiny, to force >1 shard
	if len(batches) < 2 {
		t.Fatalf("expected the fixture to shard into at least 2 batches, got %d", len(batches))
	}
	var shards [][]byte
	for _, b := range batches {
		shards = append(shards, javapOver(t, javap, out, b))
	}
	cap, err := jvmgroundtruth.NewCapture(shards, classes)
	if err != nil {
		t.Fatalf("NewCapture over complete shards must succeed: %v", err)
	}
	shardedTruth, err := cap.Calls()
	if err != nil {
		t.Fatalf("Capture.Calls: %v", err)
	}
	if !reflect.DeepEqual(wholeTruth, shardedTruth) {
		t.Fatalf("sharding changed the truth set.\nwhole:   %+v\nsharded: %+v", wholeTruth, shardedTruth)
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, shardedTruth, p)
		if !res.Sound() {
			t.Fatalf("the SHARDED-but-complete capture must be sound at %s; got:\n%s", p, res.Format())
		}
	}
}

// TestCapture_DigestIsStableAcrossRuns is the AC-5 primitive: the digest the
// reproducibility check compares must be a pure function of the capture bytes,
// and sharding must not perturb it. Pure unit level — no JDK needed.
func TestCapture_DigestIsStableAcrossRuns(t *testing.T) {
	shards := [][]byte{
		[]byte("Compiled from \"A.java\"\npublic class a.A {\n}\n"),
		[]byte("Compiled from \"B.java\"\npublic class a.B {\n}\n"),
	}
	required := []string{"a.A", "a.B"}

	first, err := jvmgroundtruth.NewCapture(shards, required)
	if err != nil {
		t.Fatalf("NewCapture: %v", err)
	}
	second, err := jvmgroundtruth.NewCapture(shards, required)
	if err != nil {
		t.Fatalf("NewCapture: %v", err)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("identical input yielded different digests: %s vs %s", first.Digest(), second.Digest())
	}
	// A capture that differs by one byte must differ by digest, or the AC-5
	// comparison is decorative.
	altered := [][]byte{shards[0], []byte("Compiled from \"B.java\"\npublic class a.C {\n}\n")}
	third, err := jvmgroundtruth.NewCapture(altered, []string{"a.A", "a.C"})
	if err != nil {
		t.Fatalf("NewCapture: %v", err)
	}
	if third.Digest() == first.Digest() {
		t.Fatal("different captures produced the same digest")
	}
	if got := first.Classes(); !reflect.DeepEqual(got, []string{"a/A", "a/B"}) {
		t.Fatalf("Classes() = %v, want [a/A a/B]", got)
	}
}

// TestCapture_MissingSetIsDerivedFromDisk pins the rule that makes the gate
// mean anything: the required set must come from the compiler's output
// directory, never from the capture. Here the same omission is checked BOTH
// ways — against the real required set (refused) and against a set derived from
// the capture itself (trivially satisfied) — so the self-satisfying variant is
// demonstrated to be useless rather than merely warned against in a comment.
func TestCapture_MissingSetIsDerivedFromDisk(t *testing.T) {
	full := [][]byte{
		[]byte("Compiled from \"A.java\"\npublic class a.A {\n}\n"),
		[]byte("Compiled from \"B.java\"\npublic class a.B {\n}\n"),
	}
	onDisk := []string{"a.A", "a.B"}

	short := full[:1] // a.B never captured

	if _, err := jvmgroundtruth.NewCapture(short, onDisk); err == nil {
		t.Fatal("a capture missing a class present on disk must be refused")
	}
	// The self-satisfying variant: required derived from what the capture
	// happens to contain. It passes exactly when it should fail.
	selfDerived := []string{"a.A"}
	if _, err := jvmgroundtruth.NewCapture(short, selfDerived); err != nil {
		t.Fatalf("the self-derived check is expected to pass vacuously (that is the point): %v", err)
	}
}

// TestShardClasses_DeterministicAndTotal pins the two properties the sharded
// path rests on: every input class lands in exactly one batch (nothing is lost
// in the split, which would defeat the gate by construction), and the batching
// does not depend on input order.
func TestShardClasses_DeterministicAndTotal(t *testing.T) {
	in := []string{"a.Zeta", "a.Alpha", "a.Mu", "a.Beta", "a.Omega"}
	shuffled := []string{"a.Mu", "a.Omega", "a.Alpha", "a.Zeta", "a.Beta"}

	got := jvmgroundtruth.ShardClasses(in, 16)
	again := jvmgroundtruth.ShardClasses(shuffled, 16)
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("shardings differ by input order:\n%v\n%v", got, again)
	}
	if len(got) < 2 {
		t.Fatalf("expected the budget to force multiple batches, got %v", got)
	}

	var flat []string
	for _, b := range got {
		flat = append(flat, b...)
	}
	if len(flat) != len(in) {
		t.Fatalf("sharding lost or duplicated classes: %d in, %d out (%v)", len(in), len(flat), flat)
	}
	for _, c := range in {
		if !contains(flat, c) {
			t.Fatalf("class %q vanished from the sharding: %v", c, got)
		}
	}

	// An over-long name must still get a batch rather than being dropped.
	long := jvmgroundtruth.ShardClasses([]string{strings.Repeat("x", 100)}, 4)
	if len(long) != 1 || len(long[0]) != 1 {
		t.Fatalf("an over-long class name must survive sharding, got %v", long)
	}
}

// --- helpers ---------------------------------------------------------------

func javapOver(t *testing.T, javap, out string, classes []string) []byte {
	t.Helper()
	b, err := exec.Command(javap, append([]string{"-c", "-p", "-s", "-classpath", out}, classes...)...).Output()
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	return b
}

func without(all []string, drop string) []string {
	var kept []string
	for _, c := range all {
		if c != drop {
			kept = append(kept, c)
		}
	}
	return kept
}

func contains(all []string, want string) bool {
	for _, c := range all {
		if c == want {
			return true
		}
	}
	return false
}
