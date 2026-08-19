package jvmbindrate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmbindrate"
)

// The real-pin run is DISPATCH-ONLY and opt-in through the same environment
// variable SW-173's compile run uses, for the same reason: a hermetic test that
// clones is not hermetic, and the PR gate must not pay for three checkouts.
//
//	GRAPHI_JVM_CORPUS_PINS  a directory holding one checkout per pin, each at
//	                        the manifest's sha, named after the manifest entry
//	                        (guava, okio, kotlinx.serialization)
//
// Unlike SW-173's run this one needs NO compiler and NO classpath: the binding
// rate is a source-to-binder measurement and never touches bytecode. That is
// why its pin coverage is the whole pin rather than the compiled subset, and
// the published record says so explicitly.
const envPins = "GRAPHI_JVM_CORPUS_PINS"

// pins are the three JVM corpus pins with the sha corpus/manifest.json records.
// The sha is asserted at run time: a measurement published against "guava" that
// silently ran against a different checkout is worse than no measurement.
//
// Each pin is measured at more than one SCOPE, and the scopes are not a
// convenience. Two of these pins are monorepos that publish the SAME fully
// qualified type twice — guava ships every `com.google.common.*` class in both
// `guava/src` (the JRE flavour) and `android/guava/src` (the Android flavour) —
// and the binder's tabledType helper binds a FQN only when it has EXACTLY ONE
// candidate. Measuring only the whole checkout would publish a rate that is
// mostly a statement about monorepo layout; measuring only one module would
// publish a rate no user pointing graphi at the repository would ever see.
// Both are measured and both are published, with the collision count beside
// them so the reader can see which effect they are looking at.
var pins = []struct {
	name, sha string
	scopes    []scope
}{
	{"guava", "2214c63670fc161da170ac6e1a2d6d07e1531a55", []scope{
		{"whole-pin", ""},
		{"guava/src (JRE flavour only)", "guava/src/"},
		{"android/guava/src (Android flavour only)", "android/guava/src/"},
	}},
	{"okio", "8b870e8eaacecb1c1ceffbbb47246112604a1f92", []scope{
		{"whole-pin", ""},
		{"okio/ (the core module)", "okio/"},
	}},
	{"kotlinx.serialization", "3efe324be422ead21ca44f2f6318e1791c166556", []scope{
		{"whole-pin", ""},
		{"core/ (the core module)", "core/"},
	}},
}

// scope names a path prefix to restrict the measurement to. An empty prefix is
// the whole checkout.
type scope struct{ name, prefix string }

// TestPins_BindingRateIsReproducible is the SW-175 evidence run.
//
// For every pin it reads every .java and .kt source at the pin, runs the
// measurement TWICE from scratch, and asserts the two runs agree on every
// figure (AC-5) — a full repeat, not a re-read of one result, because AC-5 asks
// whether the MEASUREMENT is reproducible and re-printing the same struct would
// answer a much easier question. It then prints the rate, its numerator, its
// denominator, every named exclusion with its size, and the full skip
// histogram, in the form the published record carries.
func TestPins_BindingRateIsReproducible(t *testing.T) {
	pinsDir := os.Getenv(envPins)
	if pinsDir == "" {
		t.Skipf("%s unset; the real-pin run is dispatch-only", envPins)
	}
	for _, pin := range pins {
		pin := pin
		t.Run(pin.name, func(t *testing.T) {
			root := filepath.Join(pinsDir, pin.name)
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("pin checkout missing: %v", err)
			}
			assertSHA(t, root, pin.sha)

			all, err := readSources(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) == 0 {
				t.Fatal("no JVM sources found at the pin — a vacuous measurement")
			}

			for _, sc := range pin.scopes {
				files := all
				if sc.prefix != "" {
					files = map[string][]byte{}
					for p, b := range all {
						if strings.HasPrefix(p, sc.prefix) {
							files[p] = b
						}
					}
					if len(files) == 0 {
						t.Fatalf("scope %q matched no files — the scope is stale, not empty", sc.name)
					}
				}

				runA := jvmbindrate.Measure(files)
				runB := jvmbindrate.Measure(files)

				for _, lang := range []string{"java", "kotlin"} {
					a, b := runA[lang], runB[lang]
					if render(a) != render(b) {
						t.Fatalf("NON-REPRODUCIBLE at %s / %s / %s — publish this as a finding, never retry it away:\nrun A:\n%s\nrun B:\n%s",
							pin.name, sc.name, lang, render(a), render(b))
					}
					if a.SourceFiles == 0 {
						continue
					}
					t.Logf("PIN %s — SCOPE %s\n%s", pin.name, sc.name, render(a))
				}
			}
		})
	}
}

func assertSHA(t *testing.T, root, want string) {
	t.Helper()
	head, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("cannot read pin HEAD: %v", err)
	}
	got := strings.TrimSpace(string(head))
	if strings.HasPrefix(got, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(got, "ref:"))
		b, err := os.ReadFile(filepath.Join(root, ".git", filepath.FromSlash(ref)))
		if err != nil {
			t.Fatalf("cannot resolve pin ref %q: %v", ref, err)
		}
		got = strings.TrimSpace(string(b))
	}
	if got != want {
		t.Fatalf("pin checkout is at %s, manifest pins %s — the figures would not be about the pin", got, want)
	}
}

// readSources reads every .java and .kt file under root, keyed by its
// repository-relative slash path (the same key shape the ingest pass uses).
// .git is skipped; nothing else is filtered, because filtering is how a
// denominator quietly shrinks.
func readSources(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".java" && ext != ".kt" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	return out, err
}

// render is the canonical text form of a report: it is what the two runs are
// compared on, so a difference in ANY published figure fails AC-5 rather than
// only a difference in the headline rate.
func render(r jvmbindrate.LanguageReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LANG %s\n", r.Language)
	fmt.Fprintf(&b, "  source files %d, tabled by the binder %d, table-skipped %d\n",
		r.SourceFiles, r.TabledFiles, len(r.SkippedFiles))
	fmt.Fprintf(&b, "  tabled types %d, of which in a COLLIDED FQN %d (across %d colliding FQNs)\n",
		r.TabledTypes, r.TypesInCollidedFQNs, r.CollidedFQNs)
	fmt.Fprintf(&b, "  RATE %.2f%% = %d bound call sites / %d CST call sites\n",
		r.Rate(), r.BoundCallSites, r.CST.Denominator())
	fmt.Fprintf(&b, "  widest denominator %.2f%% = %d / %d (constructor delegation added back)\n",
		r.WidestRate(), r.BoundCallSites, r.CST.WidestDenominator())
	fmt.Fprintf(&b, "  denominator parts: method_invocation %d, object_creation %d, call_suffix %d\n",
		r.CST.MethodInvocation, r.CST.ObjectCreation, r.CST.CallSuffix)
	fmt.Fprintf(&b, "  EXCLUDED-but-a-call: explicit_ctor %d, ctor_delegation_call %d, superclass_delegation %d, enum_constant %d, enum_entry %d, infix %d, indexing %d\n",
		r.CST.ExplicitCtorInvocation, r.CST.CtorDelegationCall, r.CST.DelegationCtorCall,
		r.CST.EnumConstantArgs, r.CST.EnumEntryArgs, r.CST.InfixExpression, r.CST.IndexingExpression)
	fmt.Fprintf(&b, "  EXCLUDED-not-a-call: annotation_ctor %d, object_literal %d, method_reference %d, callable_reference %d, array_creation %d\n",
		r.CST.AnnotationCtorCall, r.CST.ObjectLiteral,
		r.CST.MethodReference, r.CST.CallableReference, r.CST.ArrayCreation)
	fmt.Fprintf(&b, "  PARSE DEGRADATION: %d file(s) with ERROR nodes, %d ERROR node(s) total\n",
		r.CST.FilesWithParseErrors, r.CST.ParseErrorNodes)
	fmt.Fprintf(&b, "  bound value sites (NOT in the numerator) %d\n", r.BoundValueSites)
	fmt.Fprintf(&b, "  SKIP HISTOGRAM total %d\n", r.SkipTotal())
	for _, row := range r.SkipHistogram() {
		fmt.Fprintf(&b, "    %-32s %d\n", row.Reason, row.Count)
	}
	fmt.Fprintf(&b, "  RESIDUAL denominator-bound-histogram = %d\n", r.Residual())
	if len(r.SkippedFiles) > 0 {
		s := append([]string(nil), r.SkippedFiles...)
		sort.Strings(s)
		if len(s) > 12 {
			s = append(s[:12], fmt.Sprintf("… and %d more", len(r.SkippedFiles)-12))
		}
		fmt.Fprintf(&b, "  TABLE-SKIPPED FILES:\n    %s\n", strings.Join(s, "\n    "))
	}
	return b.String()
}
