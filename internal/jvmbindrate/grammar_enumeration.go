package jvmbindrate

// This file is THE ENUMERATION: every named symbol of the two embedded grammars
// that actually occurs on the three corpus pins, classified into exactly one of
// four buckets. It exists because "fix the node types someone named in a review"
// is not a method.
//
// The denominator has now been wrong EIGHT times across two adversarial rounds,
// and the last two — `indexing_expression` missing the indexed WRITE forms, and
// the operator-convention family counted in no bucket at all — were the SAME
// defect class as the first (`call_expression` missing string-template calls),
// recurring in a sibling counter and in a whole family. A defect class that
// recurs after being fixed once is not a defect, it is a missing method.
//
// So the node types are no longer chosen by inspection. Every symbol below was
// obtained from gts.Language.SymbolNames filtered by SymbolMetadata.Named, then
// intersected with what the pins actually produce, then classified ONE AT A
// TIME. The "not a call" bucket is written out in full rather than being the
// default, because a default classifies every future grammar addition as
// harmless by silence — which is exactly how the eight got in.
//
//	bucket                                      java   kotlin
//	D  primary denominator                         2        1
//	W  a call; in WidestDenominator                2       15
//	P  synthesized protocol; in NO denominator     2        3
//	N  not a call                                119      122
//	                                             ----     ----
//	   occurring on the pins                      125      141
//	   named symbols in the grammar               166      198
//
// The 41 java and 57 kotlin symbols that never occur on the pins are deliberately
// NOT classified: recording a decision about a node type no corpus produces
// would be a guess dressed as an enumeration. The test requires classification
// only of what occurs, and fails the moment something new does.
//
// # Round 2: the census itself was measured against the wrong population
//
// The java column read 120 / N 114 for one round, and 120 is GUAVA'S number. The
// enumeration was intersected against guava alone and published as "the three
// pins". Measured per corpus:
//
//	corpus                       java types occurring   in no bucket
//	guava alone                          120                 0
//	okio .java                            86                 0
//	kotlinx.serialization .java            8                 5
//	ALL THREE PINS (published)           125                 5
//	kotlin, both pins                    141                 0
//
// The five were the JPMS module-descriptor types above, and the D/W/P/N row
// summed to 120 only because unclassified types are dropped rather than counted.
// No rate moved — none of the five can be a call — but the guard
// TestGrammarEnumeration_ClassifiesEveryOccurringNodeType was RED against the
// pins, and nothing ran it there: the PR gate runs it hermetically (where it
// passes) and the corpus workflow ran `-run TestPins`, which excludes it. So the
// method guard had never once executed against the corpus its numbers come from.
// The workflow step is now `-run 'TestPins|TestGrammarEnumeration'` for exactly
// that reason. A census that under-counts is the same defect class as a
// denominator that under-counts — the enumeration exists to say so, and had to
// be held to it first.
//
// WHAT THIS ENUMERATION DOES NOT COVER, stated because it is the real residual:
// it classifies NODE TYPES, not TYPES. `a + b` is classified as an
// operator-convention call whether `a` is an `Int` (a JVM intrinsic, no callee
// exists) or a `BigDecimal` (a genuine `plus` call). No CST can tell those
// apart. That is why the whole family is excluded from the PRIMARY denominator
// and included in the widest one, with both sizes published: the split is
// unmeasurable here, so it is bounded rather than invented.

// nodeBucket is the classification. The zero value is deliberately NOT a valid
// bucket, so a missing map entry is a failure rather than a silent "not a call".
type nodeBucket uint8

const (
	bucketUnclassified nodeBucket = iota
	// bucketDenominator — counted in Denominator(). An invocation expression.
	bucketDenominator
	// bucketWidestOnly — names a callable but is excluded from the primary
	// denominator (operator conventions, constructor delegation, enum
	// arguments); added back by WidestDenominator().
	bucketWidestOnly
	// bucketProtocol — the source names NO callable and the compiler
	// synthesizes one. Measured for both languages, in NO denominator.
	bucketProtocol
	// bucketNotACall — not a call under any reading.
	bucketNotACall
)

// javaNodeBuckets classifies every named java symbol that occurs on the pins.
var javaNodeBuckets = map[string]nodeBucket{
	// ---- D: the primary denominator ----
	"method_invocation":          bucketDenominator, // `a.f()` — the invocation expression
	"object_creation_expression": bucketDenominator, // `new A(1)`, `new A(1){…}`

	// ---- W: names a callable, excluded from the primary denominator ----
	"explicit_constructor_invocation": bucketWidestOnly, // `super(…)` / `this(…)`
	"enum_constant":                   bucketWidestOnly, // `enum E { A(1) }` invokes E(int)

	// ---- P: synthesized protocol, in NO denominator ----
	"enhanced_for_statement": bucketProtocol, // `for (X x : xs)` → iterator/hasNext/next
	"resource":               bucketProtocol, // try-with-resources → close()

	// ---- N: not a call. Written out in full, never a default. ----
	"_element_value": bucketNotACall, "_method_header": bucketNotACall,
	"_unannotated_type": bucketNotACall, "annotated_type": bucketNotACall,
	"annotation": bucketNotACall, "annotation_argument_list": bucketNotACall,
	"annotation_type_body": bucketNotACall, "annotation_type_declaration": bucketNotACall,
	"annotation_type_element_declaration": bucketNotACall, "argument_list": bucketNotACall,
	"array_access": bucketNotACall, "array_creation_expression": bucketNotACall,
	"array_initializer": bucketNotACall, "array_type": bucketNotACall,
	"assert_statement": bucketNotACall, "assignment_expression": bucketNotACall,
	"asterisk": bucketNotACall, "binary_expression": bucketNotACall, "block": bucketNotACall,
	"block_comment": bucketNotACall, "boolean_type": bucketNotACall,
	"break_statement": bucketNotACall, "cast_expression": bucketNotACall,
	"catch_clause": bucketNotACall, "catch_formal_parameter": bucketNotACall,
	"catch_type": bucketNotACall, "character_literal": bucketNotACall,
	"class_body": bucketNotACall, "class_declaration": bucketNotACall,
	"class_literal": bucketNotACall, "constant_declaration": bucketNotACall,
	"constructor_body": bucketNotACall, "constructor_declaration": bucketNotACall,
	"continue_statement": bucketNotACall, "decimal_floating_point_literal": bucketNotACall,
	"decimal_integer_literal": bucketNotACall, "dimensions": bucketNotACall,
	"dimensions_expr": bucketNotACall, "do_statement": bucketNotACall,
	"element_value_array_initializer": bucketNotACall, "element_value_pair": bucketNotACall,
	"enum_body": bucketNotACall, "enum_body_declarations": bucketNotACall,
	"enum_declaration": bucketNotACall, "escape_sequence": bucketNotACall,
	"expression_statement": bucketNotACall, "extends_interfaces": bucketNotACall,
	"false": bucketNotACall, "field_access": bucketNotACall, "field_declaration": bucketNotACall,
	"finally_clause": bucketNotACall, "floating_point_type": bucketNotACall,
	"for_statement": bucketNotACall, "formal_parameter": bucketNotACall,
	"formal_parameters": bucketNotACall, "generic_type": bucketNotACall,
	"hex_floating_point_literal": bucketNotACall, "hex_integer_literal": bucketNotACall,
	"identifier": bucketNotACall, "if_statement": bucketNotACall,
	"import_declaration": bucketNotACall, "inferred_parameters": bucketNotACall,
	"instanceof_expression": bucketNotACall, "integral_type": bucketNotACall,
	"interface_body": bucketNotACall, "interface_declaration": bucketNotACall,
	"labeled_statement": bucketNotACall, "lambda_expression": bucketNotACall,
	"line_comment": bucketNotACall, "local_variable_declaration": bucketNotACall,
	"marker_annotation": bucketNotACall, "method_declaration": bucketNotACall,
	"method_reference": bucketNotACall, "modifiers": bucketNotACall,
	// JPMS module descriptors — `module-info.java`. A module declaration and its
	// directives declare a MODULE GRAPH: which modules are required and which
	// packages are exported. They name no callable, contain no expression and
	// cannot invoke anything; there is no bytecode body for a call to sit in.
	//
	// These five reached the list the same way `record_declaration` did — by
	// failing the enumeration guard rather than by being thought of — and they
	// exposed a worse defect than themselves: the published census said "occurs
	// on the three pins" but had only ever been intersected against GUAVA, whose
	// java is 120 of the 125 that occur. The five come from kotlinx.serialization's
	// six `module-info.java`, the exact files corpus/manifest.json singles out.
	// See the round-2 note below the table.
	"module_declaration": bucketNotACall, "module_body": bucketNotACall,
	"requires_module_directive": bucketNotACall, "requires_modifier": bucketNotACall,
	"exports_module_directive": bucketNotACall,
	"null_literal":             bucketNotACall, "octal_integer_literal": bucketNotACall,
	"package_declaration": bucketNotACall, "parenthesized_expression": bucketNotACall,
	"program": bucketNotACall,
	// A record declaration SYNTHESIZES a canonical constructor and one accessor
	// per component — but it DECLARES them, it does not call them. Not a call.
	//
	// It reached this list by failing the enumeration test rather than by being
	// thought of: guava v33 predates records, so no pin produces the node, and
	// the hermetic fixture does. That is the guard behaving exactly as intended
	// — the classification of a node type the published corpus never exercises
	// is now a recorded decision instead of an accident of coverage.
	"record_declaration":     bucketNotACall,
	"resource_specification": bucketNotACall,
	"return_statement":       bucketNotACall, "scoped_identifier": bucketNotACall,
	"scoped_type_identifier": bucketNotACall, "spread_parameter": bucketNotACall,
	"static_initializer": bucketNotACall, "string_fragment": bucketNotACall,
	"string_literal": bucketNotACall, "super": bucketNotACall, "super_interfaces": bucketNotACall,
	"superclass": bucketNotACall, "switch_block": bucketNotACall,
	"switch_block_statement_group": bucketNotACall, "switch_expression": bucketNotACall,
	"switch_label": bucketNotACall, "synchronized_statement": bucketNotACall,
	"ternary_expression": bucketNotACall, "this": bucketNotACall,
	"throw_statement": bucketNotACall, "throws": bucketNotACall, "true": bucketNotACall,
	"try_statement": bucketNotACall, "try_with_resources_statement": bucketNotACall,
	"type_arguments": bucketNotACall, "type_bound": bucketNotACall,
	"type_identifier": bucketNotACall, "type_list": bucketNotACall,
	"type_parameter": bucketNotACall, "type_parameters": bucketNotACall,
	"unary_expression": bucketNotACall, "update_expression": bucketNotACall,
	"variable_declarator": bucketNotACall, "void_type": bucketNotACall,
	"while_statement": bucketNotACall, "wildcard": bucketNotACall,
}

// kotlinNodeBuckets classifies every named kotlin symbol that occurs on the pins.
var kotlinNodeBuckets = map[string]nodeBucket{
	// ---- D: the primary denominator ----
	"call_suffix": bucketDenominator, // the exact invocation marker, incl. `"${f()}"`

	// ---- W: names a callable, excluded from the primary denominator ----
	"constructor_delegation_call": bucketWidestOnly, // `constructor(x) : this(x, 0)`
	"constructor_invocation":      bucketWidestOnly, // `class A : B(1)` — a call; `@Anno(v)` is not.
	// The PARENT discriminates; see countInvocations
	"enum_entry":                bucketWidestOnly, // `enum class E { A(1) }`
	"infix_expression":          bucketWidestOnly, // `a shl b` — a call iff `infix fun`
	"indexing_suffix":           bucketWidestOnly, // `a[i]` READ and WRITE → get/set
	"additive_expression":       bucketWidestOnly, // `a + b` → plus / minus
	"multiplicative_expression": bucketWidestOnly, // `a * b` → times / div / rem
	"prefix_expression":         bucketWidestOnly, // `-a` `!a` → unaryMinus / not (NOT `@Ann a`)
	"postfix_expression":        bucketWidestOnly, // `a++` → inc / dec (NOT `a!!`)
	"equality_expression":       bucketWidestOnly, // `a == b` → equals (NOT `a === b`)
	"comparison_expression":     bucketWidestOnly, // `a < b` → compareTo
	"range_expression":          bucketWidestOnly, // `a..b` → rangeTo
	"range_test":                bucketWidestOnly, // `when { in 1..5 -> }` → contains
	"check_expression":          bucketWidestOnly, // `a in b` → contains (NOT `a is B`)
	"assignment":                bucketWidestOnly, // `a += b` → plusAssign (NOT plain `a = b`)

	// ---- P: synthesized protocol, in NO denominator ----
	"for_statement":              bucketProtocol, // `for (x in xs)` → iterator/hasNext/next
	"multi_variable_declaration": bucketProtocol, // `val (a, b) = p` → componentN()
	"property_delegate":          bucketProtocol, // `by lazy {}` → getValue / setValue

	// ---- N: not a call. Written out in full, never a default. ----
	"_alpha_identifier": bucketNotACall, "_automatic_semicolon": bucketNotACall,
	"_expression": bucketNotACall, "_lexical_identifier": bucketNotACall,
	"_primary_expression": bucketNotACall, "_simple_user_type": bucketNotACall,
	"_string_start": bucketNotACall, "_type": bucketNotACall, "_type_reference": bucketNotACall,
	"annotated_lambda": bucketNotACall, "annotation": bucketNotACall,
	"anonymous_initializer": bucketNotACall, "as_expression": bucketNotACall,
	"bin_literal": bucketNotACall, "binding_pattern_kind": bucketNotACall,
	"boolean_literal": bucketNotACall, "call_expression": bucketNotACall,
	"callable_reference": bucketNotACall, "catch_block": bucketNotACall,
	"character_escape_seq": bucketNotACall, "character_literal": bucketNotACall,
	"class_body": bucketNotACall, "class_declaration": bucketNotACall,
	"class_modifier": bucketNotACall, "class_parameter": bucketNotACall,
	"collection_literal": bucketNotACall, "companion_object": bucketNotACall,
	"conjunction_expression": bucketNotACall, "control_structure_body": bucketNotACall,
	"delegation_specifier": bucketNotACall, "directly_assignable_expression": bucketNotACall,
	"disjunction_expression": bucketNotACall, "do_while_statement": bucketNotACall,
	"elvis_expression": bucketNotACall, "enum_class_body": bucketNotACall,
	"explicit_delegation": bucketNotACall, "file_annotation": bucketNotACall,
	"finally_block": bucketNotACall, "function_body": bucketNotACall,
	"function_declaration": bucketNotACall, "function_modifier": bucketNotACall,
	"function_type": bucketNotACall, "function_type_parameters": bucketNotACall,
	"function_value_parameters": bucketNotACall, "getter": bucketNotACall,
	"hex_literal": bucketNotACall, "identifier": bucketNotACall, "if_expression": bucketNotACall,
	"import_alias": bucketNotACall, "import_header": bucketNotACall,
	"import_list": bucketNotACall, "indexing_expression": bucketNotACall,
	"inheritance_modifier": bucketNotACall, "integer_literal": bucketNotACall,
	"interpolated_expression": bucketNotACall, "interpolated_identifier": bucketNotACall,
	"jump_expression": bucketNotACall, "label": bucketNotACall, "lambda_literal": bucketNotACall,
	"lambda_parameters": bucketNotACall, "line_comment": bucketNotACall,
	"long_literal": bucketNotACall, "member_modifier": bucketNotACall,
	"modifiers": bucketNotACall, "multiline_comment": bucketNotACall,
	"navigation_expression": bucketNotACall, "navigation_suffix": bucketNotACall,
	"not_nullable_type": bucketNotACall, "null_literal": bucketNotACall,
	"nullable_type": bucketNotACall, "object_declaration": bucketNotACall,
	"object_literal": bucketNotACall, "package_header": bucketNotACall,
	"parameter": bucketNotACall, "parameter_modifier": bucketNotACall,
	"parameter_modifiers": bucketNotACall, "parameter_with_optional_type": bucketNotACall,
	"parenthesized_expression": bucketNotACall, "parenthesized_type": bucketNotACall,
	"platform_modifier": bucketNotACall, "primary_constructor": bucketNotACall,
	"property_declaration": bucketNotACall, "property_modifier": bucketNotACall,
	"quest": bucketNotACall, "real_literal": bucketNotACall, "receiver_type": bucketNotACall,
	"reification_modifier": bucketNotACall, "secondary_constructor": bucketNotACall,
	"setter": bucketNotACall, "simple_identifier": bucketNotACall, "source_file": bucketNotACall,
	"spread_expression": bucketNotACall, "statements": bucketNotACall,
	"string_content": bucketNotACall, "string_literal": bucketNotACall,
	"super_expression": bucketNotACall, "this_expression": bucketNotACall,
	"try_expression": bucketNotACall, "type_alias": bucketNotACall,
	"type_arguments": bucketNotACall, "type_identifier": bucketNotACall,
	"type_modifiers": bucketNotACall, "type_parameter": bucketNotACall,
	"type_parameter_modifiers": bucketNotACall, "type_parameters": bucketNotACall,
	"type_projection": bucketNotACall, "type_projection_modifiers": bucketNotACall,
	"type_test": bucketNotACall, "unsigned_literal": bucketNotACall,
	"use_site_target": bucketNotACall, "user_type": bucketNotACall,
	"value_argument": bucketNotACall, "value_arguments": bucketNotACall,
	"variable_declaration": bucketNotACall, "variance_modifier": bucketNotACall,
	"visibility_modifier": bucketNotACall, "when_condition": bucketNotACall,
	"when_entry": bucketNotACall, "when_expression": bucketNotACall,
	"when_subject": bucketNotACall, "while_statement": bucketNotACall,
	"wildcard_import": bucketNotACall,
}
