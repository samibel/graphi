package parity

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// The JVM half of the real-repository harness (WP-J7 / SW-176).
//
// WHY THIS IS A SECOND MODEL AND NOT A GENERALISATION OF RepoModel.
//
// RepoModel is built with go/parser and its whole vocabulary — packages, import
// paths, *ast.FuncDecl — is Go's. A "generalised" model would have to be the
// intersection of Go and the JVM, which is nearly empty: Java has no package
// clause per directory that graphi keys on the same way, Kotlin has top-level
// functions Go has no analogue for, and neither has a go.mod. Sharing the type
// would cost more in nil-checks than it saves in lines, and — more importantly —
// it would let a Go-shaped assumption leak into a JVM row silently.
//
// WHAT THIS MODEL IS ALLOWED TO BE. Exactly what RepoModel is allowed to be: a
// read-only view whose ONLY job is to LOCATE a real edit target in real source.
// It never models the graph, never resolves a type, never imports engine/ or
// core/parse, and it is deliberately NOT tree-sitter — using the product's own
// parser to choose the edit the product is then measured on would couple the
// instrument to the subject. Every graph in this harness is produced by the
// graphi binary running as a SUBPROCESS.
//
// Consequence, stated rather than discovered later: this scanner is approximate.
// It masks comments and string/char literals (so a brace inside a string cannot
// desynchronise it) and then works on brace depth and a small set of keyword
// shapes. It will miss constructs it does not know. That is ACCEPTABLE because a
// miss produces errNoTarget — the row skips and says so — and never a wrong
// edit: every planner re-reads the real bytes it is about to rewrite.
// ---------------------------------------------------------------------------

// JVM language ids, as the manifest spells them.
const (
	langJava   = "java"
	langKotlin = "kotlin"
)

// JVMImport is one import statement.
type JVMImport struct {
	// Path is the imported name without the trailing ".*" of an on-demand
	// import: "java.util.List", or "java.util" for "import java.util.*;".
	Path string
	// OnDemand is true for `import a.b.*;` (Java) / `import a.b.*` (Kotlin).
	OnDemand bool
	// Static marks a Java `import static`.
	Static bool
	// Start/End are byte offsets of the whole statement line in Src.
	Start, End int
}

// JVMMethod is one method or function declaration.
type JVMMethod struct {
	Name string
	// Arity is the declared parameter count. It is what jvm_change_overload
	// needs: the ADR 0008 D6 drop rule keys on (name, arity), never on types.
	Arity int
	// Start/End bound the whole declaration including its body.
	Start, End int
}

// JVMType is one type declaration.
type JVMType struct {
	Name string
	// Kind is one of class, interface, enum, record, object (Kotlin).
	Kind string
	// Super is the first supertype named after `extends` (Java) or after `:`
	// (Kotlin), empty when the declaration names none.
	Super string
	// SuperStart/SuperEnd bound Super's identifier in Src, so it can be
	// re-pointed without re-parsing.
	SuperStart, SuperEnd int
	// Start is the offset of the declaration's first keyword; End is just past
	// its closing brace.
	Start, End int
	// BodyStart/BodyEnd are the offsets just inside the braces.
	BodyStart, BodyEnd int
	// Nested holds ONE level of nested type declarations. Nested types mint no
	// node (qn.go), which is exactly what jvm_move_nested_class is about.
	Nested []JVMType
	// Methods are the declarations at this type's own body depth.
	Methods []JVMMethod
}

// JVMFile is one parsed .java or .kt source file.
type JVMFile struct {
	Rel  string // repo-relative, slash-separated
	Abs  string
	Src  []byte
	Dir  string // repo-relative dir, "." at the root
	Lang string // langJava | langKotlin
	// Pkg is the package clause, "" when the file declares none.
	Pkg string
	// PkgStart/PkgEnd bound the dotted package name in Src.
	PkgStart, PkgEnd int
	// ImportEnd is the offset just past the last import statement (or past the
	// package clause when there are none) — where a new import is inserted.
	ImportEnd int
	Imports   []JVMImport
	Types     []JVMType
	// TopFuncs are Kotlin top-level functions (Java has none).
	TopFuncs []JVMMethod
}

// JVMDir is one source directory of a materialized clone.
//
// The DIRECTORY is the unit rather than the package, because that is the unit
// graphi's JVM side keys on: a JVM node's qualified name is built from the file
// directory (qn.go filePackage), and ADR 0008 ruling D9 made the stale-confirmed
// sweep unit (directory, language). A model keyed on the package clause could
// not express the mixed-language directory the two W0.h rows are about.
type JVMDir struct {
	Dir string
	// Pkg is the package clause the directory's files agree on, or the
	// lexicographically first one when they do not.
	Pkg   string
	Files []*JVMFile
	// Java and Kotlin count this directory's files per language. Both non-zero
	// is a MIXED-LANGUAGE directory.
	Java, Kotlin int
}

// Mixed reports whether the directory holds both languages' sources.
func (d *JVMDir) Mixed() bool { return d.Java > 0 && d.Kotlin > 0 }

// JVMModel is the harness's read-only view of a materialized JVM clone.
type JVMModel struct {
	Root  string
	Files []*JVMFile // sorted by Rel
	Dirs  []*JVMDir  // sorted by Dir
	byDir map[string]*JVMDir
}

// dirOf returns the model's directory record, or nil.
func (m *JVMModel) dirOf(dir string) *JVMDir { return m.byDir[dir] }

// jvmSkipDir names the directory bases the model never walks into.
//
// Why `build`, `out` and `.gradle` are here and `testdata` is not: the JVM pins
// are Gradle/Maven trees whose build outputs are generated sources the harness
// must not choose as an edit target (they are not in git, so `git restore` would
// not revert an edit to them). `test` directories ARE walked: graphi indexes
// them, so an edit there is a real change to a real indexed file, and excluding
// them would shrink the target pool for no stated reason.
func jvmSkipDir(base string) bool {
	switch base {
	case ".git", "build", "out", "target", ".gradle", ".idea", "node_modules":
		return true
	}
	return strings.HasPrefix(base, ".")
}

// discoverJVM scans a clone for .java and .kt sources.
func discoverJVM(root string) (*JVMModel, error) {
	m := &JVMModel{Root: root, byDir: map[string]*JVMDir{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && jvmSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		lang := ""
		switch {
		case strings.HasSuffix(rel, ".java"):
			lang = langJava
		case strings.HasSuffix(rel, ".kt"):
			lang = langKotlin
		default:
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		f := parseJVMFile(rel, p, src, lang)
		m.Files = append(m.Files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parity: scan JVM clone %s: %w", root, err)
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("parity: %s holds no .java or .kt source", root)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Rel < m.Files[j].Rel })
	for _, f := range m.Files {
		d := m.byDir[f.Dir]
		if d == nil {
			d = &JVMDir{Dir: f.Dir}
			m.byDir[f.Dir] = d
			m.Dirs = append(m.Dirs, d)
		}
		d.Files = append(d.Files, f)
		if f.Lang == langJava {
			d.Java++
		} else {
			d.Kotlin++
		}
		if f.Pkg != "" && (d.Pkg == "" || f.Pkg < d.Pkg) {
			d.Pkg = f.Pkg
		}
	}
	sort.Slice(m.Dirs, func(i, j int) bool { return m.Dirs[i].Dir < m.Dirs[j].Dir })
	return m, nil
}

// ---------------------------------------------------------------------------
// The scanner.
// ---------------------------------------------------------------------------

// mask returns a copy of src in which every byte inside a comment, a string
// literal or a character literal is replaced by a space, and every other byte —
// including all newlines — is preserved at its original offset.
//
// This is what makes brace counting safe. A `"}"` inside a string literal and a
// `{` inside a // comment are both extremely common in real JVM source, and a
// naive depth counter desynchronises on the first one. Offsets are preserved so
// every position found in the mask indexes the real bytes directly.
func mask(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	blank := func(i int) {
		if src[i] != '\n' {
			out[i] = ' '
		}
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for ; i < len(src) && src[i] != '\n'; i++ {
				blank(i)
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			blank(i)
			blank(i + 1)
			i += 2
			for i < len(src) && !(src[i] == '*' && i+1 < len(src) && src[i+1] == '/') {
				blank(i)
				i++
			}
			if i < len(src) {
				blank(i)
				if i+1 < len(src) {
					blank(i + 1)
				}
				i += 2
			}
		case c == '"' && i+2 < len(src) && src[i+1] == '"' && src[i+2] == '"':
			// Kotlin / Java text block. Ends at the next unescaped triple quote.
			blank(i)
			blank(i + 1)
			blank(i + 2)
			i += 3
			for i < len(src) && !(src[i] == '"' && i+2 < len(src) && src[i+1] == '"' && src[i+2] == '"') {
				blank(i)
				i++
			}
			for j := 0; j < 3 && i < len(src); j, i = j+1, i+1 {
				blank(i)
			}
		case c == '"' || c == '\'':
			quote := c
			blank(i)
			i++
			for i < len(src) && src[i] != quote && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) {
					blank(i)
					i++
				}
				blank(i)
				i++
			}
			if i < len(src) && src[i] == quote {
				blank(i)
				i++
			}
		default:
			i++
		}
	}
	return out
}

func isIdentRune(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// wordAt reports whether msk holds the exact word w at offset i (identifier
// boundaries on both sides).
func wordAt(msk []byte, i int, w string) bool {
	if i+len(w) > len(msk) || string(msk[i:i+len(w)]) != w {
		return false
	}
	if i > 0 && isIdentRune(msk[i-1]) {
		return false
	}
	if i+len(w) < len(msk) && isIdentRune(msk[i+len(w)]) {
		return false
	}
	return true
}

// skipSpace advances past whitespace.
func skipSpace(msk []byte, i int) int {
	for i < len(msk) && (msk[i] == ' ' || msk[i] == '\t' || msk[i] == '\n' || msk[i] == '\r') {
		i++
	}
	return i
}

// readIdent reads an identifier starting at i and returns it with the offset
// just past it.
func readIdent(msk []byte, i int) (string, int) {
	start := i
	for i < len(msk) && isIdentRune(msk[i]) {
		i++
	}
	return string(msk[start:i]), i
}

// readDotted reads a dotted name (a.b.C), tolerating the Kotlin backtick form
// only by stopping at it.
func readDotted(msk []byte, i int) (string, int) {
	start := i
	for i < len(msk) && (isIdentRune(msk[i]) || msk[i] == '.') {
		i++
	}
	return string(msk[start:i]), i
}

// matchBrace returns the offset of the '}' matching the '{' at open, or -1.
func matchBrace(msk []byte, open int) int {
	depth := 0
	for i := open; i < len(msk); i++ {
		switch msk[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// matchParen returns the offset of the ')' matching the '(' at open, or -1.
func matchParen(msk []byte, open int) int {
	depth := 0
	for i := open; i < len(msk); i++ {
		switch msk[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// arityOf counts declared parameters between open '(' and its match.
//
// Commas at nesting depth > 0 are ignored, so `Map<String, Integer> m` counts
// as ONE parameter and not two. That matters: arity is the key ADR 0008 D6's
// overload-drop rule uses, and a generic parameter miscounted as two would make
// jvm_change_overload plan an overload that is not one.
func arityOf(msk []byte, open, close int) int {
	inner := strings.TrimSpace(string(msk[open+1 : close]))
	if inner == "" {
		return 0
	}
	n, depth := 1, 0
	for i := open + 1; i < close; i++ {
		switch msk[i] {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}

var jvmTypeKeywords = []string{"class", "interface", "enum", "record", "object"}

// parseJVMFile builds the file model.
func parseJVMFile(rel, abs string, src []byte, lang string) *JVMFile {
	f := &JVMFile{Rel: rel, Abs: abs, Src: src, Lang: lang, Dir: path.Dir(rel)}
	if f.Dir == "" {
		f.Dir = "."
	}
	msk := mask(src)

	// package clause
	for i := 0; i < len(msk); i++ {
		if !wordAt(msk, i, "package") {
			continue
		}
		j := skipSpace(msk, i+len("package"))
		name, end := readDotted(msk, j)
		if name != "" {
			f.Pkg, f.PkgStart, f.PkgEnd = name, j, end
			f.ImportEnd = lineEnd(src, end)
		}
		break
	}

	// imports
	for i := 0; i < len(msk); i++ {
		if !wordAt(msk, i, "import") {
			continue
		}
		j := skipSpace(msk, i+len("import"))
		static := false
		if wordAt(msk, j, "static") {
			static = true
			j = skipSpace(msk, j+len("static"))
		}
		name, end := readDotted(msk, j)
		if name == "" {
			continue
		}
		onDemand := false
		k := end
		if k < len(msk) && msk[k] == '*' {
			onDemand = true
			k++
		} else if strings.HasSuffix(name, ".") {
			continue
		}
		name = strings.TrimSuffix(name, ".")
		stmtEnd := lineEnd(src, k)
		f.Imports = append(f.Imports, JVMImport{
			Path: name, OnDemand: onDemand, Static: static, Start: i, End: stmtEnd,
		})
		if stmtEnd > f.ImportEnd {
			f.ImportEnd = stmtEnd
		}
		i = k
	}

	f.Types = scanTypes(msk, src, 0, len(msk), lang, true)
	if lang == langKotlin {
		f.TopFuncs = scanFuncs(msk, 0, len(msk), lang, topLevelOnly(msk))
	}
	return f
}

// topLevelOnly returns a predicate admitting offsets at brace depth 0.
func topLevelOnly(msk []byte) func(int) bool {
	// Precompute depth prefix so the predicate is O(1) per call.
	depth := make([]int16, len(msk)+1)
	d := int16(0)
	for i := 0; i < len(msk); i++ {
		depth[i] = d
		switch msk[i] {
		case '{':
			d++
		case '}':
			d--
		}
	}
	depth[len(msk)] = d
	return func(i int) bool { return i >= 0 && i <= len(msk) && depth[i] == 0 }
}

// scanTypes finds type declarations between lo and hi.
//
// It walks the masked source and, at each type keyword whose brace depth inside
// [lo,hi) is the expected one, reads the name, an optional supertype and the
// body. `top` distinguishes the file level (depth 0) from a type body (depth 1),
// which is the whole nested/top-level distinction jvm_move_nested_class exists
// to exercise.
func scanTypes(msk, src []byte, lo, hi int, lang string, top bool) []JVMType {
	var out []JVMType
	depth := 0
	for i := lo; i < hi; i++ {
		switch msk[i] {
		case '{':
			depth++
			continue
		case '}':
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		kw := ""
		for _, k := range jvmTypeKeywords {
			if wordAt(msk, i, k) {
				kw = k
				break
			}
		}
		if kw == "" {
			continue
		}
		// Kotlin `object :` (an anonymous object expression) and Java's
		// `new Foo() { … }` are not declarations; both are excluded by
		// requiring an identifier after the keyword.
		j := skipSpace(msk, i+len(kw))
		name, after := readIdent(msk, j)
		if name == "" {
			continue
		}
		open := indexByteFrom(msk, after, '{')
		if open < 0 {
			continue
		}
		// A brace belonging to something else between the name and the body
		// (e.g. an annotation array) would make this wrong; requiring no ';'
		// in between rejects an abstract/`external` declaration with no body.
		if semi := indexByteFrom(msk, after, ';'); semi >= 0 && semi < open {
			continue
		}
		close := matchBrace(msk, open)
		if close < 0 {
			continue
		}
		t := JVMType{
			Name: name, Kind: kw, Start: i, End: close + 1,
			BodyStart: open + 1, BodyEnd: close,
		}
		t.Super, t.SuperStart, t.SuperEnd = scanSuper(msk, after, open, lang)
		t.Methods = scanFuncs(msk, t.BodyStart, t.BodyEnd, lang, depthOneIn(msk, t.BodyStart, t.BodyEnd))
		if top {
			t.Nested = scanTypes(msk, src, t.BodyStart, t.BodyEnd, lang, false)
		}
		out = append(out, t)
		i = close
		depth = 0
	}
	return out
}

// depthOneIn returns a predicate admitting offsets at the immediate body depth
// of [lo,hi) — i.e. a member of the type rather than a local of a method.
func depthOneIn(msk []byte, lo, hi int) func(int) bool {
	return func(i int) bool {
		if i < lo || i >= hi {
			return false
		}
		d := 0
		for k := lo; k < i; k++ {
			switch msk[k] {
			case '{':
				d++
			case '}':
				d--
			}
		}
		return d == 0
	}
}

// scanSuper reads the first named supertype between the type name and the body.
func scanSuper(msk []byte, after, open int, lang string) (string, int, int) {
	if lang == langJava {
		for i := after; i < open; i++ {
			if !wordAt(msk, i, "extends") {
				continue
			}
			j := skipSpace(msk, i+len("extends"))
			name, end := readDotted(msk, j)
			if name != "" {
				return name, j, end
			}
		}
		return "", 0, 0
	}
	// Kotlin: the supertype list follows a ':' that is not part of a
	// constructor parameter type, so only a ':' at paren depth 0 counts.
	depth := 0
	for i := after; i < open; i++ {
		switch msk[i] {
		case '(', '<':
			depth++
		case ')', '>':
			depth--
		case ':':
			if depth != 0 {
				continue
			}
			j := skipSpace(msk, i+1)
			name, end := readDotted(msk, j)
			if name != "" {
				return name, j, end
			}
			return "", 0, 0
		}
	}
	return "", 0, 0
}

// scanFuncs finds method/function declarations in [lo,hi) admitted by `at`.
func scanFuncs(msk []byte, lo, hi int, lang string, at func(int) bool) []JVMMethod {
	var out []JVMMethod
	for i := lo; i < hi; i++ {
		if lang == langKotlin {
			if !wordAt(msk, i, "fun") || !at(i) {
				continue
			}
			j := skipSpace(msk, i+len("fun"))
			// Skip an optional receiver/type-parameter prefix conservatively:
			// only the simple `fun name(` shape is modelled.
			name, after := readIdent(msk, j)
			if name == "" {
				continue
			}
			after = skipSpace(msk, after)
			if after >= hi || msk[after] != '(' {
				continue
			}
			cl := matchParen(msk, after)
			if cl < 0 || cl >= hi {
				continue
			}
			end := kotlinFuncEnd(msk, cl+1, hi)
			out = append(out, JVMMethod{Name: name, Arity: arityOf(msk, after, cl), Start: i, End: end})
			i = end - 1
			continue
		}
		// Java: an identifier immediately followed by '(' whose matching ')' is
		// followed (past any throws clause) by '{'. The identifier must be
		// preceded by another identifier or '>' — a return type — which is what
		// separates a declaration from a call site.
		if msk[i] != '(' || !at(i) {
			continue
		}
		nameEnd := i
		for nameEnd > lo && (msk[nameEnd-1] == ' ' || msk[nameEnd-1] == '\t') {
			nameEnd--
		}
		nameStart := nameEnd
		for nameStart > lo && isIdentRune(msk[nameStart-1]) {
			nameStart--
		}
		if nameStart == nameEnd {
			continue
		}
		prev := nameStart
		for prev > lo && (msk[prev-1] == ' ' || msk[prev-1] == '\t' || msk[prev-1] == '\n') {
			prev--
		}
		if prev == lo || !(isIdentRune(msk[prev-1]) || msk[prev-1] == '>' || msk[prev-1] == ']') {
			continue
		}
		cl := matchParen(msk, i)
		if cl < 0 || cl >= hi {
			continue
		}
		open := indexByteFrom(msk, cl+1, '{')
		semi := indexByteFrom(msk, cl+1, ';')
		if open < 0 || open >= hi || (semi >= 0 && semi < open) {
			continue // abstract / interface method: no body to bound
		}
		body := matchBrace(msk, open)
		if body < 0 || body >= hi {
			continue
		}
		out = append(out, JVMMethod{
			Name: string(msk[nameStart:nameEnd]), Arity: arityOf(msk, i, cl),
			Start: nameStart, End: body + 1,
		})
		i = body
	}
	return out
}

// kotlinFuncEnd bounds a Kotlin function: either its block body or its
// expression body up to the end of the line.
func kotlinFuncEnd(msk []byte, from, hi int) int {
	i := skipSpace(msk, from)
	if i < hi && msk[i] == ':' {
		// return type
		i = skipSpace(msk, i+1)
		_, i = readDotted(msk, i)
		for i < hi && (msk[i] == '<' || msk[i] == '>' || msk[i] == '?' || msk[i] == ',' ||
			msk[i] == ' ' || isIdentRune(msk[i]) || msk[i] == '.') {
			i++
		}
		i = skipSpace(msk, i)
	}
	if i < hi && msk[i] == '{' {
		if e := matchBrace(msk, i); e >= 0 && e < hi {
			return e + 1
		}
	}
	if i < hi && msk[i] == '=' {
		for ; i < hi && msk[i] != '\n'; i++ {
		}
		return i
	}
	return i
}

func indexByteFrom(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// lineEnd returns the offset just past the newline ending the line that
// contains i.
func lineEnd(src []byte, i int) int {
	for ; i < len(src); i++ {
		if src[i] == '\n' {
			return i + 1
		}
	}
	return len(src)
}

// ---------------------------------------------------------------------------
// Selection helpers. Every one is DETERMINISTIC: candidates are walked in
// sorted order and the first match wins, so two dispatches plan the identical
// edit. A harness that picked a different target on two dispatches would
// manufacture exactly the run-to-run disagreement -counts-diff exists to catch.
// ---------------------------------------------------------------------------

// files returns the model's files of a language, in Rel order.
func (m *JVMModel) files(lang string) []*JVMFile {
	var out []*JVMFile
	for _, f := range m.Files {
		if lang == "" || f.Lang == lang {
			out = append(out, f)
		}
	}
	return out
}

// simpleName returns the last segment of a dotted name.
func simpleName(dotted string) string {
	if i := strings.LastIndexByte(dotted, '.'); i >= 0 {
		return dotted[i+1:]
	}
	return dotted
}

// splice replaces src[start:end] with repl.
func splice(src []byte, start, end int, repl string) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(repl))
	out = append(out, src[:start]...)
	out = append(out, repl...)
	out = append(out, src[end:]...)
	return out
}

// insert places repl at offset at.
func insert(src []byte, at int, repl string) []byte { return splice(src, at, at, repl) }

// referencesSimpleName reports whether f mentions name as a whole word outside
// its own declaration — a cheap, deterministic "somebody uses this" test used
// only to pick a delete target that other files actually reference.
func referencesSimpleName(f *JVMFile, name string) bool {
	msk := mask(f.Src)
	for i := 0; i+len(name) <= len(msk); i++ {
		if wordAt(msk, i, name) {
			return true
		}
	}
	return false
}
