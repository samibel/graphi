package jvmbindrate_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
//
// # The pin is the TREE, not only the commit (SW-175 review, MAJOR-4)
//
// Each entry pins THREE things, and the third is the one an earlier draft was
// missing. The commit sha says which revision was asked for; the TREE sha says
// what was actually measured; and `git status` says the bytes on disk are still
// that tree.
//
// The gap was demonstrated, not hypothesised: a reviewer's okio checkout carried
// 16 gitignored gradle-generated `.java` files under
// `.gradle/**/dependencies-accessors/**`. `readSources` walked the FILESYSTEM,
// so it swallowed them; the commit assertion stayed green and the two-run
// reproducibility check stayed identical, because both runs read the same wrong
// file set. okio's Java whole-pin figures moved 16.07 % → 12.72 % (29 files /
// 139 / 865 → 45 / 149 / 1 171) with every guard still passing. `git clean -xdff`
// restored exactly 29 `.java` and 284 `.kt` — the published numbers were right;
// the GUARANTEE that they stay right did not exist.
//
// The hazard is live rather than theoretical: `.github/workflows/jvm-corpus.yml`
// runs SW-173's oracle/compile step over `/tmp/pins` and then this measurement
// over THE SAME clones, and the published record invites any reader to
// recompute with GRAPHI_JVM_CORPUS_PINS pointed at a directory whose
// cleanliness nothing checked.
var pins = []struct {
	name, sha, tree string
	scopes          []scope
}{
	{"guava", "2214c63670fc161da170ac6e1a2d6d07e1531a55",
		"375ac95aadc97ff4989a4a6fa60fd606d8122050", []scope{
			{"whole-pin", ""},
			{"guava/src (JRE flavour only)", "guava/src/"},
			{"android/guava/src (Android flavour only)", "android/guava/src/"},
		}},
	{"okio", "8b870e8eaacecb1c1ceffbbb47246112604a1f92",
		"3b3cd2ef8f11b7fb43191bd2115220c2410d6f3e", []scope{
			{"whole-pin", ""},
			{"okio/ (the core module)", "okio/"},
		}},
	{"kotlinx.serialization", "3efe324be422ead21ca44f2f6318e1791c166556",
		"bf86bc149a72ddef6c5bcf9dea9537d5d2506c0e", []scope{
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
			assertPinnedTree(t, root, pin.sha, pin.tree)

			all := readSources(t, root)
			if len(all) == 0 {
				t.Fatal("no JVM sources found at the pin — a vacuous measurement")
			}
			t.Logf("PIN %s — commit %s\n  tree %s (asserted; working tree clean)\n  %d JVM sources enumerated from the pinned tree",
				pin.name, pin.sha, pin.tree, len(all))

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

// git runs one git command in the pin checkout and returns its stdout.
func git(t *testing.T, root string, arg ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, arg...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(arg, " "), root, err, stderr.String())
	}
	return string(out)
}

// assertPinnedTree is the MAJOR-4 guarantee: the measurement pins the TREE it
// measured, not merely the commit that was requested.
//
// Three assertions, each closing a different hole:
//
//  1. HEAD is the pinned COMMIT — the checkout is the revision claimed.
//  2. HEAD^{tree} is the pinned TREE — recorded in the pins table beside the
//     commit, so the published figures name the tree they were taken from and
//     a reader can verify it in one command.
//  3. `git status --porcelain` is EMPTY — no tracked file has been modified,
//     added or deleted on disk. A tree sha describes the committed objects; only
//     this says the bytes actually read still match them.
//
// Untracked and ignored files need no assertion here because readSources no
// longer looks at them: it enumerates from `git ls-tree`, so a generated
// `.java` sitting in the working directory cannot enter the denominator at all.
// Guarding at BOTH ends is deliberate — the enumeration makes the defect
// impossible and the status check makes a *different* defect (an edited pin)
// visible rather than silent.
//
// # Round 2: `git status` is not a sound basis, and is no longer the basis
//
// Assertion 3 was the ONLY thing standing between the measurement and edited
// bytes, because contents were read from disk. A reviewer bypassed it:
//
//	git update-index --assume-unchanged <file> && <edit it>
//
// leaves `git status --porcelain -uno` EMPTY — that is precisely what the bit
// means, "stop stat-ing this path" — and okio's Java rate moved 16.07 % ->
// 17.97 % (139/865 -> 159/885) with all three assertions green. `--skip-worktree`
// gave 16.55 %. Neither is reachable by accident: no clone, gradle build or
// kotlinc run sets those bits. But "you would have to do it deliberately" is the
// same defence the FILESYSTEM WALK had until a plain gradle build produced 16
// generated `.java`, so it is not a defence this file gets to use twice.
//
// The fix is not a fourth assertion, it is removing the premise: readSources now
// reads blob CONTENT from the object store (`git cat-file --batch`), so what is
// on disk cannot enter the measurement at all, whatever the index says. The
// index-bit assertion below is kept anyway — not because the content still
// depends on it, but so a tampered pin is REPORTED rather than silently
// bypassed-and-corrected. A pin someone has marked assume-unchanged is a fact
// the reader should see.
func assertPinnedTree(t *testing.T, root, wantCommit, wantTree string) {
	t.Helper()
	if got := strings.TrimSpace(git(t, root, "rev-parse", "HEAD")); got != wantCommit {
		t.Fatalf("pin checkout is at commit %s, manifest pins %s — the figures would not be about the pin", got, wantCommit)
	}
	// Assertion 2 closes no hole assertion 1 leaves open — a commit determines
	// its tree — and is not claimed to. Its value is PUBLICATION: the pins table
	// names the tree the figures were taken from, and this fails if the reader's
	// checkout disagrees with what is published.
	if got := strings.TrimSpace(git(t, root, "rev-parse", "HEAD^{tree}")); got != wantTree {
		t.Fatalf("pin checkout tree is %s, this measurement pins %s — the same commit cannot have two trees, so one of them is wrong", got, wantTree)
	}
	if dirty := strings.TrimSpace(git(t, root, "status", "--porcelain", "--untracked-files=no")); dirty != "" {
		t.Fatalf("pin checkout has MODIFIED TRACKED FILES — the bytes measured are not the pinned tree:\n%s", dirty)
	}
	// `git ls-files -v`: a lowercase tag marks assume-unchanged, `S` marks
	// skip-worktree. Both make `git status` lie by design.
	var hidden []string
	for _, line := range strings.Split(git(t, root, "ls-files", "-v"), "\n") {
		if len(line) < 2 || line[1] != ' ' {
			continue
		}
		if tag := line[0]; tag == 'S' || (tag >= 'a' && tag <= 'z') {
			hidden = append(hidden, line)
		}
	}
	if len(hidden) > 0 {
		if len(hidden) > 12 {
			hidden = append(hidden[:12], fmt.Sprintf("… and %d more", len(hidden)-12))
		}
		t.Fatalf("pin checkout has %d path(s) marked assume-unchanged or skip-worktree — `git status` is suppressed for them by design, so its emptiness proves nothing:\n  %s",
			len(hidden), strings.Join(hidden, "\n  "))
	}
}

// readSources reads every .java and .kt file AT THE PINNED TREE, keyed by its
// repository-relative slash path (the same key shape the ingest pass uses).
//
// The file set comes from `git ls-tree -r HEAD`, NOT from walking the
// filesystem. An earlier draft walked the filesystem and skipped only `.git`,
// with the comment "nothing else is filtered, because filtering is how a
// denominator quietly shrinks" — which is true, and left the INVERSE hazard
// wide open: a denominator quietly GROWING with build-generated sources that no
// commit contains. That hazard was demonstrated on a real checkout (see the
// pins table). Enumerating the tree closes it without reintroducing the one the
// comment feared, because `ls-tree` is not a filter — it is the definition of
// what the pin contains. Nothing is excluded from it; a file is in the
// measurement if and only if the pinned commit contains it.
//
// CONTENTS come from the object store too, not from disk (SW-175 review round 2,
// MINOR-B). An earlier draft read `os.ReadFile` and said so, with the premise
// stated in this comment: "safe only because assertPinnedTree has already
// established that no tracked file differs from the tree". That premise was
// false — `git update-index --assume-unchanged` plus an edit keeps `git status`
// empty by design and moved okio's Java rate 16.07 % -> 17.97 %.
//
// So the file list AND the bytes now both come from `git ls-tree` + `git
// cat-file --batch`. The working directory is not read at all, and the pinned
// tree is not merely asserted to be what was measured — it IS what was measured.
// The only way to change these figures is to change the pinned commit, which
// changes the sha in the table.
func readSources(t *testing.T, root string) map[string][]byte {
	t.Helper()

	// `ls-tree -r HEAD -z` emits "<mode> <type> <sha>\t<path>\0". The blob sha
	// is what makes the content addressable without touching the checkout.
	type entry struct{ sha, rel string }
	var want []entry
	for _, rec := range strings.Split(git(t, root, "ls-tree", "-r", "HEAD", "-z"), "\x00") {
		if rec == "" {
			continue
		}
		meta, rel, ok := strings.Cut(rec, "\t")
		if !ok {
			t.Fatalf("unparseable ls-tree record %q", rec)
		}
		switch strings.ToLower(path.Ext(rel)) {
		case ".java", ".kt":
		default:
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 || f[1] != "blob" {
			// A submodule (commit) or malformed record. Neither is a source
			// file at this pin; skipping silently is how a denominator shrinks,
			// so say so.
			t.Logf("ls-tree: skipping non-blob entry %q (%s)", rel, meta)
			continue
		}
		want = append(want, entry{f[2], rel})
	}
	if len(want) == 0 {
		return map[string][]byte{}
	}

	// One `cat-file --batch` for the whole pin: 3 204 separate `git show`
	// invocations would dominate the run time of the measurement.
	var stdin bytes.Buffer
	for _, e := range want {
		stdin.WriteString(e.sha)
		stdin.WriteByte('\n')
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = &stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	blob, err := cmd.Output()
	if err != nil {
		t.Fatalf("git cat-file --batch in %s: %v: %s", root, err, stderr.String())
	}

	// Replies are "<sha> <type> <size>\n<size bytes>\n", in request order.
	out := map[string][]byte{}
	rest := blob
	for _, e := range want {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			t.Fatalf("cat-file stream truncated before %s", e.rel)
		}
		hdr := strings.Fields(string(rest[:nl]))
		if len(hdr) != 3 || hdr[0] != e.sha || hdr[1] != "blob" {
			t.Fatalf("cat-file returned %q for %s (blob %s) — the object store does not hold the pinned bytes",
				string(rest[:nl]), e.rel, e.sha)
		}
		size, err := strconv.Atoi(hdr[2])
		if err != nil {
			t.Fatalf("cat-file size %q for %s: %v", hdr[2], e.rel, err)
		}
		rest = rest[nl+1:]
		if len(rest) < size+1 {
			t.Fatalf("cat-file stream holds %d bytes for %s, header says %d", len(rest), e.rel, size)
		}
		out[e.rel] = rest[:size:size]
		rest = rest[size+1:] // trailing newline git appends after each object
	}
	if len(rest) != 0 {
		t.Fatalf("cat-file stream has %d trailing bytes — the reply order did not match the request order", len(rest))
	}
	return out
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
	// RateText, never Rate: 0 bound of 0 sites is `n/a`, not 0 %. The document
	// has always said so (§7.2, §11.8); this log used to print `RATE 0.00%`
	// and contradict it on the one case the story singled out.
	fmt.Fprintf(&b, "  RATE %s (bound call sites / CST call sites)\n", r.RateText())
	fmt.Fprintf(&b, "  widest denominator %s (every construct that NAMES a callable added back)\n",
		r.WidestRateText())
	fmt.Fprintf(&b, "  denominator parts: method_invocation %d, object_creation %d, call_suffix %d\n",
		r.CST.MethodInvocation, r.CST.ObjectCreation, r.CST.CallSuffix)
	fmt.Fprintf(&b, "  EXCLUDED-but-a-call: explicit_ctor %d, ctor_delegation_call %d, superclass_delegation %d, enum_constant %d, enum_entry %d, infix %d, indexing_suffix %d\n",
		r.CST.ExplicitCtorInvocation, r.CST.CtorDelegationCall, r.CST.DelegationCtorCall,
		r.CST.EnumConstantArgs, r.CST.EnumEntryArgs, r.CST.InfixExpression, r.CST.IndexingSuffix)
	fmt.Fprintf(&b, "  OPERATOR-CONVENTION calls (excluded, in the widest) total %d: additive %d, multiplicative %d, prefix %d, postfix %d, equality %d, comparison %d, range %d, contains %d, augmented_assign %d\n",
		r.CST.OperatorConventionCalls(),
		r.CST.AdditiveExpression, r.CST.MultiplicativeExpression,
		r.CST.PrefixExpression, r.CST.PostfixExpression,
		r.CST.EqualityExpression, r.CST.ComparisonExpression,
		r.CST.RangeExpression, r.CST.ContainsExpression, r.CST.AugmentedAssignment)
	fmt.Fprintf(&b, "  OPERATOR-SHAPED but NOT a call: not_null_assertion(!!) %d, referential_equality(===) %d, plain_assignment(=) %d, type_test(is) %d, UNCLASSIFIED %d\n",
		r.CST.NotNullAssertion, r.CST.ReferentialEquality,
		r.CST.PlainAssignment, r.CST.TypeTest, r.CST.UnclassifiedOperator)
	fmt.Fprintf(&b, "  SYNTHESIZED-PROTOCOL calls (in NO denominator, both languages) >=%d: for-in loops %d (x3), destructuring components %d, delegated properties %d, try-with-resources %d\n",
		r.CST.SynthesizedProtocolCalls(), r.CST.ForInLoops,
		r.CST.DestructuringComponents, r.CST.DelegatedProperties, r.CST.TryWithResources)
	fmt.Fprintf(&b, "  EXCLUDED-not-a-call: annotation_ctor %d, object_literal %d, method_reference %d, callable_reference %d, array_creation %d\n",
		r.CST.AnnotationCtorCall, r.CST.ObjectLiteral,
		r.CST.MethodReference, r.CST.CallableReference, r.CST.ArrayCreation)
	fmt.Fprintf(&b, "  PARSE DEGRADATION: %d file(s) with ERROR nodes, %d ERROR node(s) total\n",
		r.CST.FilesWithParseErrors, r.CST.ParseErrorNodes)
	// FINDING B-0's direction, measured rather than declared undetermined.
	fmt.Fprintf(&b, "  PARSE-QUALITY SPLIT (one table over all files; the arms partition the corpus)\n")
	fmt.Fprintf(&b, "    CLEAN files %d: %s\n", r.Clean.Files, r.Clean.RateText())
	fmt.Fprintf(&b, "    DIRTY files %d (%d ERROR nodes): %s\n",
		r.Dirty.Files, r.Dirty.ParseErrorNodes, r.Dirty.RateText())
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
