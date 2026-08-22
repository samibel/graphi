// Package jvmbindrate is the CI-ONLY call-site binding-rate harness for the JVM
// languages (SW-175, W1.b). Like internal/jvmgroundtruth and internal/jvmcorpus
// it NEVER ships: nothing under cmd/, core/, engine/ or surfaces/ imports it,
// which is asserted mechanically by cionly_test.go against `go list -deps`.
//
// # Why this package exists: a numerator without a denominator
//
// ADR 0008's decision D2 needs a binding-rate threshold, and the figure quoted
// in its support — "3517 typed Kotlin sites" — is a NUMERATOR WITHOUT A
// DENOMINATOR. It says how many sites the binder bound; it does not say out of
// how many exist. 3517 of 3600 and 3517 of 35 000 are opposite findings about
// the same binder. The independent review (R6) named exactly this as the reason
// the threshold could not be set.
//
// # The denominator comes from the parse tree, never from the binder
//
// If the denominator were derived from anything the binder produced — bound
// sites plus the sites it skipped, say — the binder would be defining its own
// denominator and the rate would be TAUTOLOGICAL: every site the binder cannot
// even see would be invisible on both sides of the fraction, and the rate would
// approach 100 % as the binder's blind spots grew. So the denominator is
// counted by an INDEPENDENT walk of the tree-sitter CST (countInvocations
// below), which visits every node of every parsed file and knows nothing about
// tables, types, members or scopes.
//
// It is the same grammar the binder parses with — deliberately. Using a second
// parser would measure the disagreement between two parsers rather than the
// coverage of one binder. Independence here means independent OF THE BINDER,
// not of the grammar.
//
// # What counts as a call site, stated so it can be recomputed
//
// The denominator is INVOCATION EXPRESSIONS, and the node types were read off
// the REAL embedded grammar (gotreesitter v0.20.2) with a dump probe, not from
// upstream documentation. They are exactly the node types the binder's own
// walkers switch on, so the two sides of the fraction agree about what a call
// site IS and disagree only about whether it bound:
//
//	java    method_invocation            `helper()`, `a.f()`, `A.stat()`
//	        object_creation_expression   `new A(1)`, `new A(1){…}`
//	kotlin  call_suffix                  `helper()`, `a.f()`, `A(1)`, `l.map{…}`,
//	                                     and the calls inside `"${f()}"`
//
// Nesting is counted, not collapsed: `l.get(0).length()` is TWO Java call sites
// (an outer method_invocation whose object is an inner one) and `A(2).m(…)` is
// two Kotlin ones. That matches the binder, which recurses into receivers and
// arguments and reports the inner site separately.
//
// Kotlin counts `call_suffix` rather than `call_expression`, and that choice is
// a CORRECTION rather than a style preference — see countInvocations. Inside a
// string template the grammar emits no `call_expression` at all, so counting it
// silently dropped every call written in an interpolated string. The binder
// (body_kotlin.go, which switches on `call_expression`) cannot bind those sites
// either — which is exactly why they must be in the denominator. A denominator
// that shares the binder's blind spots is the tautology this package exists to
// avoid, and this is the form it actually took here.
//
// # What is deliberately EXCLUDED, and why each exclusion is also counted
//
// An exclusion that is not counted is a place to hide a bad rate, so every one
// of them is tallied in CSTCounts and published beside the rate, and
// WidestDenominator() adds every one that IS a call back so the size of the
// choice is visible rather than trusted.
//
//	java   explicit_constructor_invocation  `super(…)` / `this(…)`
//	kotlin constructor_delegation_call      `constructor(x) : this(x, 0)`
//
// These two are each other's twin. `class A : B(1)` is NOT the twin of
// `super(…)` — it is the PRIMARY constructor's superclass call, whose Java
// counterpart is `extends B`, which produces no invocation node at all. It is
// excluded and counted separately as DelegationCtorCall, and the asymmetry is
// published rather than papered over.
//
//	java   enum_constant   `enum E { A(1) }`   — invokes E(int), no call node
//	kotlin enum_entry      `enum class E { A(1) }`
//
//	kotlin infix_expression    `a shl b`   — a call iff `infix fun`
//	kotlin indexing_suffix     `a[i]`      — a call iff `operator fun get`/`set`
//	kotlin additive/multiplicative/prefix/postfix/equality/comparison/range/
//	       check(`in`)/augmented-assignment       — the OPERATOR CONVENTION
//
// Excluded for symmetry: Java's `a + b` and `a[i]` are not call nodes either,
// and the CST cannot tell a user-defined operator from a builtin. Counted, so
// the size of this Kotlin-only residual is published.
//
// # The enumeration, because patching named defects is not a method
//
// The first draft's denominator was wrong six ways; a review then found a
// SEVENTH (`indexing_expression` missing the write and interpolated forms) and
// an EIGHTH (the operator family counted nowhere) — and both were the SAME
// defect class as F1, the `call_suffix`/`call_expression` correction, recurring
// in a sibling counter and in a whole family. Fixing the two that were named
// would have left the method that missed them intact, so the node types are no
// longer chosen by inspection. They are chosen against an ENUMERATION:
//
//	every named symbol in the embedded grammar   java 166, kotlin 198
//	  (gts.Language.SymbolNames, filtered by SymbolMetadata.Named)
//	× every one that OCCURS on the three pins    java 125, kotlin 141
//	  → each classified into exactly one of four buckets, no residue:
//
//	  D  in the primary denominator      java   2   kotlin   1
//	  W  a call, in WidestDenominator    java   2   kotlin  15
//	  P  synthesized protocol, measured  java   2   kotlin   3
//	     and in NO denominator
//	  N  not a call, explicitly listed   java 119   kotlin 122
//
// The java column read 120 / N 114 for one round because the intersection was
// taken against GUAVA alone and published as the three pins'. It is 125 across
// all three; the five extra are kotlinx.serialization's JPMS module-descriptor
// types and are classified `bucketNotACall` in grammar_enumeration.go, which
// carries the per-corpus measurement. No rate moved — none of the five can be a
// call — but the guard was red on the corpus and no configuration ran it there.
//
// The N bucket is an EXPLICIT list (grammar_enumeration.go), not a default.
// That is the whole point: with a default, a grammar upgrade that adds a
// call-bearing node type is classified as "not a call" by silence, which is
// precisely how the first eight defects got in.
// TestGrammarEnumeration_ClassifiesEveryOccurringNodeType walks the same symbol
// table at test time and fails on any occurring node type in no bucket.
//
// 41 java and 57 kotlin named symbols never occur on the pins and are therefore
// unclassified; the test does not require them, because classifying a node type
// no corpus produces would be a guess recorded as a decision.
//
// What the enumeration does NOT cover, stated because it is the residual that
// matters: it classifies node types, not TYPES. `a + b` is counted as an
// operator-convention call whether `a` is an `Int` (an intrinsic, no callee) or
// a `BigDecimal` (a real `plus` call). The CST cannot tell, so the family is
// counted in the widest denominator and excluded from the primary one, and both
// sizes are published rather than a split being invented.
//
//	java   method_reference     `A::stat`   — not an invocation
//	kotlin callable_reference   `::stat`    — not an invocation
//	java   array_creation_expression  `new int[3]`  — no callee
//	kotlin object_literal   `object : X {…}` — java's `new X(){…}` IS counted
//	kotlin constructor_invocation under an
//	       annotation       `@Anno(v = 1)`  — not a call at all
//
// The annotation case is the one an adversarial reader should check first: the
// Kotlin grammar gives an annotation's argument list the SAME node type as a
// superclass delegation, and only the parent discriminates. Java is not exposed
// to this — `@Anno(v = 1)` parses as annotation → annotation_argument_list →
// element_value_pair, with no method_invocation anywhere.
//
// TestGrammarShapes_AreWhatWeCount pins all of it against the real grammar, and
// asserts every fixture parses with ZERO ERROR nodes — because an earlier draft
// of that test was itself measuring a partially-recovered tree.
//
// # The numerator is Phase B, and the Phase C loss is NOT measured here
//
// The numerator counts TypedSite values with Kind == SiteCall returned by the
// SHIPPED walkers (AnalyzeJavaBodies / AnalyzeKotlinBodies) — the binder's own
// output, not a reimplementation. It is a PHASE B figure: the site was typed
// and its member bound. Phase C (emit.go) then maps endpoints onto graph nodes
// and drops what has none, under emit_from_no_node / emit_to_no_node. That step
// needs a committed node set, i.e. a full ingest, so this harness does not run
// it and MUST NOT be read as reporting confirmed edges. The gap is named in the
// published record rather than folded silently into the rate.
package jvmbindrate

import (
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/engine/jvmresolve"
)

// operatorToken joins a node's ANONYMOUS children — its operator tokens — into
// one string, so `a !in b` yields "!in" and is distinguishable from `a !is B`,
// which yields "!is". The two share the node type `check_expression`, and only
// the first is a call.
//
// It exists because the operator-convention family CANNOT be classified by node
// type: `postfix_expression` is `inc`/`dec` for `++`/`--` and nothing at all for
// `!!`; `equality_expression` is `equals` for `==` and referential identity for
// `===`. Counting node types alone overcounts, measured, by more than half of
// okio's postfix family.
//
// A named first child is returned in angle brackets: the Kotlin grammar admits
// an annotation or a label as a prefix "operator" (`@Ann a`), which is not an
// operator call. Anything else this walk does not recognise reaches
// UnclassifiedOperator rather than being guessed into a bucket.
func operatorToken(g *gts.Language, n *gts.Node) string {
	var b strings.Builder
	for i := 0; i < n.ChildCount(); i++ {
		ch := n.Child(i)
		if ch == nil {
			continue
		}
		if !ch.IsNamed() {
			// An anonymous node's type IS its literal token text, verified
			// against the real grammar ("!", "in", "!!", "===").
			b.WriteString(ch.Type(g))
			continue
		}
		if i == 0 && b.Len() == 0 {
			switch t := ch.Type(g); t {
			case "annotation", "label":
				return "<" + t + ">"
			}
		}
	}
	return b.String()
}

// CSTCounts is the parse-tree side of the fraction: every count here comes from
// walking the CST and none of it from the binder.
type CSTCounts struct {
	// Files actually parsed for this language.
	Files int

	// Parse degradation, published rather than assumed absent. tree-sitter
	// recovers from a syntax error instead of failing, so a file can
	// contribute a SILENTLY PARTIAL count. Measured example: a one-line Kotlin
	// class body whose members are not newline-separated yields 3 ERROR nodes
	// and loses two of its three calls.
	FilesWithParseErrors int
	ParseErrorNodes      int

	// The denominator's components.
	MethodInvocation int // java `helper()`, `a.f()`
	ObjectCreation   int // java `new A(1)`
	CallSuffix       int // kotlin — the exact invocation marker; see countInvocations

	// Named exclusions, counted so every exclusion has a published size.
	ExplicitCtorInvocation int // java  `super(…)` / `this(…)`
	CtorDelegationCall     int // kotlin `constructor(x) : this(x, 0)` — java's true twin
	DelegationCtorCall     int // kotlin `class A : B(1)` — java's twin is `extends B`, no node
	AnnotationCtorCall     int // kotlin `@Anno(v = 1)` — not a call at all
	EnumConstantArgs       int // java  `enum E { A(1) }`
	EnumEntryArgs          int // kotlin `enum class E { A(1) }`
	InfixExpression        int // kotlin `a shl b` — a call only if `infix fun`
	IndexingSuffix         int // kotlin `a[i]` READ AND WRITE — see countInvocations
	ObjectLiteral          int // kotlin `object : X { … }` — java's `new X(){…}` IS counted
	MethodReference        int // java  `A::stat`
	CallableReference      int // kotlin `::stat` — LOWER BOUND, see countInvocations
	ArrayCreation          int // java  `new int[3]` — no callee

	// ---- the Kotlin OPERATOR-CONVENTION family (SW-175 review, MAJOR-3) ----
	//
	// Every one of these denotes a call to a named operator function whenever
	// the operand type declares one, exactly as `infix_expression` does. They
	// were counted in NO bucket at all in the first draft — neither in the
	// denominator nor among the published exclusions — while a quarter of the
	// same family (infix + indexing) WAS published, which implied the
	// published number was the whole of it. They are excluded from the primary
	// denominator on the same Java-symmetry rule and counted here so the size
	// of the Kotlin-only operator residual is visible.
	//
	// Each is discriminated BY ITS OPERATOR TOKEN, not by its node type,
	// because several of these node types carry both a call and a non-call
	// form. Counting the node type alone OVERCOUNTS — measured: `a!!` is a
	// postfix_expression and is not a call, `a === b` is an equality_expression
	// and is not `equals`.
	AdditiveExpression       int // kotlin `a + b`  → plus / minus
	MultiplicativeExpression int // kotlin `a * b`  → times / div / rem
	PrefixExpression         int // kotlin `-a` `!a` `++a` → unaryMinus / not / inc
	PostfixExpression        int // kotlin `a++`   → inc / dec        (NOT `a!!`)
	EqualityExpression       int // kotlin `a == b` → equals          (NOT `a === b`)
	ComparisonExpression     int // kotlin `a < b` → compareTo
	RangeExpression          int // kotlin `a..b`  → rangeTo
	ContainsExpression       int // kotlin `a in b`, `when { in 1..5 ->` → contains
	AugmentedAssignment      int // kotlin `a += b` → plusAssign / plus (NOT plain `a = b`)

	// ---- measured, and deliberately NOT counted as calls ----
	//
	// These are the forms the operator-token discrimination above REJECTS.
	// They are published for the same reason the exclusions are: a rejection
	// that is not counted is a place to hide a denominator choice.
	NotNullAssertion    int // kotlin `a!!`     — a null check, no callee
	ReferentialEquality int // kotlin `a === b` — identity, never `equals`
	PlainAssignment     int // kotlin `a = b`   — not a call
	TypeTest            int // kotlin `a is B`  — not a call
	// UnclassifiedOperator is the grammar-drift guard: an operator token this
	// walk does not recognise lands here rather than being silently dropped
	// into either bucket. It should be 0; if it is not, the classification is
	// stale and the published exclusion sizes are wrong.
	UnclassifiedOperator int

	// ---- SYNTHESIZED-PROTOCOL calls, both languages ----
	//
	// Calls the compiler synthesizes for a construct that NAMES NO CALLABLE in
	// the source. They are measured for BOTH languages and added to NO
	// denominator; see WidestDenominator for the line and why it falls here.
	ForInLoops              int // kotlin `for (x in xs)` / java `for (X x : xs)` → iterator/hasNext/next
	DestructuringComponents int // kotlin `val (a, b) = p` → component1(), component2()
	DelegatedProperties     int // kotlin `val p by lazy {}` → getValue / setValue
	TryWithResources        int // java `try (X x = …)` → close()
}

// Denominator is the published denominator: invocation expressions.
func (c CSTCounts) Denominator() int {
	return c.MethodInvocation + c.ObjectCreation + c.CallSuffix
}

// add folds one file's counts into a running total.
//
// Every field of CSTCounts is an int and every one accumulates the same way, so
// this is done by reflection RATHER than by 30 hand-written `+=` lines. That is
// not brevity for its own sake: the hand-written form has a silent failure mode
// — add a counter to the struct, publish it, forget one `+=`, and the counter
// reads 0 on every real corpus while every test that measures a single file
// still passes. TestCSTCounts_AddIsExhaustive pins the property directly.
func (c *CSTCounts) add(o CSTCounts) {
	dst := reflect.ValueOf(c).Elem()
	src := reflect.ValueOf(o)
	for i := 0; i < dst.NumField(); i++ {
		dst.Field(i).SetInt(dst.Field(i).Int() + src.Field(i).Int())
	}
}

// OperatorConventionCalls is the Kotlin operator-convention family: every
// construct that invokes a NAMED operator function when the operand type
// declares one. `infix_expression` and the indexing suffix are part of the same
// family and are added by WidestDenominator alongside it.
func (c CSTCounts) OperatorConventionCalls() int {
	return c.AdditiveExpression + c.MultiplicativeExpression +
		c.PrefixExpression + c.PostfixExpression +
		c.EqualityExpression + c.ComparisonExpression +
		c.RangeExpression + c.ContainsExpression + c.AugmentedAssignment
}

// SynthesizedProtocolCalls is a LOWER BOUND on the calls a compiler synthesizes
// for constructs that name no callable in the source. Each `for` loop implies at
// least iterator() + hasNext() + next(); each delegated property at least
// getValue(). It is measured for both languages and belongs to NO denominator —
// see WidestDenominator.
//
// It is a lower bound in TWO ways, and the second is the one worth stating: not
// only is the per-construct multiplier a minimum, whole CONSTRUCT FAMILIES are
// not counted here at all. Known omissions, systematic in both languages:
//
//	java    string concatenation      `a + b` on String -> StringConcatFactory
//	java    autoboxing                `Integer.valueOf` / `intValue`
//	java    implicit `super()`        emitted when a constructor writes none
//	kotlin  string templates          `"$x"` -> toString()
//	kotlin  collection_literal        `[1, 2]` in annotations -> arrayOf
//
// So this number must be read as "at least this many", never as a census of
// synthesized calls. It is published to BOUND the exclusion, not to enumerate
// it. Nothing here is in any denominator, so no rate depends on the gap — but a
// reader comparing this total against a compiler's own would find it low, and
// should.
func (c CSTCounts) SynthesizedProtocolCalls() int {
	return 3*c.ForInLoops + c.DestructuringComponents +
		c.DelegatedProperties + c.TryWithResources
}

// WidestDenominator adds back every excluded construct that NAMES a callable:
// constructor delegation in both languages, enum constant/entry argument lists,
// and the whole Kotlin operator-convention family (OperatorConventionCalls plus
// `infix_expression` and the indexing suffix). It is published beside the
// primary rate so the size of the denominator choice is visible rather than
// trusted.
//
// # Where this variant stops, stated rather than claimed
//
// An earlier draft called this "deliberately the most damning variant: nothing
// here can make the published rate look better." That claim was FALSE and is
// retracted: `indexing_expression` undercounted the indexing suffix by a third
// (the write and interpolated forms parse to a bare suffix), and the entire
// operator-convention family above was in no bucket at all — together ~4 100
// sites on okio and ~2 200 on kotlinx, which SHRANK this denominator and RAISED
// this rate. A superlative that has not been tested against an enumeration of
// the grammar is exactly the kind of claim that defect hides behind, so it is
// replaced by a stated line:
//
//	INCLUDED — every construct whose source text names a callable, whether or
//	not the name is spelled out: `f()` spells it, `a + b` names `plus` through
//	the operator convention, `A(1)` in `class A : B(1)` names B's constructor.
//
//	EXCLUDED — SynthesizedProtocolCalls: constructs whose source names no
//	callable at all and for which the compiler invents the call. `for (x in xs)`
//	writes no identifier that a binder could resolve, yet implies three calls.
//	This line is drawn IDENTICALLY IN BOTH LANGUAGES — java's `for (X x : xs)`
//	is the same construct and is excluded the same way, and its size (guava
//	WHOLE PIN 6 851 loops + 43 resources; guava/src 884 + 8) is published beside
//	Kotlin's (okio 100/28/2, kotlinx 114/36/11), so the exclusion cannot flatter
//	one language against the other. Their sizes are measured and published; they
//	are not a residual anyone has to take on trust.
//
//	The figure here read "3 740 loops" for one round and matched NO scope of the
//	pin — it was simply stale, while docs/rc/jvm-binding-rate.md carried the
//	right one. It is the scope, not the number, that made it rot: a bare count
//	with no scope beside it cannot be checked against a re-run. Hence both.
//
// Annotations and object literals also stay out. An annotation is not a call,
// and an object literal is a type declaration whose constructor call, where it
// has one, is already counted as DelegationCtorCall.
func (c CSTCounts) WidestDenominator() int {
	return c.Denominator() +
		c.ExplicitCtorInvocation + c.CtorDelegationCall + c.DelegationCtorCall +
		c.EnumConstantArgs + c.EnumEntryArgs +
		c.InfixExpression + c.IndexingSuffix +
		c.OperatorConventionCalls()
}

// LanguageReport is one (corpus, language) measurement.
type LanguageReport struct {
	Language string

	// Source files found for this language, and how many BuildTable actually
	// tabled. TabledFiles < SourceFiles means the binder never saw the rest,
	// which is a coverage statement and is published as one.
	SourceFiles  int
	TabledFiles  int
	SkippedFiles []string

	// TabledTypes is how many types of this language the table holds, and
	// TypesInCollidedFQNs how many of them share their FQN with at least one
	// other tabled type. The second number is NOT decoration: tabledType
	// (engine/jvmresolve/body_java.go:686) returns nil unless a FQN has
	// EXACTLY ONE candidate, so every type in a collided FQN has its ENTIRE
	// body walk abandoned under a single java_body_unmatched increment. One
	// counter therefore stands for an unbounded number of unbound call sites,
	// which is the single most important thing to know before reading the
	// histogram as if it were site-keyed.
	TabledTypes         int
	CollidedFQNs        int
	TypesInCollidedFQNs int

	CST CSTCounts

	// BoundCallSites is the NUMERATOR: Phase B TypedSites with Kind ==
	// SiteCall. BoundValueSites is reported only because the named-skip
	// histogram is shared between the two kinds — see Residual.
	BoundCallSites  int
	BoundValueSites int

	// Skips is the Phase B half of the SW-171 named-skip vocabulary, exactly
	// as the shipped walkers count it. The two emit_* counters are absent by
	// construction (Phase C is not run here) and that is stated, not implied.
	Skips map[string]int

	// Clean and Dirty partition THIS SAME measurement by parse quality:
	// Clean holds the files whose CST carries zero ERROR nodes, Dirty the rest.
	// See ParseArm for why the split is sound and what it answers.
	Clean ParseArm
	Dirty ParseArm
}

// ParseArm is one half of the parse-quality partition — FINDING B-0's direction,
// which an earlier draft declared "undetermined".
//
// # Why the split is sound, and what would have made it a confound
//
// The table is built ONCE over ALL files (Measure does this before either arm
// exists), so both arms bind against an IDENTICAL index: a clean file that
// references a type declared in a dirty file still resolves it. Rebuilding a
// table per arm would have measured "how well does a corpus half bind itself",
// which is a different and much easier question, and would have made the clean
// arm look worse by starving it of types.
//
// The numerator is partitioned by TypedSite.FromFile and the denominator by the
// same file's own ERROR-node count, so the two arms PARTITION the corpus and
// reconstitute the published totals exactly. Measure asserts nothing here; the
// reconstitution is asserted by TestParseArms_PartitionTheCorpus, because two
// arms that quietly failed to sum would be a silent re-measurement rather than
// a split.
//
// # What it establishes: the bias is anti-flattering
//
// A recovered tree destroys the NUMERATOR far faster than the denominator — the
// binder cannot walk a body it cannot parse, while the CST still contributes
// every invocation node the recovery left standing. So dirty parses DEPRESS the
// published Kotlin rates rather than inflating them. "Undetermined" erred
// against the product, which is the right direction to err, but it was an
// incomplete measurement presented as an unavailable one.
type ParseArm struct {
	// Files in this arm, and the ERROR nodes they carry (0 for Clean by
	// construction — it is the definition of the arm, and it is rendered so a
	// reader can see the partition is on what it claims to be on).
	Files           int
	ParseErrorNodes int

	// BoundCallSites over CallSites, each counted exactly as the whole-corpus
	// figures are, restricted to this arm's files.
	BoundCallSites int
	CallSites      int
}

// Rate is this arm's bound call sites ÷ its CST call sites, in percent.
// A zero denominator yields 0; callers that publish MUST test CallSites first,
// because 0 of 0 is not 0 % (see LanguageReport.RateText).
func (a ParseArm) Rate() float64 {
	if a.CallSites == 0 {
		return 0
	}
	return 100 * float64(a.BoundCallSites) / float64(a.CallSites)
}

// SkipTotal is the sum of the named-skip histogram.
func (r LanguageReport) SkipTotal() int {
	t := 0
	for _, n := range r.Skips {
		t += n
	}
	return t
}

// Residual is what AC-4 exists to expose: denominator − bound − histogram.
//
// It is NOT expected to be zero, and a reader who expects zero has misread the
// histogram. The named counters are not call-site-keyed: the same counter names
// are incremented by VALUE sites (`a.b` field/property reads), some are per-BODY
// rather than per-site (java_body_unmatched, java_body_panic), and one —
// java_var_inferred / kotlin_val_inferred — is incremented at a LOCAL VARIABLE
// DECLARATION, which is not a call site at all. So the difference is not a count
// of "lost call sites"; it is the arithmetic consequence of differencing two
// quantities with different keys, and it can be either sign.
//
// TestHistogram_IsNotCallSiteKeyed measures each of those mechanisms on a
// hermetic fixture with a hand count, so the explanation is demonstrated rather
// than asserted.
func (r LanguageReport) Residual() int {
	return r.CST.Denominator() - r.BoundCallSites - r.SkipTotal()
}

// Rate is bound call sites ÷ CST call sites, in percent. Zero denominator
// yields 0 rather than NaN so a report is always printable.
//
// A caller that PUBLISHES the rate must not use this number without testing the
// denominator: 0 of 0 is not 0 %. Use RateText, which cannot make that mistake.
func (r LanguageReport) Rate() float64 {
	d := r.CST.Denominator()
	if d == 0 {
		return 0
	}
	return 100 * float64(r.BoundCallSites) / float64(d)
}

// WidestRate is the same numerator over WidestDenominator.
func (r LanguageReport) WidestRate() float64 {
	d := r.CST.WidestDenominator()
	if d == 0 {
		return 0
	}
	return 100 * float64(r.BoundCallSites) / float64(d)
}

// rateText is the ONE place that knows a rate over an empty denominator is not
// a rate. `0 bound / 0 sites` is "this language has no call sites here", not
// "this binder bound nothing", and printing 0.00 % for it states a failure that
// did not happen.
//
// The published record has always written `n/a` for this case; the harness log
// printed `RATE 0.00%`, and the CI step's own comment calls that log the place
// "every published figure lands". The document and the log now agree because
// they go through this function.
func rateText(num, den int) string {
	if den == 0 {
		return fmt.Sprintf("n/a (%d/%d — no call sites at this scope)", num, den)
	}
	return fmt.Sprintf("%.2f%% = %d / %d", 100*float64(num)/float64(den), num, den)
}

// RateText is Rate rendered for publication — `n/a` on an empty denominator.
func (r LanguageReport) RateText() string {
	return rateText(r.BoundCallSites, r.CST.Denominator())
}

// WidestRateText is WidestRate rendered for publication.
func (r LanguageReport) WidestRateText() string {
	return rateText(r.BoundCallSites, r.CST.WidestDenominator())
}

// RateText is this arm's rate rendered for publication.
func (a ParseArm) RateText() string {
	return rateText(a.BoundCallSites, a.CallSites)
}

// SkipHistogram returns the named counters sorted by descending count, then by
// name — deterministic, because AC-5 asks two runs to be identical and a map
// iteration is not.
func (r LanguageReport) SkipHistogram() []SkipRow {
	rows := make([]SkipRow, 0, len(r.Skips))
	for k, v := range r.Skips {
		rows = append(rows, SkipRow{Reason: k, Count: v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Reason < rows[j].Reason
	})
	return rows
}

// SkipRow is one histogram entry.
type SkipRow struct {
	Reason string
	Count  int
}

// Measure runs both languages over one source snapshot (path → bytes).
//
// The table is built ONCE over all files, exactly as the shipped resolver does
// (jvmresolve.Resolver.Input spans both languages so cross-language types
// resolve), and each language's walker is then run over the same index. Running
// two separate tables would give the Kotlin walker a repository with no Java in
// it and would change what it can bind.
func Measure(files map[string][]byte) map[string]LanguageReport {
	tab := jvmresolve.BuildTable(files)
	ix := jvmresolve.NewIndex(tab)

	out := map[string]LanguageReport{}
	for _, lang := range []string{jvmresolve.LangJava, jvmresolve.LangKotlin} {
		r := LanguageReport{Language: lang}

		var sites []jvmresolve.TypedSite
		var skips jvmresolve.SkipCounts
		if lang == jvmresolve.LangJava {
			sites, skips = ix.AnalyzeJavaBodies(files)
		} else {
			sites, skips = ix.AnalyzeKotlinBodies(files)
		}
		for i := range sites {
			if sites[i].Kind == jvmresolve.SiteCall {
				r.BoundCallSites++
			} else {
				r.BoundValueSites++
			}
		}
		r.Skips = map[string]int{}
		for k, v := range skips {
			r.Skips[k] = v
		}

		// Coverage: how many of this language's files exist, and how many the
		// binder's own table accepted.
		for p := range files {
			if languageOf(p) == lang {
				r.SourceFiles++
			}
		}
		for fi := range tab.Files {
			if tab.Files[fi].Language == lang {
				r.TabledFiles++
			}
		}
		for _, s := range tab.Skipped {
			if languageOf(s.Path) == lang {
				r.SkippedFiles = append(r.SkippedFiles, s.Path+": "+s.Reason)
			}
		}
		sort.Strings(r.SkippedFiles)

		// FQN collision exposure, measured from the same table the binder
		// reads. byFQN is not exported, so this recomputes it exactly as
		// NewIndex does (Table.TypesByFQN) — the same map, from the same
		// table, by the same rule.
		for _, cands := range tab.TypesByFQN() {
			own := 0
			for _, c := range cands {
				if c.Language == lang {
					own++
				}
			}
			r.TabledTypes += own
			// A collision is counted only where THIS language has a type in
			// it: the collision is what abandons this language's body walk,
			// and a collision between two files of the other language is the
			// other language's report to make.
			if own > 0 && len(cands) > 1 {
				r.CollidedFQNs++
				r.TypesInCollidedFQNs += own
			}
		}

		// The denominator: an independent CST walk over EVERY source file of
		// this language — including the ones BuildTable skipped, because a file
		// the binder could not table still contains call sites it did not bind,
		// and dropping them from the denominator is exactly the tautology this
		// harness exists to avoid.
		paths := make([]string, 0, len(files))
		for p := range files {
			if languageOf(p) == lang {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		// dirty records, per file, whether its parse carried ERROR nodes, so
		// the numerator can be partitioned the same way the denominator is.
		dirty := map[string]bool{}
		for _, p := range paths {
			c := countInvocations(lang, files[p])
			r.CST.Files++
			r.CST.add(c)

			// The parse-quality partition (FINDING B-0). The denominator half
			// is exact here; the numerator half is attributed below, once,
			// from TypedSite.FromFile.
			arm := &r.Clean
			if c.FilesWithParseErrors > 0 {
				dirty[p] = true
				arm = &r.Dirty
			}
			arm.Files++
			arm.ParseErrorNodes += c.ParseErrorNodes
			arm.CallSites += c.Denominator()
		}
		for i := range sites {
			if sites[i].Kind != jvmresolve.SiteCall {
				continue
			}
			if dirty[sites[i].FromFile] {
				r.Dirty.BoundCallSites++
			} else {
				r.Clean.BoundCallSites++
			}
		}
		out[lang] = r
	}
	return out
}

// languageOf keys on the file extension only — the same rule the shipped
// resolver's Input/Subject predicates use.
func languageOf(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".java":
		return jvmresolve.LangJava
	case ".kt":
		return jvmresolve.LangKotlin
	}
	return ""
}

// countInvocations walks one file's CST and tallies node types. It is
// deliberately trivial: no tables, no types, no scopes, no knowledge of the
// binder.
//
// # Parse failure is COUNTED, never assumed away
//
// tree-sitter is an error-recovering parser: it returns a tree with `ERROR`
// nodes and a nil error rather than failing, and the recovered tree can be
// missing arbitrary subtrees. An earlier draft of this file claimed "a parse
// error yields zero counts for that file", and that claim was FALSE — measured:
// a truncated Java class still yields two `method_invocation` nodes beside two
// `ERROR` nodes. Silent partial counts are the worst possible failure for a
// denominator, so ERROR nodes are tallied and published on every run
// (`ParseErrorNodes`, `FilesWithParseErrors`). A reader can see degradation
// instead of inferring its absence.
func countInvocations(lang string, src []byte) CSTCounts {
	var c CSTCounts
	var g *gts.Language
	if lang == jvmresolve.LangJava {
		g = grammars.JavaLanguage()
	} else {
		g = grammars.KotlinLanguage()
	}
	tree, err := gts.NewParser(g).Parse(src)
	if err != nil || tree == nil {
		c.FilesWithParseErrors = 1
		return c
	}
	var walk func(n *gts.Node, parentType string)
	walk = func(n *gts.Node, parentType string) {
		if n == nil {
			return
		}
		t := n.Type(g)
		switch t {
		case "ERROR":
			c.ParseErrorNodes++

		// ---- java, in the denominator ----
		case "method_invocation":
			c.MethodInvocation++
		case "object_creation_expression":
			c.ObjectCreation++

		// ---- kotlin, in the denominator ----
		case "call_suffix":
			// `call_suffix` — NOT `call_expression` — is the Kotlin grammar's
			// exact marker of an invocation expression, and the difference is
			// not pedantry. Inside a string template the grammar emits
			// `interpolated_expression → simple_identifier + call_suffix` with
			// NO enclosing call_expression, so `"${f()}"` is a real call that
			// counting call_expression misses entirely. Measured: three calls
			// in one interpolated string produce 3 `call_suffix` and 0
			// `call_expression`.
			//
			// It is exact in the other direction too, verified against the real
			// grammar: `constructor_invocation` (annotations and superclass
			// delegation), `constructor_delegation_call` and `enum_entry` all
			// carry `value_arguments` and NOT `call_suffix`, so counting
			// call_suffix cannot pull an annotation into the denominator. On
			// ordinary code the two counts coincide exactly (3 and 3 on the
			// control fixture).
			c.CallSuffix++

		// ---- excluded, and counted so the exclusion has a size ----
		case "explicit_constructor_invocation":
			// java `super(…)` / `this(…)`
			c.ExplicitCtorInvocation++
		case "constructor_delegation_call":
			// kotlin `constructor(x: Int) : this(x, 0)` — the TRUE twin of
			// java's explicit_constructor_invocation. An earlier draft claimed
			// that twin was `constructor_invocation` under a
			// delegation_specifier; that was WRONG, and the two are not the
			// same construct: `class A : B(1)` is the PRIMARY constructor's
			// superclass call, whose java counterpart is `extends B`, which
			// produces no invocation node at all.
			c.CtorDelegationCall++
		case "enum_constant":
			// java `enum E { A(1) }` — `A(1)` invokes `E(int)` and produces no
			// method_invocation. Symmetric with kotlin's enum_entry.
			c.EnumConstantArgs++
		case "enum_entry":
			c.EnumEntryArgs++
		case "infix_expression":
			// kotlin `a shl b` — a call iff `shl` is a user-declared `infix
			// fun`, which the CST cannot tell. Excluded for SYMMETRY: java's
			// `a + b` is not a call node either. Counted so the size of the
			// Kotlin-only residual is published rather than absorbed.
			c.InfixExpression++
		case "indexing_suffix":
			// kotlin `a[i]` — a call iff `get`/`set` is an `operator fun`.
			//
			// `indexing_suffix` — NOT `indexing_expression` — is the marker,
			// and this is the SAME correction `call_suffix` made over
			// `call_expression`, in the sibling counter. Measured against the
			// real grammar:
			//
			//	a[i]      read   indexing_expression → indexing_suffix
			//	a[i] = v  `set`  assignment → directly_assignable_expression →
			//	                 indexing_suffix — NO indexing_expression node
			//	a[i] += v same
			//	"${a[i]}" read   interpolated_expression → indexing_suffix
			//
			// so counting the expression node missed every WRITE and every
			// interpolated read: okio 269 counted / 133 missed (+49 %),
			// kotlinx 274 / 113 (+41 %). The direction was FLATTERING —
			// WidestDenominator includes it, so undercounting it raised the
			// widest rate.
			//
			// The adjacent error is NOT present and was checked rather than
			// assumed: a multi-dimensional read `a[i][j]` emits one suffix per
			// dimension, and on both pins every indexing_expression carries
			// exactly one suffix, so nothing is undercounted there.
			c.IndexingSuffix++

		// ---- the operator-convention family (MAJOR-3) ----
		//
		// Discriminated by OPERATOR TOKEN, never by node type alone: several of
		// these node types carry a call form and a non-call form, and counting
		// the node would overcount. Anything unrecognised lands in
		// UnclassifiedOperator rather than in either bucket.
		case "additive_expression":
			c.AdditiveExpression++ // `+` / `-` → plus / minus
		case "multiplicative_expression":
			c.MultiplicativeExpression++ // `*` `/` `%` → times / div / rem
		case "comparison_expression":
			c.ComparisonExpression++ // `<` `>` `<=` `>=` → compareTo
		case "range_expression":
			c.RangeExpression++ // `a..b` → rangeTo
		case "range_test":
			// `when (x) { in 1..5 -> }` — the when-condition form of `in`, and
			// a `contains` call exactly as the expression form is. Its inner
			// `range_expression` is a separate rangeTo call and is counted
			// separately above, which is correct: `in 1..5` is two calls.
			c.ContainsExpression++
		case "type_test":
			c.TypeTest++ // `when (x) { is Foo -> }` — not a call
		case "prefix_expression":
			// `-a` `+a` `!a` `++a` `--a` → unaryMinus / unaryPlus / not / inc /
			// dec. The grammar also admits an ANNOTATION or a LABEL as a prefix
			// operator (`@Ann a`), which is not a call — measured, 2 on
			// kotlinx — so the token is checked.
			switch operatorToken(g, n) {
			case "-", "+", "!", "++", "--":
				c.PrefixExpression++
			case "<annotation>", "<label>":
				// not a call, and not an operator either
			default:
				c.UnclassifiedOperator++
			}
		case "postfix_expression":
			// `a++` / `a--` → inc / dec. `a!!` is ALSO a postfix_expression and
			// is NOT a call — it is a null assertion with no callee. It is the
			// larger of the two on real code (okio 157 `!!` against 103
			// inc/dec), so counting the node type would have overcounted this
			// family by more than half on that pin.
			switch operatorToken(g, n) {
			case "++", "--":
				c.PostfixExpression++
			case "!!":
				c.NotNullAssertion++
			default:
				c.UnclassifiedOperator++
			}
		case "equality_expression":
			// `==` / `!=` → equals. `===` / `!==` are REFERENTIAL identity and
			// call nothing; they share this node type and are separated by the
			// token (okio 29, kotlinx 59).
			switch operatorToken(g, n) {
			case "==", "!=":
				c.EqualityExpression++
			case "===", "!==":
				c.ReferentialEquality++
			default:
				c.UnclassifiedOperator++
			}
		case "check_expression":
			// `a in b` / `a !in b` → contains. `a is B` / `a !is B` is a type
			// test and calls nothing. One node type, both meanings.
			switch operatorToken(g, n) {
			case "in", "!in":
				c.ContainsExpression++
			case "is", "!is":
				c.TypeTest++
			default:
				c.UnclassifiedOperator++
			}
		case "assignment":
			// `a += b` → plusAssign (or plus + set); plain `a = b` is NOT a
			// call and is counted separately so the split is visible.
			//
			// An INDEXED target (`a[i] = v`, `a[i] += v`) invokes `set`, and
			// that call is already counted as the indexing_suffix under the
			// directly_assignable_expression — counting it again here would
			// double-count the exact sites MAJOR-2 was about.
			switch operatorToken(g, n) {
			case "=":
				c.PlainAssignment++
			case "+=", "-=", "*=", "/=", "%=":
				c.AugmentedAssignment++
			default:
				c.UnclassifiedOperator++
			}

		// ---- synthesized protocol: measured, in no denominator ----
		case "for_statement", "enhanced_for_statement":
			// kotlin `for (x in xs)` and java `for (X x : xs)` are the SAME
			// construct and are counted the same way, which is what keeps the
			// exclusion from flattering one language against the other.
			c.ForInLoops++
		case "multi_variable_declaration":
			// `val (a, b) = p` → component1(), component2(): one call per
			// declared variable, so the variables are counted, not the node.
			for i := 0; i < n.ChildCount(); i++ {
				if ch := n.Child(i); ch != nil && ch.Type(g) == "variable_declaration" {
					c.DestructuringComponents++
				}
			}
		case "property_delegate":
			c.DelegatedProperties++ // `by lazy {}` → getValue / setValue
		case "resource":
			// java try-with-resources → one close() PER RESOURCE, so the
			// resource is counted rather than the statement (a statement may
			// declare several). On guava the two coincide at 43.
			c.TryWithResources++
		case "object_literal":
			// kotlin `object : X { … }`. Its java twin `new X(){…}` IS an
			// object_creation_expression and IS in the denominator, so this is
			// a genuine cross-language asymmetry; counting it publishes its
			// size. (An object literal with constructor arguments also emits a
			// constructor_invocation under its delegation_specifier, counted
			// below.)
			c.ObjectLiteral++
		case "method_reference":
			c.MethodReference++
		case "callable_reference":
			// LOWER BOUND, and stated as one: `::f` and `::A` parse as
			// callable_reference, but `A::g` and `a::h` parse as
			// navigation_expression and are invisible here. Java's
			// method_reference catches all four forms. This never moves the
			// denominator; it means the two languages' exclusion columns are
			// not comparable, which is published rather than implied.
			c.CallableReference++
		case "array_creation_expression":
			c.ArrayCreation++

		case "constructor_invocation":
			// The Kotlin grammar gives an ANNOTATION's argument list and a
			// SUPERCLASS DELEGATION the same node type. Counting them together
			// would put `@Anno(v = 1)` in a call-site count; the parent
			// discriminates. Every parent observed against the real grammar is
			// one of: delegation_specifier, annotation, file_annotation.
			if parentType == "delegation_specifier" {
				c.DelegationCtorCall++
			} else {
				// Annotation, and any parent this probe has not seen. The
				// failure direction is deliberate: mis-attributing towards
				// "not a call" can only SHRINK the widest denominator, never
				// the primary one, so a surprise here cannot flatter the rate.
				c.AnnotationCtorCall++
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i), t)
		}
	}
	walk(tree.RootNode(), "")
	if c.ParseErrorNodes > 0 {
		c.FilesWithParseErrors = 1
	}
	return c
}
