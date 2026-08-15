// Package jvmgroundtruth is the CI-ONLY differential soundness harness for the
// JVM declared-type binder (ADR 0008 WP-J9). It NEVER ships in the product:
// the product must stay CGo-free and toolchain-free, so no code here is
// imported by cmd/graphi or any surface — it lives under internal/ and is
// driven only by tests and the jvm-groundtruth CI workflow.
//
// The contract it enforces is ADR 0008's soundness direction: EVERY confirmed
// JVM `calls` edge graphi emits must correspond to a real static-binding call
// in the bytecode `javac` produced. A counterexample is a `JVMSOUND-0xx`
// defect and a stop-ship for the GA flip. Recall — the fraction of intra-repo
// bytecode calls graphi also confirmed — is MEASURED and reported, never
// gated: it is exactly the D2 coverage number the Kotlin `typed-confirmed`
// decision needs, and low recall is an honest capability statement, not a
// failure.
//
// Why bytecode is the right oracle: `invokevirtual`/`invokestatic`/… name the
// DECLARED type's method ref in the constant pool — precisely the static
// binding ADR 0008 D1 says a confirmed edge asserts. So this check verifies
// the D1 contract directly rather than approximating it. Constructor calls
// (`invokespecial <init>`) map to graphi's type-node-targeted calls edge, so
// `<init>` is normalized to the constructed type's simple name on both sides.
//
// Scope: `calls` edges only — the D1 call-binding contract. `references`
// (field reads) and `implements` (nominal, not a bytecode invoke) are outside
// the invoke-fact space and are not checked here.
package jvmgroundtruth

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Call is one method→method binding fact, at the granularity both graphi and
// the bytecode can express: the calling method's source file and name, and the
// callee method's source file and name. The line is deliberately NOT part of
// the key — graphi's node line and javac's LineNumberTable use different
// conventions, and a line mismatch would forge false soundness violations;
// method-to-method granularity is robust and still catches a fabricated call.
type Call struct {
	CallerFile   string
	CallerMethod string
	// CalleeFile is the callee class's repo-relative source path, or "" when
	// the callee is external (stdlib/third-party) — graphi never confirms
	// those, so they are neither soundness nor intra-repo-recall facts.
	CalleeFile string
	Callee     string
}

// ParseJavap extracts the intra-invoke call facts from `javap -c -p` output
// over the repository's compiled classes. It resolves each invoke's callee
// owner to a source path via the SourceFile headers in the SAME output, so the
// caller must pass javap output covering every class whose calls should be
// resolvable (external owners resolve to CalleeFile "").
//
// Pure and deterministic: identical input yields an identical, sorted slice.
func ParseJavap(out []byte) ([]Call, error) {
	// Pass 1: internal class name → repo-relative source path.
	classSource, err := parseClassSources(out)
	if err != nil {
		return nil, err
	}
	// Pass 2: raw invokes with the caller's class/method context.
	raws, err := parseInvokes(out)
	if err != nil {
		return nil, err
	}

	seen := map[Call]struct{}{}
	var calls []Call
	for _, r := range raws {
		callerFile := classSource[r.callerClassInternal]
		if callerFile == "" {
			// A caller class with no SourceFile header is a harness gap, not a
			// product fact — skip rather than invent a path.
			continue
		}
		callee := r.calleeName
		if callee == "<init>" {
			callee = simpleName(r.calleeOwnerInternal)
		}
		c := Call{
			CallerFile:   callerFile,
			CallerMethod: r.callerMethod,
			CalleeFile:   classSource[r.calleeOwnerInternal], // "" = external
			Callee:       callee,
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		calls = append(calls, c)
	}
	sortCalls(calls)
	return calls, nil
}

// parseClassSources scans for the `Compiled from "X.java"` + class-header
// pairs and maps each class's INTERNAL name (dots→slashes) to its
// repo-relative source path (package directory + source file name).
func parseClassSources(out []byte) (map[string]string, error) {
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	pendingSource := ""
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if s, ok := parseCompiledFrom(trimmed); ok {
			pendingSource = s
			continue
		}
		if dotted, ok := parseClassHeader(trimmed); ok {
			internal := strings.ReplaceAll(dotted, ".", "/")
			pkg := packageOf(dotted)
			src := pendingSource
			if src == "" {
				// A class with no preceding Compiled-from (synthetic?) still
				// deserves a path so its own methods resolve; fall back to the
				// simple name + .java.
				src = simpleName(internal) + ".java"
			}
			if pkg != "" {
				src = pkg + "/" + src
			}
			m[internal] = src
			pendingSource = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jvmgroundtruth: scan class sources: %w", err)
	}
	return m, nil
}

type rawInvoke struct {
	callerClassInternal string
	callerMethod        string
	calleeOwnerInternal string
	calleeName          string
}

// parseInvokes walks the disassembly tracking the current class and method,
// and records every `// Method owner.name:desc` invoke comment. invokedynamic
// (lambdas/method refs) carries a bootstrap comment, not a `// Method` ref, so
// it is naturally excluded — graphi never confirms those either.
func parseInvokes(out []byte) ([]rawInvoke, error) {
	var raws []rawInvoke
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	curClass := ""
	curMethod := ""
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if dotted, ok := parseClassHeader(trimmed); ok {
			curClass = strings.ReplaceAll(dotted, ".", "/")
			curMethod = ""
			continue
		}
		if name, ok := parseMethodHeader(trimmed); ok {
			curMethod = name
			continue
		}
		if owner, name, ok := parseMethodRef(trimmed); ok && curClass != "" && curMethod != "" {
			if owner == "" {
				// javap omits the owner prefix for a SAME-CLASS call
				// (`// Method rate:()I`), so the callee owner is the class
				// currently being disassembled.
				owner = curClass
			}
			raws = append(raws, rawInvoke{
				callerClassInternal: curClass,
				callerMethod:        curMethod,
				calleeOwnerInternal: owner,
				calleeName:          name,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jvmgroundtruth: scan invokes: %w", err)
	}
	return raws, nil
}

// parseCompiledFrom reads `Compiled from "Cart.java"`.
func parseCompiledFrom(trimmed string) (string, bool) {
	const p = `Compiled from "`
	if !strings.HasPrefix(trimmed, p) {
		return "", false
	}
	rest := trimmed[len(p):]
	if i := strings.IndexByte(rest, '"'); i >= 0 {
		return rest[:i], true
	}
	return "", false
}

// classKeywords precede a dotted class name in a javap type header.
var classKeywords = []string{"class ", "interface ", "enum "}

// parseClassHeader reads a top-of-class line like `public class shop.Cart {`
// or `class shop.Cart$Helper {` and returns the dotted class name. It requires
// the trailing `{` so a method whose name contains "class" cannot match.
func parseClassHeader(trimmed string) (string, bool) {
	if !strings.HasSuffix(trimmed, "{") {
		return "", false
	}
	for _, kw := range classKeywords {
		i := strings.Index(trimmed, kw)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(trimmed[i+len(kw):])
		rest = strings.TrimSuffix(rest, "{")
		rest = strings.TrimSpace(rest)
		// Drop any generic signature / extends / implements tail.
		if j := strings.IndexAny(rest, " <"); j >= 0 {
			rest = rest[:j]
		}
		if rest != "" {
			return rest, true
		}
	}
	return "", false
}

// parseMethodHeader reads a member declaration line (ends with ';', has a
// parameter list, is not an invoke/field line) and returns the method name.
// A constructor (name-before-paren carries a '.') normalizes to "<init>".
func parseMethodHeader(trimmed string) (string, bool) {
	if !strings.HasSuffix(trimmed, ";") {
		return "", false
	}
	open := strings.IndexByte(trimmed, '(')
	if open < 0 {
		return "", false // a field declaration, not a method
	}
	head := trimmed[:open]
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return "", false
	}
	name := fields[len(fields)-1]
	if strings.Contains(name, ".") {
		return "<init>", true // fully-qualified name before '(' is a constructor
	}
	return name, true
}

// parseMethodRef reads the `// Method owner.name:desc` trailer of an invoke
// instruction. Owner is an internal name (slashes) and may be ABSENT for a
// same-class call — javap prints `// Method rate:()I` with no owner then, and
// this returns owner "" so the caller substitutes the current class. Name may
// be a quoted "<init>". Field refs (`// Field …`) and invokedynamic bootstraps
// carry no `// Method ` and are ignored.
func parseMethodRef(trimmed string) (owner, name string, ok bool) {
	const marker = "// Method "
	i := strings.Index(trimmed, marker)
	if i < 0 {
		return "", "", false
	}
	ref := trimmed[i+len(marker):]
	colon := strings.IndexByte(ref, ':')
	if colon < 0 {
		return "", "", false
	}
	ownerName := ref[:colon] // tax/Rate.rate | java/lang/Object."<init>" | rate
	// The owner/name separator is the single '.' outside the quoted name;
	// internal owners use '/', so a '.' can only be that separator. A ref with
	// NO '.' is a same-class call with an implicit owner.
	dot := strings.LastIndexByte(ownerName, '.')
	if dot < 0 {
		name = strings.Trim(ownerName, `"`)
		if name == "" {
			return "", "", false
		}
		return "", name, true
	}
	owner = ownerName[:dot]
	name = strings.Trim(ownerName[dot+1:], `"`)
	if owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

// packageOf returns the dotted package of a dotted class name ("" for the
// default package), as a slash path suffix is applied by the caller.
func packageOf(dotted string) string {
	if i := strings.LastIndexByte(dotted, '.'); i >= 0 {
		return strings.ReplaceAll(dotted[:i], ".", "/")
	}
	return ""
}

// simpleName returns the last segment of an internal name, stripping any
// nesting ('$') so a nested owner reduces to its own simple name.
func simpleName(internal string) string {
	s := internal
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndexByte(s, '$'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// Result is the outcome of one soundness comparison.
type Result struct {
	// Violations are confirmed calls with no matching intra-repo bytecode
	// fact — the soundness set. MUST be empty; each entry is a JVMSOUND-0xx
	// candidate.
	Violations []Call
	// TruthIntra is the number of distinct intra-repo call facts in the
	// bytecode (the recall denominator).
	TruthIntra int
	// Matched is the number of confirmed calls that appear in the truth set
	// (the recall numerator; equals len(confirmed) when sound).
	Matched int
}

// Recall is Matched/TruthIntra (0 when there is nothing to recall).
func (r Result) Recall() float64 {
	if r.TruthIntra == 0 {
		return 0
	}
	return float64(r.Matched) / float64(r.TruthIntra)
}

// Sound reports the zero-tolerance soundness verdict.
func (r Result) Sound() bool { return len(r.Violations) == 0 }

// Compare checks the confirmed calls against the bytecode truth. Soundness:
// every confirmed call must be a distinct intra-repo truth fact. Recall is
// measured over the intra-repo truth facts.
func Compare(confirmed, truth []Call) Result {
	truthSet := map[Call]struct{}{}
	for _, c := range truth {
		if c.CalleeFile == "" {
			continue // external callee — not an intra-repo fact
		}
		truthSet[c] = struct{}{}
	}
	confSet := map[Call]struct{}{}
	for _, c := range confirmed {
		confSet[c] = struct{}{}
	}

	var res Result
	res.TruthIntra = len(truthSet)
	for c := range confSet {
		if _, ok := truthSet[c]; ok {
			res.Matched++
		} else {
			res.Violations = append(res.Violations, c)
		}
	}
	sortCalls(res.Violations)
	return res
}

// Format renders a deterministic report for CI logs.
func (r Result) Format() string {
	var b strings.Builder
	if r.Sound() {
		fmt.Fprintf(&b, "jvm-groundtruth SOUND — all %d confirmed calls are backed by bytecode; recall %d/%d = %.1f%%.\n",
			r.Matched, r.Matched, r.TruthIntra, r.Recall()*100)
		return b.String()
	}
	fmt.Fprintf(&b, "jvm-groundtruth SOUNDNESS FAILURE — %d confirmed call(s) have NO bytecode fact (each a JVMSOUND-0xx stop-ship):\n", len(r.Violations))
	for _, c := range r.Violations {
		fmt.Fprintf(&b, "  - %s.%s --calls--> %s.%s\n", c.CallerFile, c.CallerMethod, c.CalleeFile, c.Callee)
	}
	return b.String()
}

func sortCalls(cs []Call) {
	sort.Slice(cs, func(a, b int) bool {
		if cs[a].CallerFile != cs[b].CallerFile {
			return cs[a].CallerFile < cs[b].CallerFile
		}
		if cs[a].CallerMethod != cs[b].CallerMethod {
			return cs[a].CallerMethod < cs[b].CallerMethod
		}
		if cs[a].CalleeFile != cs[b].CalleeFile {
			return cs[a].CalleeFile < cs[b].CalleeFile
		}
		return cs[a].Callee < cs[b].Callee
	})
}
