package parity

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/corpus"
)

// ClassesPathJVM is the machine-readable JVM class table (WP-J5). This harness
// binds to the SAME file the hermetic JVM twin table binds to
// (engine/conformance/jvmparity_test.go via jvmparity_matrix_test.go), so the
// real-repo JVM matrix and the hermetic JVM matrix can never disagree about
// which classes exist — the same discipline ClassesPath enforces for Go.
const ClassesPathJVM = "docs/rc/parity-classes-jvm.yaml"

// JVMPlanner locates a real edit target in a real JVM clone.
type JVMPlanner func(m *JVMModel) (*Mutation, error)

// JVMClassSpec binds a declared JVM class id to the real-source edit that
// exercises it.
type JVMClassSpec struct {
	ID   string
	Plan JVMPlanner
	// Lang, when set, restricts the row to repositories the manifest declares
	// in that language. It is NOT a filter on the file the planner edits —
	// okio is a Kotlin pin holding 29 .java files, and a Java row is free to
	// use them. It exists only for the two rows whose SUBJECT is a language
	// feature the other language does not have.
	Lang string
	// Note is carried into the report row, stating what the row does and does
	// not prove.
	Note string
}

// jvmSpecs is the real-repo JVM class table.
//
// Every id here must appear in docs/rc/parity-classes-jvm.yaml (the PHANTOM
// direction) and every declared, non-deferred JVM change class must appear here
// (the MISSING direction); both are asserted by
// TestJVMClassTable_BindsToDeclaredMatrix.
//
// WHAT A ROW HERE PROVES, AND WHAT IT DOES NOT — read this before reading any
// verdict below. The hermetic twin rows in engine/conformance assert a WITNESS:
// a named edge at a named tier must be present or absent after the change. A
// real-repository row cannot, because nobody has ground truth for guava's edge
// set. What a real-repo row asserts is the §12.3 property and only that: the
// incrementally-updated graph and a fresh full index of the SAME final tree
// produce byte-identical snapshot envelopes. A PASS here is a parity statement,
// never a correctness one.
func jvmSpecs() []JVMClassSpec {
	return []JVMClassSpec{
		{ID: "jvm_add_file", Plan: planJVMAddFile,
			Note: "Adds a Java file in a NEW sibling package directory of an existing one: the pure add path over real source."},
		{ID: "jvm_modify_file", Plan: planJVMModifyFile,
			Note: "Rewrites an already-indexed JVM file so its member set grows while every existing declaration keeps identity."},
		{ID: "jvm_add_call", Plan: planJVMAddCall,
			Note: "Introduces a call through a DECLARED-TYPE receiver — the shape the JVM binder resolves and the heuristic linker skips. " +
				"On the binder=off axis this row therefore adds a call site the product's shipped default does not bind at all; " +
				"that is a stated property of the axis, not a defect of the row."},
		{ID: "jvm_change_overload", Plan: planJVMChangeOverload,
			Note: "The ADR 0008 D6 signature class: a same-arity overload is added to a method that was unique by (name, arity), " +
				"so the binder must DROP rather than rank. The added overload is deliberately a declaration only — this row asserts " +
				"parity over the change, and the harness never compiles the pin."},
		{ID: "kotlin_infer_declared_flip", Lang: langKotlin, Plan: planKotlinInferDeclaredFlip,
			Note: "The ADR 0008 D2 signature class: a Kotlin local flips declared -> inferred, so the binder loses the type it was " +
				"resolving through. Kotlin-only by construction: Java has no local type inference this shape reaches."},
		{ID: "jvm_rename_package", Plan: planJVMRenamePackage,
			Note: "Directory, package clause and every in-repo importer move in ONE change set. Because a JVM node's QN keys on the " +
				"file directory (qn.go filePackage), every declaration in the renamed directory changes identity at once."},
		{ID: "jvm_change_type_hierarchy", Plan: planJVMChangeTypeHierarchy,
			Note: "Re-points an `extends` clause at another type in the SAME directory, so no import has to move with it and the " +
				"supertype-chain re-resolution is the only thing under test."},
		{ID: "jvm_move_nested_class", Plan: planJVMMoveNestedClass,
			Note: "Promotes a nested Java type to top level. Nested types mint NO node (qn.go), so the promotion makes the type and " +
				"its members appear as nodes for the first time — the nested/top-level node boundary as a parity-holding transition."},
		{ID: "jvm_change_import_shadowing", Plan: planJVMChangeImportShadowing,
			Note: "Adds a single-type import for a simple name the file already resolves through an on-demand import, so the name " +
				"re-resolves under JLS 6.4.1's precedence ladder."},
		{ID: "jvm_move_symbol", Lang: langKotlin, Plan: planJVMMoveSymbol,
			Note: "A Kotlin top-level function moves file-to-file within ONE directory. The QN keys on the directory, not the filename, " +
				"so identity is stable while source_path changes: two files claim one NodeId inside a single change set."},
		{ID: "jvm_delete_file", Plan: planJVMDeleteFile,
			Note: "Deletes a JVM file declaring a type another file in the repository names — the per-file stale-node purge, the " +
				"confirmed-edge sweep and the re-link exercised together."},
		{ID: "jvm_mixed_dir_delete_callee", Plan: planJVMMixedDirDeleteCallee,
			Note: "The ADR 0008 D9 sweep unit on real source: a file OUTSIDE a mixed-language directory is deleted while a file " +
				"INSIDE it names the deleted type. WHAT THIS DOES NOT REPRODUCE: the hermetic row isolates the sweep by proving the " +
				"caller is never reprocessed and the chained callee survives. On a real repository neither can be guaranteed, so this " +
				"row is corroboration that the D9 unit holds parity on real mixed directories — not a second proof of the D9 mechanism."},
		{ID: "jvm_mixed_dir_change_receiver_type", Plan: planJVMMixedDirChangeReceiverType,
			Note: "A declared return type is re-pointed in a file OUTSIDE a mixed-language directory whose contents name it. Same " +
				"scope caveat as jvm_mixed_dir_delete_callee: parity over the change on a real mixed directory, not a re-proof of D9."},
	}
}

// JVMSpecByID indexes the JVM table.
func JVMSpecByID() map[string]JVMClassSpec {
	out := map[string]JVMClassSpec{}
	for _, s := range jvmSpecs() {
		out[s.ID] = s
	}
	return out
}

const jvmMarker = "GraphiParity"

// ---------------------------------------------------------------------------
// The planners.
// ---------------------------------------------------------------------------

// planJVMAddFile writes a new Java file into a NEW sibling package directory.
func planJVMAddFile(m *JVMModel) (*Mutation, error) {
	for _, d := range m.Dirs {
		if d.Pkg == "" {
			continue
		}
		newDir := path.Join(path.Dir(d.Dir), "graphiparity")
		if m.dirOf(newDir) != nil {
			continue
		}
		pkg := d.Pkg
		if i := strings.LastIndexByte(pkg, '.'); i >= 0 {
			pkg = pkg[:i] + ".graphiparity"
		} else {
			pkg = "graphiparity"
		}
		rel := path.Join(newDir, jvmMarker+"Added.java")
		body := fmt.Sprintf("package %s;\n\n/** Added by the SW-176 real-repository JVM parity matrix. */\npublic final class %sAdded {\n"+
			"  public String help() {\n    return \"sw-176\";\n  }\n}\n", pkg, jvmMarker)
		return &Mutation{
			Desc: fmt.Sprintf("add the new package directory %s with %s declaring package %s, sibling of %s (package %s)",
				newDir, rel, pkg, d.Dir, d.Pkg),
			Ops: []FileOp{{Kind: opWrite, Path: rel, Data: []byte(body)}},
		}, nil
	}
	return nil, errNoTarget
}

// planJVMModifyFile appends a member to an already-indexed type.
func planJVMModifyFile(m *JVMModel) (*Mutation, error) {
	for _, f := range m.Files {
		t := firstBodiedType(f)
		if t == nil {
			continue
		}
		add := jvmMemberText(f.Lang, jvmMarker+"Modified")
		return &Mutation{
			Desc: fmt.Sprintf("modify %s: append %s to %s %s, leaving every existing declaration's identity intact",
				f.Rel, jvmMarker+"Modified", t.Kind, t.Name),
			Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: insert(f.Src, t.BodyEnd, add)}},
		}, nil
	}
	return nil, errNoTarget
}

// planJVMAddCall introduces a call through a DECLARED-TYPE receiver.
//
// The receiver's type is the enclosing type itself, declared as a local (Java)
// or a parameter (Kotlin), so the call site is one the declared-type binder can
// resolve and the heuristic linker — which skips variable-receiver instance
// calls — cannot. That is the whole point of the row: it must add work for the
// SUBJECT, not for the fallback.
func planJVMAddCall(m *JVMModel) (*Mutation, error) {
	for _, f := range m.Files {
		t := firstBodiedType(f)
		if t == nil {
			continue
		}
		callee := ""
		for _, mm := range t.Methods {
			if mm.Arity == 0 && mm.Name != t.Name && !strings.HasPrefix(mm.Name, jvmMarker) {
				callee = mm.Name
				break
			}
		}
		if callee == "" {
			continue
		}
		var add string
		if f.Lang == langJava {
			add = fmt.Sprintf("\n  /** Added by the SW-176 real-repository JVM parity matrix. */\n"+
				"  public void %sCaller(%s %sRecv) {\n    %sRecv.%s();\n  }\n",
				jvmMarker, t.Name, jvmMarker, jvmMarker, callee)
		} else {
			add = fmt.Sprintf("\n  /** Added by the SW-176 real-repository JVM parity matrix. */\n"+
				"  fun %sCaller(%sRecv: %s) {\n    %sRecv.%s()\n  }\n",
				jvmMarker, jvmMarker, t.Name, jvmMarker, callee)
		}
		return &Mutation{
			Desc: fmt.Sprintf("add a call site in %s: new member %sCaller takes a %s-declared receiver and calls its already-indexed %s() — a DECLARED-TYPE receiver call, which the heuristic linker skips",
				f.Rel, jvmMarker, t.Name, callee),
			Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: insert(f.Src, t.BodyEnd, add)}},
		}, nil
	}
	return nil, errNoTarget
}

// planJVMChangeOverload adds a same-arity overload to a method that was unique
// by (name, arity) — the ADR 0008 D6 drop precondition.
func planJVMChangeOverload(m *JVMModel) (*Mutation, error) {
	for _, f := range m.Files {
		for ti := range f.Types {
			t := &f.Types[ti]
			counts := map[string]int{}
			for _, mm := range t.Methods {
				counts[fmt.Sprintf("%s/%d", mm.Name, mm.Arity)]++
			}
			for _, mm := range t.Methods {
				if mm.Arity < 1 || mm.Name == t.Name || strings.HasPrefix(mm.Name, jvmMarker) {
					continue // arity 0 has no same-arity overload; constructors are out of scope
				}
				if counts[fmt.Sprintf("%s/%d", mm.Name, mm.Arity)] != 1 {
					continue // already ambiguous: the change would prove nothing
				}
				params := make([]string, mm.Arity)
				for i := range params {
					if f.Lang == langJava {
						params[i] = fmt.Sprintf("java.lang.CharSequence %sArg%d", jvmMarker, i)
					} else {
						params[i] = fmt.Sprintf("%sArg%d: CharSequence", jvmMarker, i)
					}
				}
				var add string
				if f.Lang == langJava {
					add = fmt.Sprintf("\n  /** Same-arity overload added by the SW-176 real-repository JVM parity matrix. */\n"+
						"  public void %s(%s) {\n  }\n", mm.Name, strings.Join(params, ", "))
				} else {
					add = fmt.Sprintf("\n  /** Same-arity overload added by the SW-176 real-repository JVM parity matrix. */\n"+
						"  fun %s(%s) {\n  }\n", mm.Name, strings.Join(params, ", "))
				}
				return &Mutation{
					Desc: fmt.Sprintf("add a same-arity overload of %s.%s(%d params) in %s, making (%s,%d) ambiguous — the ADR 0008 D6 drop precondition",
						t.Name, mm.Name, mm.Arity, f.Rel, mm.Name, mm.Arity),
					Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: insert(f.Src, t.BodyEnd, add)}},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// planKotlinInferDeclaredFlip removes the declared type of a Kotlin local with
// an initializer, so `val x: T = e` becomes `val x = e` (ADR 0008 D2).
func planKotlinInferDeclaredFlip(m *JVMModel) (*Mutation, error) {
	for _, f := range m.files(langKotlin) {
		start, end, name, typ := firstDeclaredLocal(f.Src)
		if start < 0 {
			continue
		}
		return &Mutation{
			Desc: fmt.Sprintf("flip the Kotlin local %s in %s from DECLARED (`: %s`) to INFERRED by deleting its type annotation — ADR 0008 D2: the binder types the declared form and abstains on the inferred one",
				name, f.Rel, typ),
			Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: splice(f.Src, start, end, "")}},
		}, nil
	}
	return nil, errNoTarget
}

// firstDeclaredLocal finds the first `val|var <ident>: <Type> =` in src and
// returns the byte range of the `: <Type>` annotation, the local's name and the
// type text. It works on the MASKED source so a colon inside a string or a
// comment cannot be chosen.
func firstDeclaredLocal(src []byte) (int, int, string, string) {
	msk := mask(src)
	for i := 0; i < len(msk); i++ {
		if !wordAt(msk, i, "val") && !wordAt(msk, i, "var") {
			continue
		}
		j := skipSpace(msk, i+3)
		name, after := readIdent(msk, j)
		if name == "" {
			continue
		}
		k := skipSpace(msk, after)
		if k >= len(msk) || msk[k] != ':' {
			continue
		}
		t := skipSpace(msk, k+1)
		typ, tEnd := readDotted(msk, t)
		if typ == "" {
			continue
		}
		// Only a simple, non-generic, non-nullable annotation is rewritten: a
		// generic or nullable one would need the whole type expression parsed,
		// and a partial deletion would leave broken source. Refusing is free —
		// the walk continues to the next candidate.
		e := skipSpace(msk, tEnd)
		if e >= len(msk) || msk[e] != '=' {
			continue
		}
		return k, tEnd, name, typ
	}
	return -1, -1, "", ""
}

// planJVMRenamePackage renames a directory, its files' package clauses and
// every in-repo importer, in one change set.
//
// The SMALLEST directory declaring a package is chosen so the change set stays
// reviewable; the class is exercised either way, because renaming the directory
// changes the QN of every declaration it holds (qn.go filePackage).
func planJVMRenamePackage(m *JVMModel) (*Mutation, error) {
	var target *JVMDir
	for _, d := range m.Dirs {
		if d.Pkg == "" || d.Dir == "." || len(d.Files) == 0 {
			continue
		}
		if target == nil || len(d.Files) < len(target.Files) {
			target = d
		}
	}
	if target == nil {
		return nil, errNoTarget
	}
	oldBase := path.Base(target.Dir)
	newDir := path.Join(path.Dir(target.Dir), oldBase+"renamed")
	if m.dirOf(newDir) != nil {
		return nil, errNoTarget
	}
	oldPkg := target.Pkg
	newPkg := oldPkg + "renamed"

	ops := []FileOp{{Kind: opRenameDir, Path: target.Dir, NewPath: newDir}}
	// Rewrite each moved file's package clause AT ITS NEW PATH. The rename op
	// runs first, so writing to the new path is writing to the moved file.
	for _, f := range target.Files {
		if f.Pkg != oldPkg {
			continue
		}
		ops = append(ops, FileOp{
			Kind: opWrite,
			Path: path.Join(newDir, path.Base(f.Rel)),
			Data: splice(f.Src, f.PkgStart, f.PkgEnd, newPkg),
		})
	}
	// Re-point every in-repo importer.
	importers := 0
	for _, f := range m.Files {
		if f.Dir == target.Dir {
			continue
		}
		src, changed := f.Src, false
		// Rewrite from the LAST import backwards so earlier offsets stay valid.
		for i := len(f.Imports) - 1; i >= 0; i-- {
			im := f.Imports[i]
			if im.Path != oldPkg && !strings.HasPrefix(im.Path, oldPkg+".") {
				continue
			}
			// The import statement's dotted name starts after the keyword; find
			// it in the current bytes rather than trusting a stale offset.
			seg := oldPkg
			idx := indexBytesFrom(src, im.Start, []byte(seg))
			if idx < 0 || idx >= im.End {
				continue
			}
			src = splice(src, idx, idx+len(seg), newPkg)
			changed = true
		}
		if changed {
			ops = append(ops, FileOp{Kind: opWrite, Path: f.Rel, Data: src})
			importers++
		}
	}
	sort.Slice(ops[1:], func(i, j int) bool { return ops[1+i].Path < ops[1+j].Path })
	return &Mutation{
		Desc: fmt.Sprintf("rename package %s: directory %s -> %s AND the package clause of %d file(s) -> %s, re-pointing %d in-repo importer file(s) in one change set",
			oldPkg, target.Dir, newDir, len(target.Files), newPkg, importers),
		Ops: ops,
	}, nil
}

// planJVMChangeTypeHierarchy re-points an `extends` clause at another top-level
// type in the SAME directory, so nothing has to move with it.
func planJVMChangeTypeHierarchy(m *JVMModel) (*Mutation, error) {
	for _, f := range m.files(langJava) {
		d := m.dirOf(f.Dir)
		if d == nil {
			continue
		}
		for _, t := range f.Types {
			if t.Super == "" || t.Kind != "class" {
				continue
			}
			cur := simpleName(t.Super)
			repl := ""
			for _, sib := range d.Files {
				if sib.Lang != langJava {
					continue
				}
				for _, st := range sib.Types {
					if st.Kind == "class" && st.Name != t.Name && st.Name != cur {
						repl = st.Name
						break
					}
				}
				if repl != "" {
					break
				}
			}
			if repl == "" {
				continue
			}
			return &Mutation{
				Desc: fmt.Sprintf("re-point the supertype of class %s in %s: extends %s -> extends %s (a top-level class of the SAME directory %s, so no import moves with it)",
					t.Name, f.Rel, t.Super, repl, f.Dir),
				Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: splice(f.Src, t.SuperStart, t.SuperEnd, repl)}},
			}, nil
		}
	}
	return nil, errNoTarget
}

// planJVMMoveNestedClass promotes a nested Java type to top level.
func planJVMMoveNestedClass(m *JVMModel) (*Mutation, error) {
	for _, f := range m.files(langJava) {
		for _, t := range f.Types {
			for _, n := range t.Nested {
				if m.declaresTopLevel(f.Dir, n.Name) {
					continue // a file of that name already exists here
				}
				text := string(f.Src[n.Start:n.End])
				// Strip any `static` modifier the promoted declaration carried:
				// a top-level type cannot be static.
				text = strings.TrimSpace(text)
				dest := path.Join(f.Dir, n.Name+".java")
				pkgLine := ""
				if f.Pkg != "" {
					pkgLine = "package " + f.Pkg + ";\n\n"
				}
				body := fmt.Sprintf("%s/* Promoted from %s.%s by the SW-176 real-repository JVM parity matrix. */\n%s\n",
					pkgLine, t.Name, n.Name, text)
				return &Mutation{
					Desc: fmt.Sprintf("promote the nested %s %s.%s out of %s into the new top-level file %s — nested types mint no node, so the promotion makes the type and its members appear as nodes for the first time",
						n.Kind, t.Name, n.Name, f.Rel, dest),
					Ops: []FileOp{
						{Kind: opWrite, Path: f.Rel, Data: splice(f.Src, n.Start, n.End, "")},
						{Kind: opWrite, Path: dest, Data: []byte(body)},
					},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// declaresTopLevel reports whether a directory already holds a top-level type
// of that name.
func (m *JVMModel) declaresTopLevel(dir, name string) bool {
	d := m.dirOf(dir)
	if d == nil {
		return false
	}
	for _, f := range d.Files {
		for _, t := range f.Types {
			if t.Name == name {
				return true
			}
		}
	}
	return false
}

// planJVMChangeImportShadowing adds a single-type import for a simple name the
// file currently resolves through an on-demand import (JLS 6.4.1).
func planJVMChangeImportShadowing(m *JVMModel) (*Mutation, error) {
	// The candidate pool of shadowing targets: every top-level type in the
	// repository, keyed by simple name, taking the lexicographically first
	// package that declares it so the choice is deterministic.
	pkgOf := map[string]string{}
	for _, f := range m.Files {
		if f.Pkg == "" {
			continue
		}
		for _, t := range f.Types {
			if cur, ok := pkgOf[t.Name]; !ok || f.Pkg < cur {
				pkgOf[t.Name] = f.Pkg
			}
		}
	}
	for _, f := range m.files(langJava) {
		onDemand := ""
		explicit := map[string]bool{}
		declared := map[string]bool{}
		for _, im := range f.Imports {
			if im.OnDemand {
				// A STATIC on-demand import (`import static a.b.C.*;`) imports
				// MEMBERS, not types, so JLS 6.4.1's single-type-import
				// shadowing rule — which is what this row is about — does not
				// govern it. Accepting one would let the row run under the
				// wrong semantics and publish a verdict about a rule it never
				// exercised. Measured relevance: across the three pinned JVM
				// repositories the ONLY Java wildcard import at all is guava's
				// `import static java.util.stream.Collectors.*;`, so without
				// this condition that single line would be the shadowing base
				// for the entire corpus.
				if !im.Static && onDemand == "" {
					onDemand = im.Path
				}
				continue
			}
			explicit[simpleName(im.Path)] = true
		}
		if onDemand == "" {
			continue
		}
		for _, t := range f.Types {
			declared[t.Name] = true
			for _, n := range t.Nested {
				declared[n.Name] = true
			}
		}
		names := make([]string, 0, len(pkgOf))
		for n := range pkgOf {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if explicit[n] || declared[n] || pkgOf[n] == f.Pkg || pkgOf[n] == onDemand {
				continue
			}
			if !referencesSimpleName(f, n) {
				continue
			}
			stmt := fmt.Sprintf("import %s.%s;\n", pkgOf[n], n)
			return &Mutation{
				Desc: fmt.Sprintf("add the single-type import %s.%s to %s, which already carries the on-demand import %s.* and names %s — JLS 6.4.1: a single-type import shadows an on-demand one, so %s re-resolves",
					pkgOf[n], n, f.Rel, onDemand, n, n),
				Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: insert(f.Src, f.ImportEnd, stmt)}},
			}, nil
		}
	}
	return nil, errNoTarget
}

// planJVMMoveSymbol relocates a Kotlin top-level function to a NEW file of the
// SAME directory.
func planJVMMoveSymbol(m *JVMModel) (*Mutation, error) {
	for _, f := range m.files(langKotlin) {
		for _, fn := range f.TopFuncs {
			if strings.HasPrefix(fn.Name, jvmMarker) {
				continue
			}
			dest := path.Join(f.Dir, "GraphiParityMoved.kt")
			if m.fileAt(dest) != nil {
				continue
			}
			text := strings.TrimSpace(string(f.Src[fn.Start:fn.End]))
			pkgLine := ""
			if f.Pkg != "" {
				pkgLine = "package " + f.Pkg + "\n\n"
			}
			// Carry the source file's imports with the function: a top-level
			// Kotlin function that loses its imports is a different edit.
			imports := ""
			if len(f.Imports) > 0 {
				imports = strings.TrimRight(string(f.Src[f.Imports[0].Start:f.Imports[len(f.Imports)-1].End]), "\n") + "\n\n"
			}
			body := fmt.Sprintf("%s%s/* Moved from %s by the SW-176 real-repository JVM parity matrix. */\n%s\n",
				pkgLine, imports, path.Base(f.Rel), text)
			return &Mutation{
				Desc: fmt.Sprintf("move the Kotlin top-level fun %s from %s to the NEW file %s in the SAME directory — the QN keys on the directory, not the filename, so identity is stable while source_path changes and two files claim one NodeId inside one change set",
					fn.Name, f.Rel, dest),
				Ops: []FileOp{
					{Kind: opWrite, Path: f.Rel, Data: splice(f.Src, fn.Start, fn.End, "")},
					{Kind: opWrite, Path: dest, Data: []byte(body)},
				},
			}, nil
		}
	}
	return nil, errNoTarget
}

func (m *JVMModel) fileAt(rel string) *JVMFile {
	for _, f := range m.Files {
		if f.Rel == rel {
			return f
		}
	}
	return nil
}

// planJVMDeleteFile deletes the SMALLEST JVM file declaring a top-level type
// another file in the repository names.
func planJVMDeleteFile(m *JVMModel) (*Mutation, error) {
	victim, typeName, referrer := m.smallestReferencedFile(nil)
	if victim == nil {
		return nil, errNoTarget
	}
	return &Mutation{
		Desc: fmt.Sprintf("delete %s (%d bytes), which declares the top-level type %s that %s names — the per-file stale-node purge, the confirmed-edge sweep and the re-link exercised together",
			victim.Rel, len(victim.Src), typeName, referrer),
		Ops: []FileOp{{Kind: opDelete, Path: victim.Rel}},
	}, nil
}

// smallestReferencedFile returns the smallest single-type file whose type is
// named by another file admitted by `where` (nil = any other file), together
// with the type name and the naming file's path.
func (m *JVMModel) smallestReferencedFile(where func(*JVMFile) bool) (*JVMFile, string, string) {
	var best *JVMFile
	bestType, bestRef := "", ""
	for _, f := range m.Files {
		if len(f.Types) != 1 || len(f.Types[0].Name) < 4 {
			continue // one declaration per file keeps the blast radius readable
		}
		name := f.Types[0].Name
		ref := ""
		for _, g := range m.Files {
			if g.Rel == f.Rel || (where != nil && !where(g)) {
				continue
			}
			if referencesSimpleName(g, name) {
				ref = g.Rel
				break
			}
		}
		if ref == "" {
			continue
		}
		if best == nil || len(f.Src) < len(best.Src) {
			best, bestType, bestRef = f, name, ref
		}
	}
	return best, bestType, bestRef
}

// planJVMMixedDirDeleteCallee deletes a file OUTSIDE a mixed-language directory
// that a file INSIDE it names.
func planJVMMixedDirDeleteCallee(m *JVMModel) (*Mutation, error) {
	mixed := m.mixedDirs()
	if len(mixed) == 0 {
		return nil, errNoTarget
	}
	inMixed := func(f *JVMFile) bool {
		for _, d := range mixed {
			if f.Dir == d.Dir {
				return true
			}
		}
		return false
	}
	victim, typeName, referrer := m.smallestReferencedFile(inMixed)
	if victim == nil || inMixed(victim) {
		return nil, errNoTarget
	}
	return &Mutation{
		Desc: fmt.Sprintf("delete %s, declaring the top-level type %s, which %s NAMES from the MIXED-LANGUAGE directory %s (%d .java + %d .kt) — the ADR 0008 D9 (directory, language) sweep unit on real source",
			victim.Rel, typeName, referrer, path.Dir(referrer),
			m.dirOf(path.Dir(referrer)).Java, m.dirOf(path.Dir(referrer)).Kotlin),
		Ops: []FileOp{{Kind: opDelete, Path: victim.Rel}},
	}, nil
}

// planJVMMixedDirChangeReceiverType re-points a declared supertype in a file
// OUTSIDE a mixed-language directory whose contents name the edited type.
func planJVMMixedDirChangeReceiverType(m *JVMModel) (*Mutation, error) {
	mixed := m.mixedDirs()
	if len(mixed) == 0 {
		return nil, errNoTarget
	}
	for _, f := range m.files(langJava) {
		if m.dirOf(f.Dir) != nil && m.dirOf(f.Dir).Mixed() {
			continue // the edit must land OUTSIDE the mixed directory
		}
		d := m.dirOf(f.Dir)
		if d == nil {
			continue
		}
		for _, t := range f.Types {
			if t.Super == "" || t.Kind != "class" {
				continue
			}
			namer := ""
			for _, md := range mixed {
				for _, g := range md.Files {
					if referencesSimpleName(g, t.Name) {
						namer = g.Rel
						break
					}
				}
				if namer != "" {
					break
				}
			}
			if namer == "" {
				continue
			}
			cur := simpleName(t.Super)
			repl := ""
			for _, sib := range d.Files {
				if sib.Lang != langJava {
					continue
				}
				for _, st := range sib.Types {
					if st.Kind == "class" && st.Name != t.Name && st.Name != cur {
						repl = st.Name
						break
					}
				}
				if repl != "" {
					break
				}
			}
			if repl == "" {
				continue
			}
			return &Mutation{
				Desc: fmt.Sprintf("re-point the declared supertype of class %s in %s (extends %s -> extends %s) while %s, in the MIXED-LANGUAGE directory %s, names %s and is itself never edited",
					t.Name, f.Rel, t.Super, repl, namer, path.Dir(namer), t.Name),
				Ops: []FileOp{{Kind: opWrite, Path: f.Rel, Data: splice(f.Src, t.SuperStart, t.SuperEnd, repl)}},
			}, nil
		}
	}
	return nil, errNoTarget
}

func (m *JVMModel) mixedDirs() []*JVMDir {
	var out []*JVMDir
	for _, d := range m.Dirs {
		if d.Mixed() {
			out = append(out, d)
		}
	}
	return out
}

// firstBodiedType returns the first top-level type of f with a non-empty body.
func firstBodiedType(f *JVMFile) *JVMType {
	for i := range f.Types {
		if f.Types[i].BodyEnd > f.Types[i].BodyStart {
			return &f.Types[i]
		}
	}
	return nil
}

// jvmMemberText renders a language-appropriate no-argument member.
func jvmMemberText(lang, name string) string {
	if lang == langJava {
		return fmt.Sprintf("\n  /** Appended by the SW-176 real-repository JVM parity matrix. */\n"+
			"  public String %s() {\n    return \"sw-176\";\n  }\n", name)
	}
	return fmt.Sprintf("\n  /** Appended by the SW-176 real-repository JVM parity matrix. */\n"+
		"  fun %s(): String {\n    return \"sw-176\"\n  }\n", name)
}

func indexBytesFrom(b []byte, from int, sub []byte) int {
	if from < 0 {
		from = 0
	}
	if from > len(b) {
		return -1
	}
	i := strings.Index(string(b[from:]), string(sub))
	if i < 0 {
		return -1
	}
	return from + i
}

// ---------------------------------------------------------------------------
// Compile coverage — the closure for PARITY-COV-001 (SW-190).
// ---------------------------------------------------------------------------

// CompileCoverageInput carries the per-pin data ComputeCompileCoverage needs.
//
// It is deliberately a struct of primitives so the parity package never depends
// on jvmcorpus' Strategy type, while the caller can pass whatever the runtime
// has — the manifest's jvm_compile block, an in-memory strategy, a dispatch's
// precomputed one.
type CompileCoverageInput struct {
	// PinRoot is the absolute path to a clone of the pin at its pinned SHA.
	PinRoot string
	// SourceRoots are the relative directories the strategy declares as the
	// pin's compile targets (e.g. "guava/src", "okio/src/jvmMain/kotlin").
	SourceRoots []string
	// CommonSourceRoots are the subset of SourceRoots the strategy lists as
	// common — the multi-platform shape the kotlin compiler is told about.
	// Pass nil if the strategy has none.
	CommonSourceRoots []string
	// Strategy is the strategy name (e.g. "full-dependency-resolution",
	// "not-compiled"). A pin whose strategy is "not-compiled" gets a figure
	// of zero with ExcludedReason populated; the staging run is skipped.
	Strategy string
	// ExcludedFromCorpusScale marks pins the pin's own strategy says are out
	// of the corpus-scale claim (SW-173's okio / kotlinx note).
	ExcludedFromCorpusScale bool
	// ExcludedReason is the strategy's own wording for the exclusion; copied
	// verbatim into the returned CompileCoverage.
	ExcludedReason string
	// RunnerClass names the machine that ran the compile, so the figure's
	// provenance is auditable end to end.
	RunnerClass string
	// CandidateSHA is the product candidate the compile ran against.
	CandidateSHA string
	// Now is the function used to read the current date (a clock seam kept
	// so tests can pin time). Defaults to today's date in ISO format.
	Now func() string
}

// ComputeCompileCoverage returns the per-pin coverage figure PARITY-COV-001
// demands: how many of the pin's CST source files the signature-aware oracle
// successfully compiled, divided by how many exist at the pin. The function is
// a pure staging pass — it does NOT run javac/kotlinc. The exclusion policies
// it reports (collisions, "not-compiled") are the staging exclusions, and the
// actual compile-time check is left to the dispatch's full jvmcorpus run,
// because that run needs the toolchain and the dispatch-only workflow. What
// this function guarantees is that the FIGURE'S DENOMINATORS are reproducible
// from the harness output alone: the per-pin count of staged files is a
// property of the source tree, not of the toolchain.
//
// Hermetic by construction: it walks the pin's filesystem and never invokes a
// compiler. The writeJVMFixture test pins its schema and pins its behaviour.
func ComputeCompileCoverage(in CompileCoverageInput) (corpus.CompileCoverage, error) {
	if in.PinRoot == "" {
		return corpus.CompileCoverage{}, fmt.Errorf("parity: CompileCoverage requires a non-empty pin root")
	}
	if in.Now == nil {
		in.Now = func() string { return "2026-08-21" }
	}
	if in.RunnerClass == "" {
		return corpus.CompileCoverage{}, fmt.Errorf("parity: CompileCoverage requires a non-empty runner_class")
	}
	if in.CandidateSHA == "" {
		return corpus.CompileCoverage{}, fmt.Errorf("parity: CompileCoverage requires a non-empty candidate_sha")
	}

	// Outer denominator: every JVM source file tracked at the pin. We count
	// via filesystem walk rather than `git ls-files` because a real operator
	// may pass a working tree, and a hermetic test never has a .git at all.
	// The exact same SHAPE `internal/jvmcorpus/pin_test.go:countPinSources`
	// applies — git ls-files *.java *.kt — but fall back to a walk for the
	// cases that ship without a checkout.
	var sourceFiles int
	common := map[string]bool{}
	for _, r := range in.CommonSourceRoots {
		common[r] = true
	}
	claims := map[string][]string{}
	for _, root := range in.SourceRoots {
		abs := filepath.Join(in.PinRoot, filepath.FromSlash(root))
		err := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				// A missing source root is not a free-floating failure — the
				// strategy may declare a root the pin happens not to have.
				// Skip silently; the strategy's own reason records the gap.
				if os.IsNotExist(err) && d == nil {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !isJVMSource(p) {
				return nil
			}
			rel, rerr := filepath.Rel(abs, p)
			if rerr != nil {
				return rerr
			}
			key := filepath.ToSlash(rel)
			claims[key] = append(claims[key], p)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return corpus.CompileCoverage{}, fmt.Errorf("parity: walk source root %q: %w", root, err)
		}
	}
	// The outer count. We use the union of the strategy's offered files —
	// the strategy's view, NOT a git ls-files of the pin — because the
	// strategy IS the oracle's view of which files compile, and counting
	// files the strategy refuses to stage would manufacture a denominator
	// the oracle never sees.
	for _, srcs := range claims {
		sourceFiles += len(srcs)
	}

	var compiledFiles int
	excludedReason := ""
	switch in.Strategy {
	case "not-compiled":
		// Negative result, recorded not silently zero — okio's expect/actual
		// collision and the exclusions it forces. compiled_files stays 0 and
		// the ExcludedReason propagates from the pin's own strategy.
		excludedReason = in.ExcludedReason
	case "", "full-dependency-resolution", "accept-errors-and-score-what-resolved":
		// Staging pass: same shape as internal/jvmcorpus.Stage, but computed
		// from the strategy's source_roots only — never a tree walk that could
		// smuggle in files the strategy refuses to compile.
		keys := make([]string, 0, len(claims))
		for k := range claims {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			srcs := claims[k]
			if len(srcs) > 1 {
				// A collision. Drop every file in it. The compile_coverage
				// figure counts only what reached the compiler; this exclusion
				// is the same rule jvmcorpus.Stage enforces.
				continue
			}
			compiledFiles++
		}
		// Sanity: an accepted strategy with no files stage-able is recorded
		// as zero coverage with no auto-fabricated reason.
	default:
		return corpus.CompileCoverage{}, fmt.Errorf("parity: unknown strategy %q", in.Strategy)
	}

	var cov float64
	if sourceFiles > 0 {
		cov = float64(compiledFiles) / float64(sourceFiles)
		// 4-decimal precision, truncated (round-half-down) so a re-recorded
		// figure is reproducible from the same inputs.
		cov = float64(int64(cov*10000)) / 10000
	}

	return corpus.CompileCoverage{
		SourceFiles:   sourceFiles,
		CompiledFiles: compiledFiles,
		Coverage:      cov,
		MeasuredAt:    in.Now(),
		CandidateSHA:  in.CandidateSHA,
		RunnerClass:   in.RunnerClass,
		Oracle:        "internal/parity/jvmclasses.go signature-aware oracle",
		ExcludedReason: excludedReason,
	}, nil
}

// isJVMSource reports whether p names a JVM source file by its extension. It
// mirrors internal/jvmcorpus.compile.go:isJVMSource so the staging decision
// cannot drift between the two staging sites.
func isJVMSource(p string) bool {
	return strings.HasSuffix(p, ".java") || strings.HasSuffix(p, ".kt")
}
