package jvmcorpus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmcorpus"
	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// The real-pin run is DISPATCH-ONLY and opt-in through two environment
// variables. It clones nothing itself: a hermetic test that clones is not
// hermetic, and the PR gate must never pay for a JDK, a kotlinc and three
// checkouts. The jvm-corpus workflow prepares both directories and sets both
// variables; locally, an operator does the same by hand.
//
//	GRAPHI_JVM_CORPUS_PINS  a directory holding one checkout per pin, each at
//	                        the manifest's sha, named after the manifest entry
//	                        (guava, okio, kotlinx.serialization)
//	GRAPHI_JVM_CORPUS_LIB   a directory holding the pinned classpath artifacts,
//	                        each under the base name of its manifest URL
const (
	envPins = "GRAPHI_JVM_CORPUS_PINS"
	envLib  = "GRAPHI_JVM_CORPUS_LIB"
)

// TestPins_CompileReproduciblyAndScoreSoundly is the SW-173 evidence run.
//
// For every JVM pin whose strategy compiles, it does the whole thing TWICE from
// scratch — stage, compile, disassemble through the completeness gate — and
// asserts the two runs produce a byte-identical capture digest (AC-5). Then it
// runs the signature-aware oracle over the capture and reports
// counterexamples-and-denominator, never a bare "0 counterexamples" (AC-6).
//
// The two runs are deliberately full repeats rather than a re-read of one
// output: AC-5 asks whether the STRATEGY is reproducible, and re-hashing the
// same bytes would answer a different and much easier question.
func TestPins_CompileReproduciblyAndScoreSoundly(t *testing.T) {
	pinsDir := os.Getenv(envPins)
	if pinsDir == "" {
		t.Skipf("%s unset; the real-pin run is dispatch-only (see the jvm-corpus workflow)", envPins)
	}
	libDir := os.Getenv(envLib)

	m, err := jvmcorpus.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, pin := range m.JVMPins() {
		pin := pin
		t.Run(pin.Name, func(t *testing.T) {
			s := pin.Compile
			t.Logf("STRATEGY %s — toolchain %s", s.Strategy, s.ToolchainLine())
			t.Logf("REASON: %s", s.Reason)
			if s.ExcludedFromCorpusScale {
				// Recorded, and the run still proves what it can: the exclusion
				// claim itself is checked below by compiling the pin.
				t.Logf("EXCLUDED FROM THE CORPUS-SCALE CLAIM: %s", s.ExclusionReason)
			}
			if s.Strategy == jvmcorpus.StrategyNotCompiled {
				t.Logf("NEGATIVE RESULT — not compiled. Tried: %s", strings.Join(s.Tried, "; "))
				return
			}

			pinRoot := filepath.Join(pinsDir, pin.Name)
			if _, err := os.Stat(pinRoot); err != nil {
				t.Skipf("checkout %s absent: %v", pinRoot, err)
			}
			compiler, err := exec.LookPath(s.Compiler)
			if err != nil {
				t.Skipf("%s unavailable: %v", s.Compiler, err)
			}
			javap, err := exec.LookPath("javap")
			if err != nil {
				t.Skip("javap unavailable")
			}

			var classpath, plugins []string
			if len(s.Classpath) > 0 {
				if libDir == "" {
					t.Skipf("%s unset but pin %q has %d pinned artifacts", envLib, pin.Name, len(s.Classpath))
				}
				// Fail-closed digest verification, before any compile.
				classpath, plugins, err = jvmcorpus.VerifyArtifacts(libDir, s)
				if err != nil {
					t.Fatalf("pinned classpath: %v", err)
				}
				t.Logf("artifacts sha256-verified: %d classpath, %d compiler-plugin", len(classpath), len(plugins))
			}

			filesAtPin := countPinSources(t, pinRoot)

			// --- two independent runs (AC-5) --------------------------------
			var digests []string
			var lastCapture *jvmgroundtruth.Capture
			var lastStage jvmcorpus.StageReport
			var lastStaged string
			for run := 1; run <= 2; run++ {
				staged := t.TempDir()
				rep, err := jvmcorpus.Stage(pinRoot, staged, s, filesAtPin)
				if err != nil {
					t.Fatalf("run %d stage: %v", run, err)
				}
				out := filepath.Join(t.TempDir(), "classes")
				args, cerr := jvmcorpus.Compile(compiler, staged, out, s, rep, classpath, plugins)
				if cerr != nil {
					// A compile failure under full-dependency-resolution means
					// the strategy is wrong for this pin — say so plainly rather
					// than degrading to a smaller green.
					t.Fatalf("run %d compile FAILED under strategy %q.\nargs: %s\n%v",
						run, s.Strategy, strings.Join(args[:min(len(args), 12)], " "), cerr)
				}
				classes, err := jvmcorpus.CompiledClasses(out)
				if err != nil {
					t.Fatalf("run %d enumerate classes: %v", run, err)
				}
				if len(classes) == 0 {
					t.Fatalf("run %d produced NO classes — a green here would be vacuous", run)
				}
				capt, cres, err := jvmcorpus.Capture(javap, out, classes)
				if err != nil {
					t.Fatalf("run %d capture REFUSED by the completeness gate: %v", run, err)
				}
				t.Logf("run %d: staged %d/%d files (pin holds %d), %d classes, %d javap shards, capture %d bytes, digest %s",
					run, rep.FilesStaged, rep.FilesOffered, rep.FilesAtPin,
					cres.ClassesOnDisk, cres.Shards, cres.Bytes, cres.Digest)
				if cres.ClassesCaptured != cres.ClassesOnDisk {
					t.Fatalf("run %d: gate passed but captured %d of %d classes", run, cres.ClassesCaptured, cres.ClassesOnDisk)
				}
				digests = append(digests, cres.Digest)
				lastCapture, lastStage, lastStaged = capt, rep, staged
			}

			if digests[0] != digests[1] {
				// AC-5's IF branch: publish the non-reproducibility as a finding
				// and do not use the pin for evidence.
				t.Fatalf("NOT REPRODUCIBLE — two runs at the same pin with the same pinned toolchain "+
					"produced different oracle inputs:\n  run 1 %s\n  run 2 %s\n"+
					"This is a finding to publish and explain, never a flake to retry away; "+
					"the pin must not back an evidence row until it is resolved.", digests[0], digests[1])
			}
			t.Logf("REPRODUCIBLE: two independent runs, byte-identical capture digest %s", digests[0])

			if s.ExcludedFromCorpusScale {
				t.Logf("scoring SKIPPED for this pin by its recorded exclusion; the compile above is what it proves")
				return
			}

			// --- the oracle, signature-aware (AC-6) -------------------------
			truth, err := lastCapture.Calls()
			if err != nil {
				t.Fatalf("parse capture: %v", err)
			}
			declared, err := lastCapture.Declared()
			if err != nil {
				t.Fatalf("declared methods: %v", err)
			}
			src, err := jvmcorpus.SourceBytes(lastStaged, lastStage.Staged)
			if err != nil {
				t.Fatalf("read staged sources: %v", err)
			}
			confirmed := declared.Verify(jvmgroundtruth.BinderCalls(src))
			t.Logf("binder confirmed %d call(s) over %d staged source files", len(confirmed), lastStage.FilesStaged)

			violations := 0
			for _, p := range []jvmgroundtruth.Precision{
				jvmgroundtruth.ByName, jvmgroundtruth.ByArity, jvmgroundtruth.BySignature,
			} {
				res := jvmgroundtruth.CompareAt(confirmed, truth, p)
				// counterexamples-and-denominator, never a bare "0 counterexamples"
				t.Logf("[%s] %s", p, strings.TrimSpace(res.Format()))
				if !res.Sound() {
					violations++
				}
			}
			if violations > 0 {
				t.Fatalf("SOUNDNESS FAILURE on pin %q at %d precision(s) — each is a JVMSOUND-0xx stop-ship. "+
					"Before reading it as a product defect, rule out a harness-side path or capture "+
					"difference: this is the first run of the oracle at corpus scale.", pin.Name, violations)
			}
		})
	}
}

// countPinSources is the outer denominator: every JVM source tracked at the
// pin, whatever target it belongs to. Taken from git rather than from a walk so
// it counts the PIN and not the working tree.
func countPinSources(t *testing.T, pinRoot string) int {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "*.java", "*.kt")
	cmd.Dir = pinRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v", pinRoot, err)
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
