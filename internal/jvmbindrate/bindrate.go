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
//	kotlin indexing_expression `a[i]`      — a call iff `operator fun get`
//
// Excluded for symmetry: Java's `a + b` and `a[i]` are not call nodes either,
// and the CST cannot tell a user-defined operator from a builtin. Counted, so
// the size of this Kotlin-only residual is published.
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
	"path"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/engine/jvmresolve"
)

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
	IndexingExpression     int // kotlin `a[i]` — a call only if `operator fun get`
	ObjectLiteral          int // kotlin `object : X { … }` — java's `new X(){…}` IS counted
	MethodReference        int // java  `A::stat`
	CallableReference      int // kotlin `::stat` — LOWER BOUND, see countInvocations
	ArrayCreation          int // java  `new int[3]` — no callee
}

// Denominator is the published denominator: invocation expressions.
func (c CSTCounts) Denominator() int {
	return c.MethodInvocation + c.ObjectCreation + c.CallSuffix
}

// WidestDenominator adds back every excluded construct that DOES invoke a
// callable — constructor delegation in both languages, enum constant/entry
// argument lists, and Kotlin's operator-shaped calls. It is published beside
// the primary rate so the size of the denominator choice is visible rather
// than trusted, and it is deliberately the most damning variant: nothing here
// can make the published rate look better.
//
// Annotations and object literals stay out. An annotation is not a call, and an
// object literal is a type declaration whose constructor call, where it has
// one, is already counted as DelegationCtorCall.
func (c CSTCounts) WidestDenominator() int {
	return c.Denominator() +
		c.ExplicitCtorInvocation + c.CtorDelegationCall + c.DelegationCtorCall +
		c.EnumConstantArgs + c.EnumEntryArgs +
		c.InfixExpression + c.IndexingExpression
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
		for _, p := range paths {
			c := countInvocations(lang, files[p])
			r.CST.Files++
			r.CST.FilesWithParseErrors += c.FilesWithParseErrors
			r.CST.ParseErrorNodes += c.ParseErrorNodes
			r.CST.MethodInvocation += c.MethodInvocation
			r.CST.ObjectCreation += c.ObjectCreation
			r.CST.CallSuffix += c.CallSuffix
			r.CST.ExplicitCtorInvocation += c.ExplicitCtorInvocation
			r.CST.CtorDelegationCall += c.CtorDelegationCall
			r.CST.DelegationCtorCall += c.DelegationCtorCall
			r.CST.AnnotationCtorCall += c.AnnotationCtorCall
			r.CST.EnumConstantArgs += c.EnumConstantArgs
			r.CST.EnumEntryArgs += c.EnumEntryArgs
			r.CST.InfixExpression += c.InfixExpression
			r.CST.IndexingExpression += c.IndexingExpression
			r.CST.ObjectLiteral += c.ObjectLiteral
			r.CST.MethodReference += c.MethodReference
			r.CST.CallableReference += c.CallableReference
			r.CST.ArrayCreation += c.ArrayCreation
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
		case "indexing_expression":
			// kotlin `a[i]` — a call iff `get` is an `operator fun`. Same
			// reasoning; java's `a[i]` is array_access, never a call.
			c.IndexingExpression++
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
