package main

// SW-191 (EVALFRESH-001 closure): the LANGUAGE-SCOPED mutation contract.
//
// WHAT EVALFRESH-001 WAS. The freshness/incremental suite could plan a change
// sequence only over Go. Three independent mechanisms stacked:
//
//  1. the file filter accepted only `.go` (closed earlier by the
//     registry-driven `modifiableSourceFile`);
//  2. the package-clause reader (`goPackageClause`) understood only Go's
//     `package <ident>` line, so Java's `package a.b.c;` came back WITH its
//     semicolon and Python/TypeScript came back empty;
//  3. the directory gate then dropped every directory whose clause was empty
//     (`if packages[dir] == "" { continue }`), which is why a Python or
//     TypeScript pin ended with ZERO candidate files and the run aborted with
//     "the index contains no modifiable Go source files to change";
//
// and even past those, the ADD and MODIFY classes wrote GO source into files of
// every language, so the appended `GraphiEvalStepNNNN` was not parseable and the
// convergence probe could never find it. That last one is why the pins that DID
// get past the filter still scored a third to a half of their changes.
//
// WHAT REPLACES IT. One `sourceFamily` per language family. A family states,
// for its own extensions:
//
//   - the shape of its package clause (or that it HAS none — which is a
//     first-class answer, not a missing one);
//   - whether a directory needs a clause before a new sibling file can be
//     added there (Go: yes, a `.go` file without one does not parse; JVM: no,
//     the default package is legal; Python/TS/C: there is nothing to declare);
//   - the declaration text an append introduces, in that language's syntax;
//   - the file name and preamble a newly added sibling carries.
//
// WHY A CLOSED TABLE AND NOT THE PARSER REGISTRY. `parse.NewDefaultRegistry()`
// also registers json, yaml, toml, css, sql, hcl and markdown. Those are real
// parsers over real indexed files, but there is no "append one function" in
// them, so a sequence that targeted them would plan changes it cannot make and
// then report the failure as a freshness result. The harness mutates exactly
// the languages it knows how to mutate, and says so; a language with no entry
// here is NOT a candidate, which is a narrower and truer claim than "every
// extension the index can parse".
//
// THE VACUITY GUARD. Each family's clause reader is scoped: the Go reader
// REJECTS a dotted name (that is a JVM clause, not a Go one) and the JVM reader
// strips the Java terminator that Kotlin does not write. `absent` returns the
// empty string for every input, including a file that happens to contain the
// word `package`. See sourcefamily_test.go and the per-family freshness tests.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// packageClauseShape names how a language declares the namespace a newly added
// sibling file joins.
type packageClauseShape string

const (
	// packageClauseAbsent — the language HAS NO package clause. Python, the
	// TypeScript family, the C family, Ruby and Rust locate a module by its
	// PATH. A directory of theirs is admissible with no clause at all, and the
	// directory gate must not drop it.
	packageClauseAbsent packageClauseShape = "absent"
	// packageClauseGoIdent — Go's `package <ident>`: a single identifier, never
	// dotted, never terminated.
	packageClauseGoIdent packageClauseShape = "go-ident"
	// packageClauseJVMDotted — the JVM's `package a.b.c`, with a MANDATORY
	// terminating semicolon in Java and none in Kotlin. Both are read here and
	// the terminator is stripped, which is the half of EVALFRESH-001 that
	// widening the file filter alone did not reach.
	packageClauseJVMDotted packageClauseShape = "jvm-dotted"
)

// sourceFamily is one language family's mutation contract. Everything the
// change sequence needs to plan and render a change in that language is here,
// so adding a language is a table entry and never an `if` in the sequence.
type sourceFamily struct {
	// name is the family identifier published in the sequence method string.
	name string
	// exts are the file extensions this family owns, lowercase, with the dot.
	exts []string
	// clause is the shape of this language's package declaration.
	clause packageClauseShape
	// clauseRequired states whether a directory MUST have a declared clause
	// before a new sibling file may be added there. Go: yes. Everything else:
	// no — the JVM default package is legal and the rest have no clause at all.
	clauseRequired bool
	// addedFileBase renders the base name an ADD step creates. Java needs the
	// file name to match the type it declares; the rest do not care and take a
	// snake-cased name carrying the step index.
	addedFileBase func(symbol string, index int) string
	// declaration renders one self-contained top-level declaration of symbol,
	// valid at the end of any file of this family and dependent on nothing the
	// file already contains.
	declaration func(symbol string, index int) string
	// preamble renders the package/namespace header a NEW file of this family
	// needs, given the directory's clause (possibly empty).
	preamble func(pkg string) string
	// appendPrefix is text an APPEND must emit before its declaration to put
	// the file back into a state where a declaration is legal. Only PHP needs
	// one (`?>` closes whatever mode the file ended in, `<?php` reopens code
	// mode), and it is a field rather than an `if` so the shape stays in the
	// table with the language it belongs to.
	appendPrefix string
	// excluded reports paths this family will not modify even though the
	// extension matches.
	excluded func(p string) bool
}

// note renders the provenance comment every generated declaration carries, in
// the family's line-comment syntax. It exists so a reader who finds a
// GraphiEvalStep symbol in a working tree knows what put it there.
func lineComment(marker, symbol string, index int) string {
	return marker + " " + symbol + " is written by the SW-126/SW-191 freshness harness (step " +
		fmt.Sprint(index) + "). It is removed with the working tree.\n"
}

// sourceFamilies is the closed table. Order is irrelevant (lookup is by
// extension and no extension appears twice). The ordering is roughly by how
// early the language entered the corpus; pin status is NOT the grouping and is
// stated per family at the divider below, because grouping by it was wrong.
var sourceFamilies = []sourceFamily{
	// ---------------------------------------------------------------- go ---
	// The control. cobra is the pin, and this entry must stay byte-for-byte
	// equivalent to the pre-SW-191 behaviour: the cobra run is what proves a
	// non-Go abort was language scope and not a broken harness, so a change
	// here would destroy the control.
	{
		name:           "go",
		exts:           []string{".go"},
		clause:         packageClauseGoIdent,
		clauseRequired: true,
		addedFileBase:  func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.go", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) + "func " + sym + "() int { return " + fmt.Sprint(n) + " }\n"
		},
		preamble: func(pkg string) string {
			if pkg == "" {
				// Unreachable while clauseRequired holds; a deliberate
				// placeholder rather than an empty clause, which would not
				// parse and would fail the step for the wrong reason.
				pkg = "main"
			}
			return "package " + pkg + "\n\n"
		},
		// Go's `_test.go` has a different ingest shape, which would make the
		// change classes incomparable with the ordinary-source ones.
		excluded: func(p string) bool { return strings.HasSuffix(p, "_test.go") },
	},
	// -------------------------------------------------------------- java ---
	// guava is the pin. A second TOP-LEVEL type in a `.java` file is legal as
	// long as it is not public, which is why the declaration is package-private
	// and why the ADD step names the file after the type it declares.
	{
		name:          "java",
		exts:          []string{".java"},
		clause:        packageClauseJVMDotted,
		addedFileBase: func(sym string, _ int) string { return sym + ".java" },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"class " + sym + " {\n    int graphiEvalValue() { return " + fmt.Sprint(n) + "; }\n}\n"
		},
		preamble: func(pkg string) string {
			if pkg == "" {
				return "" // the default package is legal Java
			}
			return "package " + pkg + ";\n\n"
		},
	},
	// ------------------------------------------------------------ kotlin ---
	// okio and kotlinx.serialization are the pins. Kotlin has top-level
	// functions and free file names, and its package clause carries no
	// terminator.
	{
		name:          "kotlin",
		exts:          []string{".kt"},
		clause:        packageClauseJVMDotted,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.kt", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) + "fun " + sym + "(): Int = " + fmt.Sprint(n) + "\n"
		},
		preamble: func(pkg string) string {
			if pkg == "" {
				return "" // a Kotlin file may legally declare no package
			}
			return "package " + pkg + "\n\n"
		},
	},
	// ------------------------------------------------------------ python ---
	// flask is the pin. Python has NO package clause: a directory is a package
	// by virtue of its path, so `clauseRequired` is false and the reader
	// returns the empty string for every input. A column-0 `def` appended to
	// any file closes whatever block preceded it, so the declaration is valid
	// wherever the file happened to end.
	{
		name:          "python",
		exts:          []string{".py"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.py", n) },
		declaration: func(sym string, n int) string {
			return lineComment("#", sym, n) + "def " + sym + "():\n    return " + fmt.Sprint(n) + "\n"
		},
		preamble: func(string) string { return "" },
	},
	// -------------------------------------------- typescript / tsx / js ---
	// ky is the .ts/.tsx pin and express the .js pin. The family shares one
	// resolver but three grammars, so it gets three entries rather than one
	// with three extensions: a per-language entry is what lets a future change
	// to the .tsx shape stay a .tsx change.
	{
		name:          "typescript",
		exts:          []string{".ts"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.ts", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"export function " + sym + "(): number {\n    return " + fmt.Sprint(n) + ";\n}\n"
		},
		preamble: func(string) string { return "" },
		// `.d.ts` files declare types only; an exported function BODY in one is
		// not valid, so they are not candidates.
		excluded: func(p string) bool { return strings.HasSuffix(p, ".d.ts") },
	},
	{
		name:          "tsx",
		exts:          []string{".tsx"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.tsx", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"export function " + sym + "(): number {\n    return " + fmt.Sprint(n) + ";\n}\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "javascript",
		exts:          []string{".js"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.js", n) },
		// NOT `export function`: express is CommonJS, and a bare function
		// declaration is valid in both module systems while an ESM export is
		// valid in only one.
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"function " + sym + "() {\n    return " + fmt.Sprint(n) + ";\n}\n"
		},
		preamble: func(string) string { return "" },
	},
	// ------------------------------------------- pin status, per family ---
	// SW-191 rebuild round 1. This divider used to read "languages with no
	// pin" and to claim that nothing below it "is exercised by a -full-run
	// today". Both halves were wrong about the very first entry: `ruby` HAS a
	// corpus pin — sinatra, tier 3, corpus/manifest.json — and
	// `-full-run sinatra -incremental-changes 100` reaches 99/100 on this
	// change where it exited 1 before it. So the pin status is stated as a
	// fact rather than implied by where the entry sits.
	//
	// WITH a corpus pin (13, post-SW-196 W5.j): go (cobra, uuid, lo, gin,
	// grpc-go, kubernetes), java (guava), kotlin (okio, kotlinx.serialization),
	// python (flask), typescript (ky), javascript (express), ruby (sinatra),
	// c (cjson), csharp (Newtonsoft.Json), cpp (nlohmann/json), lua
	// (lua-resty-core), php (composer), rust (serde).
	//
	// WITHOUT one (2): tsx — listed above beside typescript for readability,
	// but ky is a `.ts` pin, not a `.tsx` one — and bash (no representative
	// open-source bash project at the pin tier; SW-196 honest abstention,
	// corpus/manifest.json bash-abstention). SQL is NOT a source family in
	// this table because the SW-196 W5.j honest abstention names its lack of
	// a corpus pin — the table's extension map is `[".sh", ".bash"]` for bash,
	// and no SQL grammar wires a per-file mutation contract, so SQL is not a
	// family here and the 15-of-15 count is unaffected.
	//
	// An unpinned family is exercised only by the hermetic parse tests in
	// sourcefamily_test.go, never by a -full-run. It is entered anyway because
	// the alternative is that the FIRST pin in one of them re-opens
	// EVALFRESH-001 by surprise; that difference is stated rather than glossed.
	// One caveat the pinned/unpinned split does NOT capture: bash has no pin,
	// yet grpc-go's 16 shell scripts DO enter its candidate list (542 -> 558
	// candidate files). 100 changes never reach them, so no bash file is
	// mutated today, but a longer run would mutate one.
	{
		name:          "ruby",
		exts:          []string{".rb"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.rb", n) },
		declaration: func(sym string, n int) string {
			return lineComment("#", sym, n) + "def " + sym + "\n  " + fmt.Sprint(n) + "\nend\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "rust",
		exts:          []string{".rs"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.rs", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"#[allow(non_snake_case)]\npub fn " + sym + "() -> i32 { " + fmt.Sprint(n) + " }\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "c",
		exts:          []string{".c", ".h"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.c", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) + "int " + sym + "(void) { return " + fmt.Sprint(n) + "; }\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "cpp",
		exts:          []string{".cpp", ".cc", ".cxx", ".hpp"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.cpp", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) + "int " + sym + "() { return " + fmt.Sprint(n) + "; }\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "csharp",
		exts:          []string{".cs"},
		clause:        packageClauseAbsent, // `namespace` is not read: the added type goes in the global namespace, which is legal
		addedFileBase: func(sym string, _ int) string { return sym + ".cs" },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"class " + sym + " {\n    int GraphiEvalValue() { return " + fmt.Sprint(n) + "; }\n}\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "php",
		exts:          []string{".php"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.php", n) },
		declaration: func(sym string, n int) string {
			return lineComment("//", sym, n) +
				"function " + sym + "() { return " + fmt.Sprint(n) + "; }\n"
		},
		preamble: func(string) string { return "<?php\n\n" },
		// `?>` leaves code mode (or is literal output if the file already left
		// it) and `<?php` re-enters, so the appended function is legal whether
		// the file ended inside a PHP block or after one.
		appendPrefix: "?>\n<?php\n",
	},
	{
		name:          "lua",
		exts:          []string{".lua"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.lua", n) },
		declaration: func(sym string, n int) string {
			return lineComment("--", sym, n) + "function " + sym + "() return " + fmt.Sprint(n) + " end\n"
		},
		preamble: func(string) string { return "" },
	},
	{
		name:          "bash",
		exts:          []string{".sh", ".bash"},
		clause:        packageClauseAbsent,
		addedFileBase: func(_ string, n int) string { return fmt.Sprintf("graphi_eval_step%04d.sh", n) },
		declaration: func(sym string, n int) string {
			return lineComment("#", sym, n) + sym + "() {\n  return " + fmt.Sprint(n) + "\n}\n"
		},
		preamble: func(string) string { return "" },
	},
}

// familyByExt is the lookup table, built once. A duplicate extension is a
// programming error in the table above and panics at init rather than silently
// letting one family shadow another.
var familyByExt = func() map[string]*sourceFamily {
	out := map[string]*sourceFamily{}
	for i := range sourceFamilies {
		f := &sourceFamilies[i]
		for _, ext := range f.exts {
			if prev, dup := out[ext]; dup {
				panic("cmd/eval: extension " + ext + " claimed by both " + prev.name + " and " + f.name)
			}
			out[ext] = f
		}
	}
	return out
}()

// familyForPath returns the mutation contract for p, or nil when the harness
// has no way to mutate a file of that type. A nil family is the honest answer
// for json, yaml, css, markdown and everything else the index can parse but
// nobody writes a function into.
func familyForPath(p string) *sourceFamily {
	f, ok := familyByExt[strings.ToLower(filepath.Ext(p))]
	if !ok {
		return nil
	}
	if f.excluded != nil && f.excluded(p) {
		return nil
	}
	return f
}

// readPackageClause reads the namespace a new sibling file in this directory
// must declare, in the family's own shape.
//
// It is a line scan rather than a real parser: the harness only needs the name,
// and a file that does not parse is one this sequence should not be adding
// siblings to anyway. Each shape narrows to ITS OWN syntax — the point of
// EVALFRESH-001's closure is that reading a Java clause with Go's reader is a
// wrong answer, not a missing one.
func readPackageClause(shape packageClauseShape, raw []byte) string {
	if shape == packageClauseAbsent {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package ")
		if !ok {
			continue
		}
		name := strings.TrimSpace(rest)
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		if i := strings.Index(name, "//"); i >= 0 {
			name = name[:i]
		}
		if i := strings.Index(name, "/*"); i >= 0 {
			name = name[:i]
		}
		switch shape {
		case packageClauseGoIdent:
			// A Go package clause is ONE identifier. A dot or a terminator
			// means this line is some other language's clause, and handing it
			// to a Go ADD step would write a file that does not parse.
			if strings.ContainsAny(name, ".;") {
				return ""
			}
		case packageClauseJVMDotted:
			// Java terminates the clause; Kotlin does not. Both arrive here.
			name = strings.TrimSuffix(name, ";")
		}
		if name != "" {
			return name
		}
	}
	return ""
}

// packageClause reads the clause for this family.
func (f *sourceFamily) packageClause(raw []byte) string {
	return readPackageClause(f.clause, raw)
}

// appended renders the text a MODIFY or CROSS_PACKAGE step appends to an
// existing file: one self-contained top-level declaration in the family's
// syntax, depending on nothing the file already holds.
func (f *sourceFamily) appended(symbol string, index int) string {
	return "\n" + f.appendPrefix + f.declaration(symbol, index)
}

// added renders a whole new file of this family for the directory's clause.
func (f *sourceFamily) added(pkg, symbol string, index int) string {
	return f.preamble(pkg) + f.declaration(symbol, index)
}

// familyNames is the ordered list of family names, for the published method
// string. Derived from the table so the string cannot fall behind it.
func familyNames() []string {
	out := make([]string, 0, len(sourceFamilies))
	for i := range sourceFamilies {
		out = append(out, sourceFamilies[i].name)
	}
	return out
}

// packageKey is the key under which a directory's package clause is recorded.
//
// It is per (family, directory) and not per directory alone: two families can
// share a directory (a `.ts` and a `.js` beside each other is ordinary, and a
// `.java` beside a `.kt` happens in mixed JVM trees), and their clauses are not
// interchangeable. Keying by directory alone would let one family's clause be
// written into the other family's added file.
func packageKey(dir string, f *sourceFamily) string {
	return f.name + "\x00" + dir
}
