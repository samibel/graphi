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
	// AbstainBytecodeAnonymousCtorParams: the call constructs a javac-minted
	// ANONYMOUS SUBCLASS. The constructed TYPE is recoverable (it is the
	// anonymous class's superclass, which is what the source `new X(){…}`
	// named), so the fact stands at ByName — but the DESCRIPTOR is the synthetic
	// constructor's, carrying the enclosing instance and every captured local,
	// and the source-level parameter list cannot be recovered from it. So the
	// two finer precisions decline rather than compare an arity the source never
	// wrote. SW-173 found this on guava, where it accused 29 correct calls at
	// by-arity and 12 at by-signature.
	AbstainBytecodeAnonymousCtorParams = "bytecode_anonymous_ctor_synthetic_params"
	// AbstainBytecodeCallerNotAlignable: the bytecode's CALLER is a method of a
	// class javac minted for an ANONYMOUS or LOCAL class body. graphi mints no
	// node for such a body and attributes the call to the enclosing
	// declaration, and the enclosing method's name is NOT recoverable from
	// `javap -c -p -s` — it lives in the EnclosingMethod attribute, which only
	// `-v` prints. The two sides therefore cannot be aligned on the caller, so
	// a confirmed call that agrees on everything else abstains rather than
	// being accused of a call the bytecode plainly makes.
	AbstainBytecodeCallerNotAlignable = "bytecode_caller_not_alignable"

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

// coarserThan returns EVERY precision strictly coarser than p, finest first.
// CompareAt uses it to tell "the truth set disagrees" from "the truth set
// declines": when a confirmed call finds no match at p but the truth holds a
// fact that matches at some coarser key and is itself undecidable at p, the
// honest verdict is an abstention, not a counterexample.
//
// Every level, not just the next one down. A truth set captured without `-s`
// is undecidable at by-arity AND at by-signature, so its facts can only be
// keyed at by-name; a single-step fallback from by-signature looks for a
// by-arity key such a fact never has, finds nothing, and ACCUSES a confirmed
// call of contradicting a truth set that was never able to answer the question
// — which is exactly the fabricated stop-ship this branch exists to prevent.
func (p Precision) coarserThan() []Precision {
	out := make([]Precision, 0, int(p))
	for q := p - 1; q >= ByName; q-- {
		out = append(out, q)
	}
	return out
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

	// owner and overloads are BINDER-SIDE PLUMBING for DeclaredMethods.Verify,
	// not facts about the call — unexported precisely so no verdict can key on
	// them and no caller outside this package can supply them.
	//
	// owner is the JVM internal name of the class the binder says declares the
	// callee ("a/Rate", "a/Outer$Key"). overloads is the rendered parameter
	// list of EVERY declaration in that class sharing the callee's compiled
	// name AND its declared parameter count — its collision set — or nil when
	// one of them could not be rendered. Together they are what makes the
	// rendering check MEMBER-scoped rather than file-scoped; see Verify.
	owner     string
	overloads []string

	// callerSynthetic marks a BYTECODE-side fact whose caller class is one
	// javac minted for an anonymous or local class body; see
	// AbstainBytecodeCallerNotAlignable. Unexported for the same reason as the
	// two above: it steers an abstention, it is not a fact about the call.
	callerSynthetic bool
}

// syntheticNestedClass reports whether an internal class name is one javac
// MINTED for an anonymous or local class (`a/App$1`, `a/App$1L`). The marker
// is a '$' followed immediately by a DIGIT, which is exact rather than
// heuristic for the question it is asked: a Java identifier cannot begin with a
// digit, so no user-written nested type can produce that pair.
//
// It is used for ONE thing — marking a bytecode fact whose CALLER class graphi
// mints no node for (callerSynthetic). Over-matching there is safe by
// construction: the fact stays in the truth set and only an extra
// caller-agnostic ABSTENTION is registered, so a wrong yes costs recall and can
// never accuse. The constructor redirect asks a different and much less
// forgiving question and uses anonymousNestedClass instead.
func syntheticNestedClass(internal string) bool {
	for i := 0; i+1 < len(internal); i++ {
		if internal[i] == '$' && internal[i+1] >= '0' && internal[i+1] <= '9' {
			return true
		}
	}
	return false
}

// anonymousNestedClass reports whether an internal class name is one javac
// minted for an ANONYMOUS class specifically: some segment between '$'
// separators is ENTIRELY digits (`a/App$1`, `a/Outer$1$2`).
//
// SW-173 round 1, minor-1. The constructor redirect used the digit-PREFIX test
// above, which also matches javac's LOCAL classes (`a/App$1L`) and the legal
// user-declared type `a.Foo$1Bar` — and for those the redirect does not merely
// abstain, it REWRITES the fact to name the superclass and the superclass's
// file, so the truth set gains a fact claiming the source constructed a type it
// never named. That is a fabrication, latent today only because graphi mints no
// confirmed constructor edge for either shape.
//
// A local class `class L {}` compiles to `Outer$1L`: javac's disambiguating
// counter, then the SOURCE name. So `1L` is digit-prefixed but not all-digits,
// and requiring the whole segment to be digits is exactly the anonymous-class
// test — `new X(){…}` is the only construct whose class has no source name at
// all, which is why javac has nothing to append.
func anonymousNestedClass(internal string) bool {
	for _, seg := range strings.Split(internal, "$")[1:] {
		if seg == "" {
			continue
		}
		allDigits := true
		for i := 0; i < len(seg); i++ {
			if seg[i] < '0' || seg[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
}

// callerAgnosticKey is c's key at p with the CALLER METHOD removed — the most
// specific key available when the bytecode names a caller graphi cannot name.
func (c Call) callerAgnosticKey(p Precision) callKey {
	k := c.key(p)
	k.callerMethod = ""
	return k
}

// declinedKey identifies a truth fact by the only parties a FAILED owner walk
// still knows: who called, and under what name. The declaring file is exactly
// the thing that could not be determined, so it cannot be part of this key —
// which is also why this is a separate, deliberately coarse channel and never
// a comparison key.
type declinedKey struct {
	callerFile   string
	callerMethod string
	callee       string
}

func (c Call) declinedKey() declinedKey {
	return declinedKey{
		callerFile:   c.CallerFile,
		callerMethod: c.CallerMethod,
		callee:       c.Callee,
	}
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
//
// SINGLE-EXEC CALLERS ONLY (SW-173 round 1, minor-7). This function trusts its
// argument to be a COMPLETE disassembly of the classes whose calls should be
// resolvable; an owner missing from it resolves to no source path and silently
// leaves the truth set, which is exactly the incomplete-capture forge
// TestIncompleteCapture_ForgesWithoutTheGate_RefusedWithIt reproduces. Any
// caller that assembles javap output from MORE THAN ONE exec must go through
// NewCapture, which refuses a merge unless every class the compiler wrote is
// present. That obligation is a convention this comment states, not an
// invariant the type system enforces.
func ParseJavap(out []byte) ([]Call, error) {
	classes, err := parseClasses(out)
	if err != nil {
		return nil, err
	}
	classSource := map[string]string{}
	// hasDescriptorTable is a GLOBAL property of the capture: was `-s` passed?
	// It must be answered globally, because per-type it is not answerable at
	// all — "this class carried no `descriptor:` lines" is the shape of an
	// `-s`-less capture AND the shape of a type that genuinely declares
	// nothing, and reading the second as the first accuses correct code (see
	// resolveOwner). Boolean OR over the map, so map order does not matter.
	hasDescriptorTable := false
	for internal, c := range classes {
		classSource[internal] = c.source
		if len(c.decls) > 0 {
			hasDescriptorTable = true
		}
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
		// The caller carries kotlinc's multifile mangling too, and on this side
		// it is the DOMINANT form: a private top-level function is DECLARED as
		// `builtinParametrizedSerializer$SerializersKt__SerializersKt`, so every
		// fact naming it as the caller disagreed with graphi about a method both
		// sides had right. Same exact rule as the callee side.
		//
		// There is deliberately NO decline fallback on this side, and the
		// asymmetry is not an oversight: a truth fact nobody claims is harmless
		// (an accusation needs a CONFIRMED call with no truth fact, never a
		// truth fact with no confirmed call), so leaving an unrecognised
		// `$`-bearing caller name alone costs nothing. Rewriting one that was
		// never mangled is what costs — see demangleMultifilePart.
		//
		// kotlinc's VALUE-CLASS mangling reaches this side independently of the
		// multifile one, and it is where JVMHARN-001 lives: a function taking an
		// inline-class parameter is DECLARED as `serialize-2TYgG_w`, so javap
		// attributes its body's invokes to that name while graphi answers
		// `serialize`, which is what the source declares. Same defect class,
		// same safe direction, same shape of fix — see demangleValueClass for
		// the guard and the named residual.
		callerMethod := r.callerMethod
		if base, ok := demangleMultifilePart(classes, callerMethod, r.callerClassInternal); ok {
			callerMethod = base
		}
		if base, ok := demangleValueClass(classes, callerMethod, r.callerClassInternal); ok {
			callerMethod = base
		}
		callee := r.calleeName
		ctor := callee == "<init>"
		// anonCtorReason is set when a constructor call names a class javac
		// MINTED for an anonymous subclass, and the type the SOURCE named
		// cannot be recovered from this capture.
		anonCtorOwner, anonCtorReason := "", ""
		if ctor {
			// SW-173, found at corpus scale on guava. `new TypeTable() { … }`
			// does not construct TypeTable — it constructs a synthetic anonymous
			// subclass, and javac writes `invokespecial TypeResolver$2.<init>`.
			// Normalising that owner to its own simple name yields the callee
			// "2", while graphi correctly reports the constructed type the
			// source names, TypeTable. Four forged stop-ships on guava
			// (TypeResolver$TypeTable, TypeResolver$WildcardCapturer and two
			// AbstractScheduledService$Scheduler cases) came from exactly this.
			//
			// The type the source named is the anonymous class's SUPERCLASS —
			// exactly, not heuristically, because `new X(){…}` with X a class
			// compiles to an anonymous class extending X. So redirect to it, and
			// take its file too: the anonymous class's own SourceFile is the
			// file holding the `new` expression, which is the CALLER's file and
			// not necessarily where X is declared.
			//
			// When X is an INTERFACE the anonymous class extends Object and
			// merely implements X, and `javap -c -p -s` prints no implements
			// clause this parser reads — so the constructed type is genuinely
			// unrecoverable and the fact DECLINES rather than guessing.
			//
			// ANONYMOUS specifically, not merely javac-minted (SW-173 round 1,
			// minor-1): the redirect rewrites the fact to name the superclass
			// and the superclass's FILE, which is only the type the source named
			// when the class has no source name of its own. A local class
			// (`a/App$1L`) and the legal user type `a.Foo$1Bar` both have one,
			// and redirecting either fabricates a construction the source never
			// wrote.
			if anonymousNestedClass(r.calleeOwnerInternal) {
				anonCtorOwner, anonCtorReason = anonymousCtorTarget(classes, r.calleeOwnerInternal)
			}
			if anonCtorOwner != "" {
				callee = simpleName(anonCtorOwner)
			} else if anonCtorReason == "" {
				callee = simpleName(r.calleeOwnerInternal)
			}
		}

		// JVM method resolution (JVMS 5.4.3.3): the constant-pool ref names
		// the SYMBOLIC owner — for `d.m(1)` where m is inherited, javac writes
		// `a/Derived.m`, not `a/Base.m` — and the JVM then searches the class
		// and its superclasses for the (name, descriptor). graphi's confirmed
		// edge points at the DECLARING method, so the symbolic owner must be
		// walked to the declaring class or the two sides disagree on a fact
		// they both get right. Without this walk the oracle manufactures FALSE
		// counterexamples on every inherited call.
		ownerInternal, ownerReason := resolveOwner(classes, hasDescriptorTable, r.calleeOwnerInternal, r.calleeName, r.calleeDescriptor)
		// An anonymous-subclass constructor overrides the walk: the walk would
		// stop at the synthetic class, which declares its own <init>, and name
		// the wrong type and the wrong file.
		if anonCtorReason != "" {
			ownerInternal, ownerReason = r.calleeOwnerInternal, anonCtorReason
		} else if anonCtorOwner != "" {
			ownerInternal, ownerReason = anonCtorOwner, ""
		}
		// An owner that IS in the capture but carries no source path is
		// intra-repo and unattributable — not external. Saying nothing would let
		// it score as external and drop out of the truth set, which is the
		// incomplete-capture forge by another route; declining routes it into
		// the truthDeclined rescue instead, where the confirmed call abstains.
		if ownerReason == "" {
			if ci, known := classes[ownerInternal]; known && ci.source == "" {
				ownerReason = AbstainBytecodeOwnerUnresolved
			}
		}
		// A callee name containing '$' is a name the COMPILER minted, not one
		// any source declares: kotlinc mangles a private top-level function into
		// `serializerByKTypeImpl$SerializersKt__SerializersKt` when it lowers a
		// multifile class. graphi reports the name the source wrote, so keying
		// the mangled form against it manufactures a disagreement about a call
		// both sides got right. Constructors are exempt: their callee has
		// already been normalised to a simple type name above.
		if !ctor {
			if base, ok := demangleMultifilePart(classes, callee, r.calleeOwnerInternal); ok {
				callee = base
			} else if strings.Contains(callee, "$") {
				ownerReason = AbstainBytecodeOwnerUnresolved
			}
			// The value-class scheme reaches the callee side whenever the call
			// target takes an inline-class parameter. There is deliberately no
			// `-`-bearing decline fallback mirroring the `$` one above: `-` is
			// not a legal identifier character outside backquotes, so an
			// unrecognised dash-name is not by itself evidence of a
			// compiler-minted name the way an unrecognised `$`-name is.
			if base, ok := demangleValueClass(classes, callee, r.calleeOwnerInternal); ok {
				callee = base
			}
		}

		c := Call{
			CallerFile:       callerFile,
			CallerMethod:     callerMethod,
			CalleeFile:       classSource[ownerInternal], // "" = external
			Callee:           callee,
			CalleeCtor:       ctor,
			CalleeDescriptor: r.calleeDescriptor,
			CalleeArity:      ArityUnknown,
			CalleeParams:     SigUnknown,
			ArityReason:      ownerReason,
			ParamsReason:     ownerReason,
			callerSynthetic:  syntheticNestedClass(r.callerClassInternal),
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
		if ownerReason == "" && anonCtorOwner != "" {
			// SW-173, guava: the owner was redirected to the type the source
			// named, but the DESCRIPTOR still belongs to the synthetic anonymous
			// constructor, which javac gives extra parameters for the enclosing
			// instance and every captured local. `new ImmutableList<E>() {…}` in
			// CartesianList.get compiles to `CartesianList$1.<init>
			// (Lcom/google/common/collect/CartesianList;I)V` — arity 2 where the
			// source wrote 0. Using it accused 29 correct calls at by-arity and
			// 12 at by-signature. The source-level signature is not recoverable
			// from the synthetic one, so the fact stays decidable at ByName
			// (where it is right) and declines at both finer levels.
			c.ArityReason = AbstainBytecodeAnonymousCtorParams
			c.ParamsReason = AbstainBytecodeAnonymousCtorParams
		} else if ownerReason == "" {
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
//
// hasDescriptorTable says whether the CAPTURE carried `descriptor:` lines at
// all, and it is a parameter rather than something inferred here because it
// CANNOT be inferred from one class. An empty decl set is the shape of an
// `-s`-less capture and equally the shape of a type that genuinely declares
// nothing — and only an INTERFACE can be genuinely empty, because javac gives
// every class a default constructor, so a class always declares at least
// `<init>`. `interface B extends A {}` is therefore exactly three tokens of
// legal Java that used to be read as "captured without -s"; that returned
// AbstainBytecodeNoDescriptors, which does NOT zero CalleeFile and does NOT
// open the truthDeclined rescue, so the truth fact kept the SYMBOLIC owner's
// path, stayed decidable at ByName, and CONTRADICTED graphi's correct answer.
// Correct Java, correct binding, fabricated stop-ship, at every precision.
func resolveOwner(classes map[string]*classInfo, hasDescriptorTable bool, owner, name, desc string) (string, string) {
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
		if !hasDescriptorTable {
			// NO class anywhere carried a `descriptor:` line: captured without
			// -s. Keep the symbolic owner — the pre-SW-172 answer.
			return owner, AbstainBytecodeNoDescriptors
		}
		// The capture HAS a descriptor table, so this type really declares
		// nothing and the ref names a member it INHERITED. For an empty
		// interface that member comes down the superinterface chain, which
		// this walk does not model (JVMS 5.4.3.4, maximally-specific), so the
		// honest answer is to DECLINE — the same channel an interface default
		// already takes — rather than to accuse.
		return owner, AbstainBytecodeOwnerUnresolved
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
			// SW-173, found at corpus scale on guava: stopping here is only
			// correct if this declaration is a REAL one. javac also emits
			// SYNTHETIC BRIDGE methods, which declare the same (name,
			// descriptor) in a subclass purely to satisfy a supertype, and
			// `javap -c -p -s` does not print ACC_SYNTHETIC — so a bridge is
			// byte-identical to a genuine override in this capture.
			//
			// guava's `AbstractGraph extends AbstractBaseGraph implements Graph`
			// is the live case. AbstractGraph declares no `edges()` IN SOURCE,
			// but javac gives it a bridge, so `edges()` inside equals/hashCode/
			// toString compiles to `invokevirtual AbstractGraph.edges` and this
			// walk stopped at AbstractGraph — naming AbstractGraph.java as the
			// declaring file when the source declaration is in
			// AbstractBaseGraph.java. graphi answered AbstractBaseGraph.java,
			// which is CORRECT, and was accused at all three precisions. Three
			// forged stop-ships against correct code, on the first corpus-scale
			// run.
			//
			// Since the two cases are indistinguishable here, the honest answer
			// when the name is ALSO declared further up the chain is to decline.
			// That routes the fact into the truthDeclined channel, where the
			// confirmed call is ABSTAINED rather than accused.
			//
			// WHAT THIS DECLINES, stated exactly (SW-173 round 1, MAJOR-3 —
			// the earlier wording said "genuine overrides called from within
			// the overriding class", which understates it by a lot). It fires
			// on ANY call whose static receiver type declares the same (name,
			// descriptor) as some supertype — i.e. every call to an overridden
			// method made through the overriding type, from anywhere, plus
			// self-calls, plus calls through a type further down a chain. That
			// is the ordinary way an overridden method is called.
			//
			// WHAT IT COSTS, measured on guava at this commit rather than
			// described (both runs at capture digest 7f1aa8d6…, this single
			// condition neutered and nothing else changed):
			//
			//	by-name          guard ON      guard OFF
			//	counterexamples         0              3
			//	matched              4721           4932
			//	truth facts         14505          15539
			//	abstained             315            101
			//	  owner_unresolved    271             65
			//
			// So 3 forged stop-ships are bought with 211 confirmed calls moved
			// from judged to abstained (4.1% of guava's 5121 confirmed) and 206
			// extra `bytecode_owner_unresolved` abstentions — 76% of that
			// bucket, 65% of the whole by-name abstention budget — plus 1034
			// truth facts leaving the recall denominator. Roughly 70 declines
			// per forge closed. Note the RATIO moves the other way (31.7% →
			// 32.5%) because the denominator shrinks faster than the numerator:
			// the guard makes recall look better while covering less, which is
			// why the absolute counts are the ones published.
			//
			// By this programme's rule that is still the right trade — an
			// oracle that accuses correct code is worse than one that declines
			// — but it is a large one, and it widens the truthDeclined channel
			// that SW-172's round-2 MAJOR-9 showed can swallow a real defect.
			// The precise discriminator is ACC_SYNTHETIC, which needs the
			// `javap -v` capture upgrade (JVMCAP-001 on the backlog).
			if ci.super != "" && declaredAbove(classes, ci.super, want) {
				return cur, AbstainBytecodeOwnerUnresolved
			}
			return cur, ""
		}
		if ci.super == "" {
			return cur, AbstainBytecodeOwnerUnresolved
		}
		cur = ci.super
	}
}

// demangleMultifilePart strips kotlinc's MULTIFILE-CLASS name mangling from a
// method name, and only that.
//
// When a Kotlin file declares `@file:JvmName("SerializersKt")
// @file:JvmMultifileClass`, kotlinc lowers its top-level declarations into a
// part class `SerializersKt__SerializersKt` and renames every PRIVATE top-level
// function to `<name>$<partClassSimpleName>` — so `serializerByKTypeImpl`
// becomes `serializerByKTypeImpl$SerializersKt__SerializersKt`, on the
// declaration AND at every call site. graphi reports the name the source wrote,
// so comparing the two forms manufactures a disagreement about a call both sides
// resolved correctly. SW-173 measured 48 such forged stop-ships on
// kotlinx.serialization, on both the caller and the callee side.
//
// # Why the OWNER is tested, and not only the suffix
//
// SW-173 round 1, found by the reviewer: matching the suffix alone ACCUSES
// CORRECT CODE. `$` is a legal Java identifier character (JLS 3.8), so
//
//	package a; public class Bar { public int foo$Bar() { return 1; } }
//
// declares a real method whose name ends in `$` + its own owner's simple name.
// graphi answers `foo$Bar`, which is what the source wrote; the suffix-only rule
// rewrote the bytecode fact to `foo` and forged a stop-ship at by-name against a
// call the parent commit scored correctly. The caller side forged the same way
// (`run$App` declared in class `App`).
//
// So the owner must be a class kotlinc could actually have MINTED this mangling
// for. Two properties are checked, both exact rather than heuristic:
//
//  1. Its simple name contains `__`. A multifile part class is always named
//     `<Facade>__<PartFile>` — the double underscore is kotlinc's own separator
//     and is what distinguishes a part class from any ordinary type.
//  2. It was compiled from a `.kt` file in THIS capture. The lowering is
//     kotlinc's; a javac-compiled owner cannot have produced it, whatever it is
//     named. An owner absent from the capture is not demangled either: an
//     external class is not one this repository's compiler lowered.
//
// A user-declared `foo$Bar` therefore no longer satisfies the rule, and neither
// does `m$Foo__Bar` in a Java class named `Foo__Bar` — both are left alone, and
// on the callee side they decline (see the caller of this function).
//
// # The residual, MEASURED and disclosed rather than claimed away
//
// A KOTLIN-compiled class whose simple name contains `__` and which declares a
// member whose name ends in `$` + that same simple name is still rewritten. The
// first draft of this comment claimed Kotlin could not spell such a member. That
// claim is false, and it was killed by trying it: kotlinc 1.9.24 accepts `$`
// inside a backquoted identifier, so
//
//	package k
//	class Foo__Bar { fun `x$Foo__Bar`(): Int = 1 }
//
// compiles and produces exactly the shape the rule matches
// (TestDemangle_KotlinCanSpellTheResidual measures it). The residual is
// REACHABLE, it is recorded as blind spot #17 in SW-172's AC-7 register, and it
// is not asserted closed — SW-173's round-1 finding was precisely a safety
// property asserted without being attacked. It is narrow (two `_`, a Kotlin
// source, and a member named after its own owner) and the corpus contains no
// instance, but "narrow" is a frequency claim, not a soundness one.
func demangleMultifilePart(classes map[string]*classInfo, name, ownerInternal string) (string, bool) {
	simple := simpleName(ownerInternal)
	if !strings.Contains(simple, "__") {
		return name, false
	}
	ci, known := classes[ownerInternal]
	if !known || !strings.HasSuffix(ci.source, ".kt") {
		return name, false
	}
	base, ok := strings.CutSuffix(name, "$"+simple)
	if !ok || base == "" {
		return name, false
	}
	return base, true
}

// demangleValueClass strips kotlinc's VALUE-CLASS (inline-class) name mangling
// from a method name. It is the JVMHARN-001 fix, and it is in the HARNESS
// because that is where JVMHARN-001 lives: javap prints the mangled
// DECLARATION name, so no rule on the product side can change it.
//
// # The defect
//
// kotlinc renames any function taking an inline-class parameter to
// `<name>-<hash>` and emits a bridge under the plain name. The rename lands on
// the DECLARATION, so every invoke inside such a function's body is attributed
// by this oracle to the mangled caller while graphi answers the name the source
// declares. `docs/rc/jvm-corpus-compile-strategy.md` §6.2 measured this as
// cluster A on the kotlinx.serialization pin: 22 of that pin's 27 by-name
// unbacked calls, with the shapes `serialize-2TYgG_w`, `deserialize-BwKQO78`,
// `toBuilder-GBYM_sE`, `writeContent-Coi6ktg` and `computeIfAbsent-gIAlu-s`,
// against a source that declares `serialize` at `ValueClasses.kt:16`. Every one
// of the 27 is the oracle accusing correct code; none is a product defect.
//
// # The guard, and what it is inherited from
//
// The recognition rule alone is not enough: Kotlin lets a user declare a name
// containing `-` inside backquotes, so a hand-written backquoted `foo-abcd` is
// legal source and matches the shape exactly. Rewriting THAT would repeat
// SW-173's round-1 defect — accusing correct code by rewriting a name the source
// wrote. So the rewrite is accepted only when the plain name is ALSO declared as
// a member of the SAME class in this capture.
//
// The premise is that kotlinc emits the bridge under the plain name, which is
// what §6.2's cluster A row states and is not re-measured here. That the bridge
// lands in the same class is this function's own conservative reading of it: a
// value-class function whose plain-name sibling is elsewhere (or absent, as for
// one that overrides nothing) is DECLINED. Declining costs recall, never
// soundness — see the asymmetry demangleMultifilePart states. Two further
// conditions, both cheap and both failing toward decline:
//
//   - The owner must be in the capture and compiled from a `.kt` file. The
//     lowering is kotlinc's; a javac-compiled or external owner cannot have
//     produced it.
//   - The declaration table must be populated, which it is only for a `javap -s`
//     capture (parseClasses fills decls from `descriptor:` lines). Without it the
//     guard cannot be evaluated and the rewrite declines.
//
// # What this does NOT reach, and what it can over-reach
//
// Not reached, both measured on the kotlinx pin and both left standing as the
// disclosed residual: cluster C's single `append-7apg3OU$main` (`internal`
// visibility mangling compounded with the value-class hash — `$` is not a
// base-64 character, so valueClassBridgeName declines), and cluster B's four
// `Encoder$DefaultImpls` interface-lowering rows, which are not a name-mangling
// shape at all.
//
// Over-reach, named rather than claimed away: a Kotlin class that declares BOTH
// a backquoted dash-name and a plain member with exactly the prefix satisfies
// the rule and IS rewritten. The reach is wider than "hash-shaped", because
// isBase64Char accepts `-`: the tail after the FIRST dash need only be four or
// more characters of [A-Za-z0-9+_-], so a backquoted `parse-json-value` beside a
// plain `parse` qualifies exactly as `foo-abcd` beside `foo` does. Both are
// pinned in TestDemangleValueClass_Guards. Tightening the alphabet is not
// available: `computeIfAbsent-gIAlu-s` is one of the measured shapes and its
// hash contains a `-`. The corpus contains no instance of the over-reach, but
// that is a frequency claim, not a soundness one — the same disclosure blind
// spot #17 carries for the multifile demangle.
//
// On the CALLEE path the rewrite can also mint a fact the source never wrote: a
// bridge's forward call to its own mangled real body becomes `serialize →
// serialize`. Under the asymmetry above that is not an accusation — an
// accusation needs a confirmed edge with no truth fact, and an extra truth fact
// can only back an edge — but it does cost the oracle its power to accuse a
// graphi self-edge on such a method. TestParseJavap_ValueClassCallerAndCallee
// asserts it as the expected outcome so it is on the record.
func demangleValueClass(classes map[string]*classInfo, name, ownerInternal string) (string, bool) {
	base := valueClassBridgeName(name)
	if base == "" {
		return name, false
	}
	ci, known := classes[ownerInternal]
	if !known || !strings.HasSuffix(ci.source, ".kt") {
		return name, false
	}
	if !declaresMemberNamed(ci, base) {
		return name, false
	}
	return base, true
}

// declaresMemberNamed reports whether ci declares a member called name at ANY
// descriptor. decls is keyed `name:descriptor`, and a descriptor never contains
// `:`, so the prefix test is exact.
func declaresMemberNamed(ci *classInfo, name string) bool {
	want := name + ":"
	for k := range ci.decls {
		if strings.HasPrefix(k, want) {
			return true
		}
	}
	return false
}

// valueClassBridgeName recognises kotlinc's value-class mangled shape and
// returns the bridge name (the prefix before the first dash), or "" when the
// name is not mangled. Split on the FIRST `-` because the hash alphabet
// contains `-` itself; the suffix must be at least 4 base-64 characters and the
// prefix a legal identifier.
//
// This is a VERBATIM COPY of engine/jvmresolve.valueClassBridgeName — the
// declaration text, not merely the behaviour. Importing the original instead
// would require exporting it, and that rename moves the product binary's digest:
// measured, not argued — renaming it to ValueClassBridgeName in
// engine/jvmresolve/hierarchy.go and rebuilding `./cmd/graphi` with
// `-trimpath -buildvcs=false` takes the digest from
// 0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf to
// fa8a2867d48986cddd5b7fc105eb68e7ef731e8ec50bb91a52a1c5d0d57b84cd, and SW-188
// must not move the product bytes the published parity candidate stands on.
// This package is not linked into cmd/graphi (`go list -deps ./cmd/graphi` does
// not contain it), so the copy itself is product-byte-neutral.
//
// The copy's cost is drift, and what pays it down is
// valueclass_drift_test.go::TestValueClassRule_IdenticalToJVMResolve, a
// SOURCE-IDENTITY guard: it parses both files and compares the copied
// declarations as printed syntax. A mirrored table cannot do that job —
// see that test's doc for the divergence a table stays green on, and for the
// two things source identity still does not catch.
func valueClassBridgeName(name string) string {
	i := strings.Index(name, "-")
	if i <= 0 || i >= len(name)-1 {
		return ""
	}
	// Suffix must be at least 4 base-64 chars; the base-64 alphabet kotlinc
	// uses includes letters, digits, `-`, `+`, `_`.
	suf := name[i+1:]
	if len(suf) < 4 {
		return ""
	}
	for j := 0; j < len(suf); j++ {
		c := suf[j]
		if !isBase64Char(c) {
			return ""
		}
	}
	// Prefix must be a valid Kotlin identifier (a name the user could have
	// declared, OR a name the compiler minted).
	pre := name[:i]
	if pre == "" || !isIdentStart(pre) {
		return ""
	}
	for j := 0; j < len(pre); j++ {
		if !isIdentCont(pre[j]) {
			return ""
		}
	}
	return pre
}

// isBase64Char, isIdentStart and isIdentCont are the rest of the copy; see
// valueClassBridgeName for why it is a copy and what guards it.
func isBase64Char(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' || c == '+' || c == '_'
}

func isIdentStart(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isIdentCont(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// anonymousCtorTarget maps a javac-minted anonymous class to the type the
// SOURCE `new` expression named: its superclass. It returns ("", reason) when
// that type is not recoverable from this capture — an anonymous class extending
// Object is one that implemented an INTERFACE, and the interface is not printed
// in a form this parser reads.
func anonymousCtorTarget(classes map[string]*classInfo, anon string) (owner, reason string) {
	ci, ok := classes[anon]
	if !ok || ci.super == "" || ci.super == "java/lang/Object" {
		return "", AbstainBytecodeOwnerUnresolved
	}
	if _, known := classes[ci.super]; !known {
		// The named supertype is outside the repository: an external
		// construction, which scores as external rather than as a fact.
		return "", AbstainBytecodeOwnerUnresolved
	}
	return ci.super, ""
}

// declaredAbove reports whether any class from `start` upwards declares `want`.
// It is the bridge-ambiguity test: a subclass declaration that is ALSO present
// on the superclass chain may be a genuine override or a synthetic bridge, and
// this capture cannot tell them apart.
//
// The walk is bounded by a seen-set rather than by trusting the chain to be
// acyclic: a malformed or adversarial capture must not hang the oracle.
func declaredAbove(classes map[string]*classInfo, start, want string) bool {
	seen := map[string]struct{}{}
	for cur := start; cur != ""; {
		if _, dup := seen[cur]; dup {
			return false
		}
		seen[cur] = struct{}{}
		ci, ok := classes[cur]
		if !ok {
			return false
		}
		if _, has := ci.decls[want]; has {
			return true
		}
		cur = ci.super
	}
	return false
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
			// SW-173, found at corpus scale on kotlinx.serialization: this used
			// to FABRICATE a path when javap printed no `Compiled from` line,
			// falling back to `simpleName + ".java"`. That invents a fact.
			//
			// Kotlin's multifile-class FACADE is the live case. `@file:JvmName
			// ("SerializersKt") @file:JvmMultifileClass` makes kotlinc emit a
			// facade class assembled from several source files, and a class with
			// no single source file carries no SourceFile attribute — so javap
			// prints no `Compiled from` for it. The fallback then claimed
			// `kotlinx/serialization/SerializersKt.java`, a path that exists in
			// no repository, and every call whose owner resolved to the facade
			// contradicted graphi's correct `Serializers.kt`. 50 forged
			// stop-ships on one pin, all against correct code.
			//
			// An unattributable class now gets NO path. That is not a silent
			// downgrade: the empty path is what ParseJavap reads as "not an
			// intra-repo fact", and it pairs with the explicit decline below so
			// the affected confirmed calls are ABSTAINED rather than accused.
			if src != "" && pkg != "" {
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
		// A method REF is tried BEFORE a method HEADER, deliberately. The two
		// shapes overlap (a ref to a method returning a reference type ends
		// with ';' and contains '('), and of the two possible mistakes only one
		// is recoverable: reading a header as a ref records nothing, while
		// reading a ref as a header both DROPS the invoke fact and re-parents
		// every later invoke in the method. parseMethodHeader now rejects ref
		// lines by shape, so this ordering is belt on braces — but it is the
		// belt that fails safe, so it is the one that runs first.
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
			continue
		}
		if name, ok := parseMethodHeader(trimmed); ok {
			curMethod = enclosingOfSynthetic(name)
			continue
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
// keywords — so NEITHER OF THOSE TWO can collide with a user's own method.
//
// THE GENERAL MAPPING CAN COLLIDE, and that is a known forge, disclosed rather
// than closed. `$` is a legal Java identifier character (JLS 3.8), so a user
// may declare `public int lambda$run$0(...)`; this function then rewrites the
// bytecode caller to `run` while graphi reports the declared name, and correct
// code is accused at all three precisions. Reproduced against javac 21. It is
// NOT fixed here because the two shapes are genuinely indistinguishable from
// `javap -c -p -s` alone — telling them apart needs the ACC_SYNTHETIC flag,
// which only `javap -v` prints, the same capture upgrade blind spots #6 and
// #12 need. Frequency in the SW-173 pins is essentially zero. See AC-7 blind
// spot #15.
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

// classKeywords precede a dotted class name in a javap type header. Each
// carries its trailing space, and typeKeywordAt requires a token boundary
// BEFORE it too — see there for the forge that taught us why.
var classKeywords = []string{"class ", "interface ", "enum "}

// typeKeywordAt finds the type keyword in a javap type header as a WHOLE
// TOKEN, returning its offset and the keyword matched. It scans by POSITION,
// left to right, so the earliest keyword wins regardless of the order
// classKeywords happens to list them in.
//
// Both properties are load-bearing, and neither was true before round 2.
// `$` and letters are legal in a Java type name, so a type name may CONTAIN a
// keyword: javap prints `public interface a.Subclass extends a.Base {`, and a
// scan for the first occurrence of the substring "class " anywhere in the line
// lands inside "Subclass ". The old code then read the class name as `extends`
// and its superclass as `a.Base`, so the REAL interface was never registered
// at all — every call to one of its methods lost its source path, scored as
// EXTERNAL, and vanished from the truth set. A missing truth fact is the
// direction that manufactures violations, and this one forged at by-name.
//
// Requiring a token boundary before the keyword is exact rather than a
// heuristic: every javap type-header modifier (public, protected, private,
// abstract, static, final, sealed, non-sealed, strictfp) is a distinct token,
// and no type name can BE `class`, `interface` or `enum`, because all three
// are reserved words (JLS 3.9). So the first whole-token match is always the
// real keyword. Scanning by position rather than by keyword additionally
// removes an order dependence: a name ending in "class" used to be tested
// against "class " before the line's own "interface " was ever considered.
func typeKeywordAt(trimmed string) (int, string, bool) {
	for i := 0; i < len(trimmed); i++ {
		if i > 0 && trimmed[i-1] != ' ' {
			continue // not at a token boundary
		}
		for _, kw := range classKeywords {
			if strings.HasPrefix(trimmed[i:], kw) {
				return i, kw, true
			}
		}
	}
	return 0, "", false
}

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
//
// The KEYWORD is found by typeKeywordAt, which requires a whole token — the
// same lesson one level down: a type NAME may contain a keyword too.
func parseClassHeader(trimmed string) (classHeader, bool) {
	if !strings.HasSuffix(trimmed, "{") {
		return classHeader{}, false
	}
	i, kw, ok := typeKeywordAt(trimmed)
	if !ok {
		return classHeader{}, false
	}
	rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed[i+len(kw):]), "{"))
	name, tail := splitOutsideBrackets(rest)
	if name == "" {
		// A type header always names a type after its keyword; a keyword with
		// nothing behind it is not one.
		return classHeader{}, false
	}
	return classHeader{name: name, super: extendsTarget(tail)}, true
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
//
// # The three javap line shapes that MASQUERADE as a method header
//
// A javap member declaration is source-like Java that ends in ';', so the
// naive test "ends with ';' and contains '('" also accepts two other kinds of
// line, and accepting either is worse than a blind spot — it REMOVES truth
// facts, which is the direction that manufactures violations:
//
//  1. javap -s's own `descriptor: (Ltax/Rate;)Ltax/Rate;`, which would be read
//     as a method named "descriptor:" and become the CALLER of the invokes
//     below it. Found by the live gate while SW-172 was being built.
//
//  2. ANY bytecode instruction whose constant-pool trailer names a member
//     RETURNING A REFERENCE TYPE:
//     `1: invokevirtual #7  // Method a/Derived.self:()La/Derived;`
//     `7: invokeinterface #21, 1 // InterfaceMethod a/HasSeed.seed:()La/Seed;`
//     Both end with ';' and contain '(', and the token before '(' carries a
//     '.', so they were read as a CONSTRUCTOR header. In parseInvokes that
//     both DROPPED the invoke fact and clobbered curMethod to `<init>`, so
//     every later invoke in the method was re-parented. This is the same bug
//     class as (1) and was PRE-EXISTING, invisible only because every fixture
//     in the harness returned a primitive or void — see
//     TestParseMethodHeader_RejectsInvokeLines.
//
//  3. A `// String …` / `// class …` ldc trailer containing parentheses.
//
// All three are rejected by SHAPE — a javap constant-pool trailer is the only
// thing here that carries `//`, and only a bytecode instruction begins with a
// decimal offset, which no Java declaration can — rather than by check ORDER,
// so a caller that scans in a different order cannot silently reintroduce any
// of them.
func parseMethodHeader(trimmed string) (string, bool) {
	if !strings.HasSuffix(trimmed, ";") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "descriptor: ") {
		return "", false
	}
	if strings.Contains(trimmed, "//") {
		return "", false
	}
	if hasBytecodeOffset(trimmed) {
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

// hasBytecodeOffset reports whether the line begins with javap's decimal
// instruction offset (`12: invokevirtual …`). No Java declaration can begin
// with a digit, so this is an exact discriminator, not a heuristic.
func hasBytecodeOffset(trimmed string) bool {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	return i > 0 && i < len(trimmed) && trimmed[i] == ':'
}

// refMarkers are the javap constant-pool trailers that name a METHOD ref.
//
// `// InterfaceMethod ` is not optional and not a nicety: javac writes it for
// every invoke whose resolved owner is an INTERFACE — `invokeinterface`
// through an interface-typed receiver, `invokestatic` on an interface static
// method, and `invokespecial` for `X.super.m()`. Matching only `// Method `
// dropped all three from the truth set entirely, and a MISSING truth fact is
// the direction that manufactures violations: correct code calling through any
// interface was accused at every precision. Found by adversarial fixture, not
// by the gate — every fixture in the harness called through classes.
//
// Field refs (`// Field …`) and invokedynamic bootstraps carry neither marker
// and stay excluded, which is right: graphi never confirms those either.
var refMarkers = []string{"// Method ", "// InterfaceMethod "}

// parseMethodRef reads the `// Method owner.name:desc` (or
// `// InterfaceMethod …`) trailer of an invoke instruction. Owner is an
// internal name (slashes) and may be ABSENT for a same-class call — javap
// prints `// Method rate:()I` with no owner then, and this returns owner "" so
// the caller substitutes the current class. Name may be a quoted "<init>".
//
// SW-172 (AC-3): the DESCRIPTOR is returned rather than discarded. It is the
// only place the callee's real, javac-chosen signature exists in this output,
// and throwing it away is what made the oracle blind to overload mis-binding.
func parseMethodRef(trimmed string) (owner, name, desc string, ok bool) {
	ref := ""
	for _, marker := range refMarkers {
		if i := strings.Index(trimmed, marker); i >= 0 {
			ref = trimmed[i+len(marker):]
			break
		}
	}
	if ref == "" {
		return "", "", "", false
	}
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
//     at SOME coarser precision and cannot
//     itself be keyed at p                     → ABSTAIN (the truth's reason)
//   - no match at p, but the truth set holds
//     an invoke from the same caller under the
//     same callee NAME whose OWNER it could
//     not resolve                              → ABSTAIN (the truth's reason)
//   - otherwise                                → VIOLATION
//
// The third and fourth cases are the ones that keep the oracle honest: "the
// truth set disagrees with you" and "the truth set cannot answer" are different
// findings, and reporting the second as a stop-ship counterexample would be a
// fabricated defect.
//
// The fourth case exists because a FAILED OWNER WALK and an EXTERNAL callee
// arrive here wearing the same clothes — CalleeFile "" — and conflating them
// made the oracle accuse every interface default method. An external callee is
// a fact the oracle has fully decided (graphi never confirms those, so there is
// nothing to compare). An owner-unresolved fact is the oracle DECLINING: the
// invoke is in the bytecode, but which intra-repo declaration it names could
// not be determined, because the class walk of JVMS 5.4.3.3 does not model
// 5.4.3.4's maximally-specific interface rule. Such a fact is still excluded
// from the recall denominator — it is not a fact the oracle can claim to have
// keyed — but it must not let a confirmed call be accused, because the oracle
// has no idea whether it is wrong.
func CompareAt(confirmed, truth []Call, p Precision) Result {
	res := Result{Precision: p}

	coarserLevels := p.coarserThan()
	truthSet := map[callKey]struct{}{}
	coarseUndecidable := make(map[Precision]map[callKey]string, len(coarserLevels))
	for _, q := range coarserLevels {
		coarseUndecidable[q] = map[callKey]string{}
	}
	truthDeclined := map[declinedKey]string{}
	// levels is p itself plus every coarser precision: an unalignable-caller
	// truth fact must be findable at whatever precision it CAN be keyed at.
	levels := append([]Precision{p}, coarserLevels...)
	unalignableCaller := make(map[Precision]map[callKey]string, len(levels))
	for _, q := range levels {
		unalignableCaller[q] = map[callKey]string{}
	}
	seenTruth := map[callKey]struct{}{}
	for _, c := range truth {
		if c.CalleeFile == "" {
			if c.ArityReason == AbstainBytecodeOwnerUnresolved {
				truthDeclined[c.declinedKey()] = AbstainBytecodeOwnerUnresolved
			}
			continue // external or unattributable — not an intra-repo fact
		}
		if c.callerSynthetic {
			for _, q := range levels {
				if _, undecidable := c.undecidableAt(q); undecidable {
					continue
				}
				unalignableCaller[q][c.callerAgnosticKey(q)] = AbstainBytecodeCallerNotAlignable
				// FINEST LEVEL ONLY. levels is finest-first, and the lookup
				// walks it the same way, so registering at every coarser level
				// too buys nothing and costs a great deal: the ByName
				// caller-agnostic key carries neither arity nor params, so one
				// local class calling `apply` would rescue ANY confirmed call
				// to `apply` from that file at ANY signature — which silently
				// swallowed the filed JVMSOUND-003 mis-binding. The coarse walk
				// is right for coarseReason, whose facts genuinely cannot be
				// keyed at p; these facts CAN be, by construction of this loop.
				break
			}
			// NOT `continue`: the fact is real and stays in the truth set, so a
			// confirmed call that DOES name the same caller still matches. Only
			// the caller-agnostic rescue is added.
		}
		if reason, undecidable := c.undecidableAt(p); undecidable {
			if _, dup := seenTruth[c.key(ByName)]; !dup {
				res.TruthUndecidable++
			}
			seenTruth[c.key(ByName)] = struct{}{}
			// Register at every coarser precision the fact CAN be keyed at,
			// not merely the next one down; see Precision.coarserThan.
			for _, q := range coarserLevels {
				if _, stillUndecidable := c.undecidableAt(q); stillUndecidable {
					continue
				}
				coarseUndecidable[q][c.key(q)] = reason
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
		if reason, ok := coarseReason(coarseUndecidable, coarserLevels, c); ok {
			res.Abstained = append(res.Abstained, c)
			res.addReason(reason)
			continue
		}
		if reason, ok := callerAgnosticReason(unalignableCaller, levels, c); ok {
			res.Abstained = append(res.Abstained, c)
			res.addReason(reason)
			continue
		}
		if reason, ok := truthDeclined[c.declinedKey()]; ok {
			res.Abstained = append(res.Abstained, c)
			res.addReason(reason)
			continue
		}
		res.Violations = append(res.Violations, c)
	}
	sortCalls(res.Violations)
	sortCalls(res.Abstained)
	return res
}

// coarseReason finds the coarsest-still-matching truth abstention for c,
// searching finest-first so the most specific reason wins.
func coarseReason(m map[Precision]map[callKey]string, levels []Precision, c Call) (string, bool) {
	for _, q := range levels {
		if reason, ok := m[q][c.key(q)]; ok {
			return reason, true
		}
	}
	return "", false
}

// callerAgnosticReason is coarseReason for the caller-unalignable channel,
// which keys everything EXCEPT the caller method.
func callerAgnosticReason(m map[Precision]map[callKey]string, levels []Precision, c Call) (string, bool) {
	for _, q := range levels {
		if reason, ok := m[q][c.callerAgnosticKey(q)]; ok {
			return reason, true
		}
	}
	return "", false
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
