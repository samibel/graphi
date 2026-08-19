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
//
// # SW-172 — the oracle is SIGNATURE-AWARE, in three named precisions
//
// The original comparison keyed a call on (caller file, caller method, callee
// file, callee NAME) only. That key is structurally blind to mis-binding
// between same-named overloads declared in one file: whichever overload the
// binder picked, the fact reads the same. Scale cannot repair that — a
// name-keyed comparison cannot see an overload error however many call sites
// it visits — so the key gained two coarse-to-fine levels (see Precision):
//
//	ByName       the original key. Kept, because it is the only level the
//	             GRAPH STORE can express: node identity collapses overloads
//	             (engine/jvmresolve/qn.go — "same kind, same flat QN, same
//	             id"), so two same-named methods in one file are ONE node.
//	ByArity      + the callee's argument count. Catches every overload pair
//	             that differs in argument count.
//	BySignature  + the callee's erased parameter types. Catches the equal-arity
//	             pairs that differ only in parameter type.
//
// Because the store cannot carry arity, the two finer levels do not read it:
// their graphi side comes from the binder itself (binder.go), where the
// signature still exists.
//
// # The oracle ABSTAINS rather than guessing
//
// Both sides of the differential are approximations of the JVM's own rules,
// and an oracle that guesses is worse than one that declines: a wrong oracle
// silently corrupts every evidence row measured against it. So a fact that
// cannot be decided SOUNDLY at a precision carries a named reason (see the
// abstention vocabulary below), and Compare reports it as ABSTAINED — neither
// matched nor a violation — with the reason counted. The vocabulary is closed
// and constant, mirroring the binder's own named-skip discipline
// (engine/jvmresolve SkipCounts): an abstention is a refusal to judge, and it
// is never folded into either verdict.
package jvmgroundtruth

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// ArityUnknown / SigUnknown are the "not decidable here" values of the two
// signature fields. They are distinct from a real zero-argument call (arity 0)
// and a real zero-parameter signature (SigUnknown is "" and an empty parameter
// list renders as "()", never ""), so "no answer" and "the answer is empty"
// can never be confused — the same fail-closed rule the trust surfaces hold.
const (
	ArityUnknown = -1
	SigUnknown   = ""
)

// The abstention vocabulary — the closed set of reasons a call fact cannot be
// judged at a given precision. Constants, not ad-hoc strings, because they are
// reported data.
const (
	// Bytecode side.

	// AbstainBytecodeExternalParam: a parameter of the invoked descriptor is a
	// class the repository does not declare (`Ljava/lang/String;`). The binder
	// records such a parameter as unresolved written text, so the two sides
	// cannot be compared as types without inventing a package for it.
	AbstainBytecodeExternalParam = "bytecode_external_param_type"
	// AbstainBytecodeOwnerUnresolved: JVM method resolution (JVMS 5.4.3.3) ran
	// off the end of the known class chain — the declaring class is external,
	// or the method is an interface default the class walk does not model.
	AbstainBytecodeOwnerUnresolved = "bytecode_owner_unresolved"
	// AbstainBytecodeNoDescriptors: the javap output carried no `descriptor:`
	// lines (captured without -s), so the declared-method table is empty and
	// the owner walk cannot run. The symbolic owner stands, which is the
	// pre-SW-172 behaviour, and the finer precisions decline.
	AbstainBytecodeNoDescriptors = "bytecode_no_descriptor_table"

	// Binder side.

	// AbstainBinderElastic: the bound member has a variadic or defaulted
	// parameter, so its declared parameter count is NOT the arity javac
	// compiles the call to (`f(int...)` called as `f(1,2,3)` is `([I)V`).
	// hierarchy.go already forfeits these, so this is a belt on the braces.
	AbstainBinderElastic = "binder_elastic_member"
	// AbstainBinderParamUnresolved: a parameter type of the bound member does
	// not resolve to an intra-repo type and is not a primitive, so its erased
	// JVM descriptor is unknowable from the declaration alone.
	AbstainBinderParamUnresolved = "binder_param_type_unresolved"
	// AbstainKotlinShapeUnproven: Kotlin. The mapping from a declared Kotlin
	// parameter to its JVM descriptor is NOT proven here — nullability boxes
	// (`Int` is I but `Int?` is Ljava/lang/Integer;), extension receivers and
	// suspend continuations add parameters that no `Member` field records, and
	// kotlinc is absent from this sandbox and unpinned in CI. So the Kotlin
	// side declines at both finer precisions rather than asserting a mapping
	// nothing here has ever compiled. Proving it is SW-173's job.
	AbstainKotlinShapeUnproven = "kotlin_bytecode_shape_unproven"
)

// Precision is how finely a call fact is keyed. Coarser precisions are strict
// prefixes of finer ones, so a violation at a coarse precision is a violation
// at every finer one.
type Precision int

const (
	// ByName keys on (caller file, caller method, callee file, callee name).
	// The GRAPH-STORE level: it is exactly what node identity can express.
	ByName Precision = iota
	// ByArity adds the callee's argument count (SW-172 stage 1).
	ByArity
	// BySignature adds the callee's erased parameter types (SW-172 stage 2).
	BySignature
)

func (p Precision) String() string {
	switch p {
	case ByName:
		return "by-name"
	case ByArity:
		return "by-arity"
	case BySignature:
		return "by-signature"
	}
	return "unknown"
}

// coarser returns the next-coarser precision, and false at ByName. Compare
// uses it to tell "the truth set disagrees" from "the truth set declines":
// when a confirmed call finds no match at p but the truth holds a fact that
// matches one level coarser and is itself undecidable at p, the honest verdict
// is an abstention, not a counterexample.
func (p Precision) coarser() (Precision, bool) {
	if p == ByName {
		return ByName, false
	}
	return p - 1, true
}

// Call is one method→method binding fact, at the granularity both graphi and
// the bytecode can express: the calling method's source file and name, and the
// callee method's source file and name. The line is deliberately NOT part of
// the key — graphi's node line and javac's LineNumberTable use different
// conventions, and a line mismatch would forge false soundness violations;
// method-to-method granularity is robust and still catches a fabricated call.
//
// The signature fields (SW-172) are keyed only at the precisions that name
// them; see Precision. They are NOT part of struct equality — use key().
type Call struct {
	CallerFile   string
	CallerMethod string
	// CalleeFile is the callee class's repo-relative source path, or "" when
	// the callee is external (stdlib/third-party) — graphi never confirms
	// those, so they are neither soundness nor intra-repo-recall facts.
	CalleeFile string
	Callee     string

	// CalleeArity is the callee's argument count, or ArityUnknown.
	CalleeArity int
	// CalleeParams is the callee's erased parameter list in the SHARED
	// alphabet both sides can render exactly: "(" + JVM field descriptors +
	// ")", e.g. "(I[La/Thing;)". SigUnknown when undecidable. The alphabet is
	// deliberately narrow — primitives, intra-repo classes, and arrays of
	// either — because those are the only parameter forms the declaration side
	// can erase without guessing.
	CalleeParams string
	// CalleeCtor marks a constructor call. Both sides normalize the callee
	// NAME to the constructed type's simple name (graphi's constructor edge
	// targets the type node), so the `<init>` fact is otherwise unrecoverable
	// — and DeclaredMethods.Verify needs it to look the member up under the
	// name javac filed it under. Not keyed on: the normalized name already
	// distinguishes it.
	CalleeCtor bool
	// CalleeDescriptor is the RAW constant-pool descriptor of the invoked
	// method ref, verbatim from javap (AC-3). Bytecode side only; "" on the
	// binder side, which has no descriptor to quote. Carried for diagnostics —
	// it is what a reader needs to check a reported counterexample by hand —
	// and never keyed on, since only one side can supply it.
	CalleeDescriptor string

	// ArityReason / ParamsReason name WHY the corresponding field is unknown
	// (one of the Abstain* constants); "" when the field is decidable.
	ArityReason  string
	ParamsReason string
}

// callKey is the comparison identity of a Call at one precision. Struct
// equality on Call itself would key on the diagnostic and reason fields, which
// are not facts about the call — hence a projection.
type callKey struct {
	callerFile   string
	callerMethod string
	calleeFile   string
	callee       string
	arity        int
	params       string
}

func (c Call) key(p Precision) callKey {
	k := callKey{
		callerFile:   c.CallerFile,
		callerMethod: c.CallerMethod,
		calleeFile:   c.CalleeFile,
		callee:       c.Callee,
		arity:        ArityUnknown,
		params:       SigUnknown,
	}
	if p >= ByArity {
		k.arity = c.CalleeArity
	}
	if p >= BySignature {
		k.params = c.CalleeParams
	}
	return k
}

// undecidableAt reports whether this fact carries no answer at precision p,
// and the named reason. Arity-unknown implies params-unknown: a signature
// whose length is unknown cannot have a known parameter list.
func (c Call) undecidableAt(p Precision) (string, bool) {
	if p >= ByArity && c.CalleeArity == ArityUnknown {
		return c.ArityReason, true
	}
	if p >= BySignature && c.CalleeParams == SigUnknown {
		return c.ParamsReason, true
	}
	return "", false
}

// ParseJavap extracts the intra-invoke call facts from `javap -c -p -s` output
// over the repository's compiled classes. It resolves each invoke's callee
// owner to a source path via the SourceFile headers in the SAME output, so the
// caller must pass javap output covering every class whose calls should be
// resolvable (external owners resolve to CalleeFile "").
//
// The `-s` flag is what makes the signature precisions available: it prints a
// `descriptor:` line under every declared method, which is what the JVM
// method-resolution walk below needs. Output captured WITHOUT it still parses,
// at ByName precision only, and says so through AbstainBytecodeNoDescriptors
// rather than degrading silently.
//
// Pure and deterministic: identical input yields an identical, sorted slice.
func ParseJavap(out []byte) ([]Call, error) {
	classes, err := parseClasses(out)
	if err != nil {
		return nil, err
	}
	classSource := map[string]string{}
	for internal, c := range classes {
		classSource[internal] = c.source
	}
	// Pass 2: raw invokes with the caller's class/method context.
	raws, err := parseInvokes(out)
	if err != nil {
		return nil, err
	}

	seen := map[callKey]struct{}{}
	var calls []Call
	for _, r := range raws {
		callerFile := classSource[r.callerClassInternal]
		if callerFile == "" {
			// A caller class with no SourceFile header is a harness gap, not a
			// product fact — skip rather than invent a path.
			continue
		}
		callee := r.calleeName
		ctor := callee == "<init>"
		if ctor {
			callee = simpleName(r.calleeOwnerInternal)
		}

		// JVM method resolution (JVMS 5.4.3.3): the constant-pool ref names
		// the SYMBOLIC owner — for `d.m(1)` where m is inherited, javac writes
		// `a/Derived.m`, not `a/Base.m` — and the JVM then searches the class
		// and its superclasses for the (name, descriptor). graphi's confirmed
		// edge points at the DECLARING method, so the symbolic owner must be
		// walked to the declaring class or the two sides disagree on a fact
		// they both get right. Without this walk the oracle manufactures FALSE
		// counterexamples on every inherited call.
		ownerInternal, ownerReason := resolveOwner(classes, r.calleeOwnerInternal, r.calleeName, r.calleeDescriptor)

		c := Call{
			CallerFile:       callerFile,
			CallerMethod:     r.callerMethod,
			CalleeFile:       classSource[ownerInternal], // "" = external
			Callee:           callee,
			CalleeCtor:       ctor,
			CalleeDescriptor: r.calleeDescriptor,
			CalleeArity:      ArityUnknown,
			CalleeParams:     SigUnknown,
			ArityReason:      ownerReason,
			ParamsReason:     ownerReason,
		}
		if ownerReason == AbstainBytecodeOwnerUnresolved {
			// The declaring class is not in the repository, so this is not an
			// intra-repo fact at all — recording it under the symbolic owner's
			// path would fabricate one and inflate the recall denominator.
			c.CalleeFile = ""
		}
		// Arity and signature are offered ONLY when the owner walk succeeded.
		// Without it CalleeFile is the SYMBOLIC owner's path rather than the
		// declaring class's, which is sound at ByName (that is the pre-SW-172
		// key, kept exactly) but is not a foundation a finer verdict may rest
		// on: a fact keyed on the wrong file is wrong at every precision, and
		// adding arity to a wrong file would dress it up as more certain.
		if ownerReason == "" {
			if params, ok := descriptorParams(r.calleeDescriptor); ok {
				c.CalleeArity = len(params)
				c.ArityReason = ""
				if sig, ok := intraRepoSignature(params, classSource); ok {
					c.CalleeParams = sig
					c.ParamsReason = ""
				} else {
					c.ParamsReason = AbstainBytecodeExternalParam
				}
			} else {
				c.ArityReason = AbstainBytecodeNoDescriptors
				c.ParamsReason = AbstainBytecodeNoDescriptors
			}
		}

		k := c.key(BySignature)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		calls = append(calls, c)
	}
	sortCalls(calls)
	return calls, nil
}

// classInfo is what one disassembled class contributes: where its source
// lives, what it extends, and which (name, descriptor) pairs it declares.
type classInfo struct {
	source string
	super  string              // internal name of the superclass; "" = none/unknown
	decls  map[string]struct{} // "name:descriptor"
}

// resolveOwner walks the symbolic owner up its superclass chain to the class
// that DECLARES (name, descriptor), per JVMS 5.4.3.3. Interfaces are not
// walked: a default method's declaring interface is not on the superclass
// chain, and modelling interface resolution correctly needs the full
// maximally-specific rule — so that case declines instead of guessing.
func resolveOwner(classes map[string]*classInfo, owner, name, desc string) (string, string) {
	if desc == "" {
		return owner, AbstainBytecodeNoDescriptors
	}
	ci, known := classes[owner]
	if !known {
		// An external owner (java/lang/Object): nothing to walk, and
		// classSource has no entry, so it scores as external either way.
		return owner, ""
	}
	if len(ci.decls) == 0 {
		// The class is known but carried no `descriptor:` lines: captured
		// without -s. Keep the symbolic owner — the pre-SW-172 answer.
		return owner, AbstainBytecodeNoDescriptors
	}
	want := name + ":" + desc
	for cur := owner; ; {
		ci, ok := classes[cur]
		if !ok {
			// Ran into a class outside the disassembly (typically an external
			// superclass): the declaration is not in the repository.
			return cur, AbstainBytecodeOwnerUnresolved
		}
		if _, has := ci.decls[want]; has {
			return cur, ""
		}
		if ci.super == "" {
			return cur, AbstainBytecodeOwnerUnresolved
		}
		cur = ci.super
	}
}

// parseClasses scans for the `Compiled from "X.java"` + class-header pairs and
// records, per class INTERNAL name (dots→slashes), its repo-relative source
// path (package directory + source file name), its superclass, and its
// declared (name, descriptor) pairs.
func parseClasses(out []byte) (map[string]*classInfo, error) {
	m := map[string]*classInfo{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	pendingSource := ""
	var cur *classInfo
	pendingMethod := ""
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if s, ok := parseCompiledFrom(trimmed); ok {
			pendingSource = s
			continue
		}
		if hdr, ok := parseClassHeader(trimmed); ok {
			internal := strings.ReplaceAll(hdr.name, ".", "/")
			pkg := packageOf(hdr.name)
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
			ci := &classInfo{source: src, decls: map[string]struct{}{}}
			if hdr.super != "" {
				ci.super = strings.ReplaceAll(hdr.super, ".", "/")
			}
			m[internal] = ci
			cur = ci
			pendingSource = ""
			pendingMethod = ""
			continue
		}
		if desc, ok := parseDescriptorLine(trimmed); ok {
			if cur != nil && pendingMethod != "" {
				cur.decls[pendingMethod+":"+desc] = struct{}{}
				pendingMethod = ""
			}
			continue
		}
		if name, ok := parseMethodHeader(trimmed); ok {
			pendingMethod = name
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jvmgroundtruth: scan class sources: %w", err)
	}
	return m, nil
}

// parseClassSources is the source-path-only view parseClasses supersedes; it
// is kept because the class→path mapping is a fact worth pinning on its own.
func parseClassSources(out []byte) (map[string]string, error) {
	classes, err := parseClasses(out)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(classes))
	for k, v := range classes {
		m[k] = v.source
	}
	return m, nil
}

type rawInvoke struct {
	callerClassInternal string
	callerMethod        string
	calleeOwnerInternal string
	calleeName          string
	calleeDescriptor    string
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
		if hdr, ok := parseClassHeader(trimmed); ok {
			curClass = strings.ReplaceAll(hdr.name, ".", "/")
			curMethod = ""
			continue
		}
		if name, ok := parseMethodHeader(trimmed); ok {
			curMethod = enclosingOfSynthetic(name)
			continue
		}
		if owner, name, desc, ok := parseMethodRef(trimmed); ok && curClass != "" && curMethod != "" {
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
				calleeDescriptor:    desc,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jvmgroundtruth: scan invokes: %w", err)
	}
	return raws, nil
}

// enclosingOfSynthetic maps javac's synthetic lambda-body method back to the
// method the lambda was WRITTEN in: `lambda$run$0` → `run`. graphi attributes
// a call inside a lambda to the enclosing declaration (the lambda body is not
// a declaration and mints no node), so without this the two sides disagree on
// the CALLER of every call made inside a lambda and the oracle reports a false
// counterexample. The name shape is javac's convention,
// `lambda$<enclosing>$<index>`; anything else is left alone.
//
// The two special enclosing names were MEASURED against javac 21, not assumed:
// a lambda written in a constructor compiles to `lambda$new$0` and belongs to
// `<init>`, and one in a static initializer to `lambda$static$1`, belonging to
// `<clinit>`. Neither `new` nor `static` can be a real method name — both are
// keywords — so the mapping cannot collide with a user's own method.
func enclosingOfSynthetic(name string) string {
	const p = "lambda$"
	if !strings.HasPrefix(name, p) {
		return name
	}
	rest := name[len(p):]
	i := strings.LastIndexByte(rest, '$')
	if i <= 0 {
		return name
	}
	switch enclosing := rest[:i]; enclosing {
	case "new":
		return "<init>"
	case "static":
		return "<clinit>"
	default:
		return enclosing
	}
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

// parseDescriptorLine reads javap -s's `descriptor: (I)I` member line.
func parseDescriptorLine(trimmed string) (string, bool) {
	const p = "descriptor: "
	if !strings.HasPrefix(trimmed, p) {
		return "", false
	}
	d := strings.TrimSpace(trimmed[len(p):])
	if d == "" {
		return "", false
	}
	return d, true
}

// classKeywords precede a dotted class name in a javap type header.
var classKeywords = []string{"class ", "interface ", "enum "}

// classHeader is a parsed type header: the dotted class name and, when the
// header declares one, the dotted name of its superclass.
type classHeader struct {
	name  string
	super string
}

// parseClassHeader reads a top-of-class line like `public class shop.Cart {`,
// `class shop.Cart$Helper {` or
// `public class a.Box<T extends java.lang.Number> extends a.Base {` and
// returns the dotted class name plus its `extends` target. It requires the
// trailing `{` so a method whose name contains "class" cannot match.
//
// The generic-parameter list is skipped with a bracket-depth counter, not a
// first-match search: a bound such as `<T extends Number>` contains the very
// keyword the superclass scan looks for, and reading THAT as the superclass
// would send the JVM resolution walk up an imaginary chain.
func parseClassHeader(trimmed string) (classHeader, bool) {
	if !strings.HasSuffix(trimmed, "{") {
		return classHeader{}, false
	}
	for _, kw := range classKeywords {
		i := strings.Index(trimmed, kw)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed[i+len(kw):]), "{"))
		name, tail := splitOutsideBrackets(rest)
		if name == "" {
			continue
		}
		return classHeader{name: name, super: extendsTarget(tail)}, true
	}
	return classHeader{}, false
}

// splitOutsideBrackets cuts rest at the first space or '<' that sits at
// angle-bracket depth zero, returning the class name and the remaining tail
// (the generic list, extends and implements clauses).
func splitOutsideBrackets(rest string) (name, tail string) {
	depth := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '<':
			if depth == 0 {
				return rest[:i], rest[i:]
			}
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case ' ':
			if depth == 0 {
				return rest[:i], rest[i:]
			}
		}
	}
	return rest, ""
}

// extendsTarget finds the `extends X` target at bracket depth zero, dropping
// any generic arguments on X itself and stopping at `implements`.
func extendsTarget(tail string) string {
	depth := 0
	i := 0
	for ; i < len(tail); i++ {
		switch tail[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case 'e':
			if depth != 0 || !strings.HasPrefix(tail[i:], "extends ") {
				continue
			}
			if i > 0 && tail[i-1] != ' ' {
				continue
			}
			target, _ := splitOutsideBrackets(strings.TrimSpace(tail[i+len("extends "):]))
			return strings.TrimSuffix(strings.TrimSpace(target), ",")
		}
	}
	return ""
}

// parseMethodHeader reads a member declaration line (ends with ';', has a
// parameter list, is not an invoke/field line) and returns the method name.
// A constructor (name-before-paren carries a '.') normalizes to "<init>".
func parseMethodHeader(trimmed string) (string, bool) {
	if !strings.HasSuffix(trimmed, ";") {
		return "", false
	}
	// `descriptor: (Ltax/Rate;)Ltax/Rate;` — javap -s's own line — ends with
	// ';' and contains '(', so it satisfies every test below and would be read
	// as a method named "descriptor:". Rejected explicitly rather than by
	// check ORDER, so a caller that scans in the other order cannot silently
	// reintroduce the bug. (It did: the live gate caught the truth set
	// attributing a constructor call to a caller method called "descriptor:".)
	if strings.HasPrefix(trimmed, "descriptor: ") {
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
//
// SW-172 (AC-3): the DESCRIPTOR is returned rather than discarded. It is the
// only place the callee's real, javac-chosen signature exists in this output,
// and throwing it away is what made the oracle blind to overload mis-binding.
func parseMethodRef(trimmed string) (owner, name, desc string, ok bool) {
	const marker = "// Method "
	i := strings.Index(trimmed, marker)
	if i < 0 {
		return "", "", "", false
	}
	ref := trimmed[i+len(marker):]
	colon := strings.IndexByte(ref, ':')
	if colon < 0 {
		return "", "", "", false
	}
	ownerName := ref[:colon] // tax/Rate.rate | java/lang/Object."<init>" | rate
	desc = strings.TrimSpace(ref[colon+1:])
	// The owner/name separator is the single '.' outside the quoted name;
	// internal owners use '/', so a '.' can only be that separator. A ref with
	// NO '.' is a same-class call with an implicit owner.
	dot := strings.LastIndexByte(ownerName, '.')
	if dot < 0 {
		name = strings.Trim(ownerName, `"`)
		if name == "" {
			return "", "", "", false
		}
		return "", name, desc, true
	}
	owner = ownerName[:dot]
	name = strings.Trim(ownerName[dot+1:], `"`)
	if owner == "" || name == "" {
		return "", "", "", false
	}
	return owner, name, desc, true
}

// descriptorParams splits a JVM method descriptor's parameter list into field
// descriptors: "(I[La/Thing;)V" → ["I", "[La/Thing;"]. Returns ok=false for
// anything it cannot read exactly — a malformed descriptor must never be
// silently read as an empty parameter list, which would forge an arity-0 fact.
func descriptorParams(desc string) ([]string, bool) {
	if !strings.HasPrefix(desc, "(") {
		return nil, false
	}
	end := strings.IndexByte(desc, ')')
	if end < 0 {
		return nil, false
	}
	body := desc[1:end]
	var out []string
	for i := 0; i < len(body); {
		n, ok := fieldDescriptorLen(body[i:])
		if !ok {
			return nil, false
		}
		out = append(out, body[i:i+n])
		i += n
	}
	return out, true
}

// fieldDescriptorLen returns the byte length of the field descriptor at the
// head of s (JVMS 4.3.2).
func fieldDescriptorLen(s string) (int, bool) {
	i := 0
	for i < len(s) && s[i] == '[' {
		i++
	}
	if i >= len(s) {
		return 0, false
	}
	switch s[i] {
	case 'B', 'C', 'D', 'F', 'I', 'J', 'S', 'Z':
		return i + 1, true
	case 'L':
		end := strings.IndexByte(s[i:], ';')
		if end < 0 {
			return 0, false
		}
		return i + end + 1, true
	}
	return 0, false
}

// intraRepoSignature renders a descriptor's parameter list into the shared
// alphabet, or declines. A parameter naming a class the repository does not
// declare makes the WHOLE signature undecidable: the binder side records such
// a parameter as unresolved written text ("String"), and pairing that with
// "Ljava/lang/String;" would require inventing the package the source never
// stated — precisely the guess the oracle must not make.
func intraRepoSignature(params []string, classSource map[string]string) (string, bool) {
	var b strings.Builder
	b.WriteByte('(')
	for _, p := range params {
		elem := strings.TrimLeft(p, "[")
		if strings.HasPrefix(elem, "L") {
			internal := strings.TrimSuffix(strings.TrimPrefix(elem, "L"), ";")
			if _, intra := classSource[internal]; !intra {
				return SigUnknown, false
			}
		}
		b.WriteString(p)
	}
	b.WriteByte(')')
	return b.String(), true
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

// Result is the outcome of one soundness comparison at one precision.
type Result struct {
	// Precision is the key the verdict was reached at.
	Precision Precision
	// Violations are confirmed calls with no matching intra-repo bytecode
	// fact — the soundness set. MUST be empty; each entry is a JVMSOUND-0xx
	// candidate.
	Violations []Call
	// Abstained are confirmed calls the oracle DECLINED to judge at this
	// precision, because one side could not render the fact soundly. They are
	// neither matched nor violations: an abstention is a refusal to judge, and
	// folding it into either verdict would be the confidence laundering the
	// programme's rules forbid.
	Abstained []Call
	// AbstainReasons counts Abstained by named reason. nil when empty, so
	// "nothing abstained" and "the map exists but is empty" cannot be told
	// apart wrongly downstream.
	AbstainReasons map[string]int
	// TruthIntra is the number of distinct intra-repo call facts in the
	// bytecode that are DECIDABLE at this precision (the recall denominator).
	TruthIntra int
	// TruthUndecidable is the number of distinct intra-repo bytecode facts
	// that could not be keyed at this precision, and are therefore outside
	// the denominator. Reported so a recall figure can never be read as
	// covering facts the oracle never keyed.
	TruthUndecidable int
	// Matched is the number of confirmed calls that appear in the truth set
	// (the recall numerator).
	Matched int
}

// Recall is Matched/TruthIntra (0 when there is nothing to recall).
func (r Result) Recall() float64 {
	if r.TruthIntra == 0 {
		return 0
	}
	return float64(r.Matched) / float64(r.TruthIntra)
}

// Sound reports the zero-tolerance soundness verdict. Abstentions do NOT make
// a result unsound — but they do bound what "sound" covers, which is why
// Format always prints them.
func (r Result) Sound() bool { return len(r.Violations) == 0 }

// Compare checks the confirmed calls against the bytecode truth at ByName —
// the graph-store precision, and the pre-SW-172 behaviour.
func Compare(confirmed, truth []Call) Result {
	return CompareAt(confirmed, truth, ByName)
}

// CompareAt checks the confirmed calls against the bytecode truth at one
// precision. Soundness: every confirmed call must be a distinct intra-repo
// truth fact. Recall is measured over the intra-repo truth facts.
//
// The three-way verdict, stated exactly:
//
//   - the confirmed fact cannot be keyed at p  → ABSTAIN (its own reason)
//   - it matches a truth fact at p             → MATCHED
//   - no match at p, but a truth fact matches
//     one precision COARSER and cannot itself
//     be keyed at p                            → ABSTAIN (the truth's reason)
//   - otherwise                                → VIOLATION
//
// The third case is the one that keeps the oracle honest: "the truth set
// disagrees with you" and "the truth set cannot answer at this precision" are
// different findings, and reporting the second as a stop-ship counterexample
// would be a fabricated defect.
func CompareAt(confirmed, truth []Call, p Precision) Result {
	res := Result{Precision: p}

	truthSet := map[callKey]struct{}{}
	coarseUndecidable := map[callKey]string{}
	coarse, hasCoarse := p.coarser()
	seenTruth := map[callKey]struct{}{}
	for _, c := range truth {
		if c.CalleeFile == "" {
			continue // external callee — not an intra-repo fact
		}
		if reason, undecidable := c.undecidableAt(p); undecidable {
			if _, dup := seenTruth[c.key(ByName)]; !dup {
				res.TruthUndecidable++
			}
			seenTruth[c.key(ByName)] = struct{}{}
			if hasCoarse {
				coarseUndecidable[c.key(coarse)] = reason
			}
			continue
		}
		truthSet[c.key(p)] = struct{}{}
	}
	res.TruthIntra = len(truthSet)

	confSet := map[callKey]Call{}
	for _, c := range confirmed {
		confSet[c.key(p)] = c
	}

	for k, c := range confSet {
		if reason, undecidable := c.undecidableAt(p); undecidable {
			res.Abstained = append(res.Abstained, c)
			res.addReason(reason)
			continue
		}
		if _, ok := truthSet[k]; ok {
			res.Matched++
			continue
		}
		if hasCoarse {
			if reason, ok := coarseUndecidable[c.key(coarse)]; ok {
				res.Abstained = append(res.Abstained, c)
				res.addReason(reason)
				continue
			}
		}
		res.Violations = append(res.Violations, c)
	}
	sortCalls(res.Violations)
	sortCalls(res.Abstained)
	return res
}

func (r *Result) addReason(reason string) {
	if reason == "" {
		reason = "unnamed"
	}
	if r.AbstainReasons == nil {
		r.AbstainReasons = map[string]int{}
	}
	r.AbstainReasons[reason]++
}

// Format renders a deterministic report for CI logs.
func (r Result) Format() string {
	var b strings.Builder
	if r.Sound() {
		fmt.Fprintf(&b, "jvm-groundtruth SOUND [%s] — all %d judged confirmed calls are backed by bytecode; recall %d/%d = %.1f%%.\n",
			r.Precision, r.Matched, r.Matched, r.TruthIntra, r.Recall()*100)
	} else {
		fmt.Fprintf(&b, "jvm-groundtruth SOUNDNESS FAILURE [%s] — %d confirmed call(s) have NO bytecode fact (each a JVMSOUND-0xx stop-ship):\n", r.Precision, len(r.Violations))
		for _, c := range r.Violations {
			fmt.Fprintf(&b, "  - %s.%s --calls--> %s.%s arity=%d params=%q descriptor=%q\n",
				c.CallerFile, c.CallerMethod, c.CalleeFile, c.Callee, c.CalleeArity, c.CalleeParams, c.CalleeDescriptor)
		}
	}
	// Always printed, sound or not: a verdict whose coverage is unstated is
	// not a verdict a reader can size.
	fmt.Fprintf(&b, "  abstained: %d confirmed call(s) not judged at this precision; %d bytecode fact(s) outside the recall denominator.\n",
		len(r.Abstained), r.TruthUndecidable)
	for _, name := range sortedReasons(r.AbstainReasons) {
		fmt.Fprintf(&b, "    %s: %d\n", name, r.AbstainReasons[name])
	}
	return b.String()
}

func sortedReasons(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
		if cs[a].Callee != cs[b].Callee {
			return cs[a].Callee < cs[b].Callee
		}
		if cs[a].CalleeArity != cs[b].CalleeArity {
			return cs[a].CalleeArity < cs[b].CalleeArity
		}
		return cs[a].CalleeParams < cs[b].CalleeParams
	})
}
