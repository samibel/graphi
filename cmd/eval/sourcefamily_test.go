package main

// SW-191 (EVALFRESH-001, AC-1): the family table's own tests.
//
// Two properties are pinned here, both hermetic — no clone, no index, no wall
// clock, only the shipped parser over bytes this package generated:
//
//  1. NON-VACUITY. Each family's package-clause reader narrows to ITS OWN
//     syntax. A reader that returned something for every input would make the
//     table decorative, which is the adversarial reading the story's test notes
//     ask for explicitly.
//  2. PARSEABILITY. Every family's generated declaration is really extracted as
//     a symbol by the shipped parser for that extension. Without this the
//     sequence would plan changes whose symbol the convergence probe can never
//     find — which is exactly the failure that left guava, okio,
//     kotlinx.serialization and flask at a third to a half of their steps even
//     after the file filter was widened.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/evalreport"
)

// parsedSymbolNames parses src as filename with the SHIPPED default registry and
// returns the bare names of every node it extracted.
func parsedSymbolNames(t *testing.T, filename, src string) []string {
	t.Helper()
	res, err := parse.NewDefaultRegistry().Parse(context.Background(), filename, []byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var out []string
	for _, n := range res.Nodes {
		out = append(out, bareName(n.QualifiedName()))
	}
	return out
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// familyProbe is a representative existing file of the family, used as the
// "before" state a MODIFY step appends to.
type familyProbe struct {
	family   string
	path     string
	existing string
	// pkg is the clause the family's reader must read out of existing.
	pkg string
}

var familyProbes = []familyProbe{
	{family: "go", path: "alpha/one.go", existing: "package alpha\n\nfunc One() int { return 1 }\n", pkg: "alpha"},
	{family: "java", path: "com/google/common/collect/ImmutableList.java",
		existing: "package com.google.common.collect;\n\npublic final class ImmutableList {\n    int size() { return 0; }\n}\n",
		pkg:      "com.google.common.collect"},
	{family: "kotlin", path: "okio/src/commonMain/kotlin/okio/Buffer.kt",
		existing: "package okio\n\nclass Buffer {\n    fun size(): Int = 0\n}\n", pkg: "okio"},
	{family: "python", path: "src/flask/app.py", existing: "import os\n\n\nclass Flask:\n    def run(self):\n        return 1\n", pkg: ""},
	{family: "typescript", path: "source/index.ts", existing: "export const ky = 1;\n\nexport function core(): number {\n\treturn 1;\n}\n", pkg: ""},
	{family: "tsx", path: "source/App.tsx", existing: "export function App(): number {\n\treturn 1;\n}\n", pkg: ""},
	{family: "javascript", path: "lib/express.js", existing: "var mixin = require('merge-descriptors');\n\nfunction createApplication() {\n  return 1;\n}\n\nmodule.exports = createApplication;\n", pkg: ""},
	{family: "ruby", path: "lib/sinatra/base.rb", existing: "module Sinatra\n  class Base\n    def call\n      1\n    end\n  end\nend\n", pkg: ""},
	{family: "rust", path: "src/lib.rs", existing: "pub fn existing() -> i32 { 1 }\n", pkg: ""},
	{family: "c", path: "src/main.c", existing: "#include <stdio.h>\n\nint existing(void) { return 1; }\n", pkg: ""},
	{family: "cpp", path: "src/main.cpp", existing: "int existing() { return 1; }\n", pkg: ""},
	{family: "csharp", path: "src/Program.cs", existing: "namespace Demo {\n    class Program {\n        int Existing() { return 1; }\n    }\n}\n", pkg: ""},
	{family: "php", path: "src/App.php", existing: "<?php\n\nfunction existing() { return 1; }\n", pkg: ""},
	{family: "lua", path: "src/init.lua", existing: "function existing() return 1 end\n", pkg: ""},
	{family: "bash", path: "scripts/run.sh", existing: "#!/usr/bin/env bash\n\nexisting() {\n  return 1\n}\n", pkg: ""},
}

// TestSourceFamilies_EveryProbeHasAFamily keeps the probe list and the table in
// step: a family added without a probe would ship untested templates.
func TestSourceFamilies_EveryProbeHasAFamily(t *testing.T) {
	probed := map[string]bool{}
	for _, p := range familyProbes {
		f := familyForPath(p.path)
		if f == nil {
			t.Fatalf("familyForPath(%q) = nil; the probe names family %s", p.path, p.family)
		}
		if f.name != p.family {
			t.Fatalf("familyForPath(%q).name = %q, want %q", p.path, f.name, p.family)
		}
		probed[f.name] = true
	}
	for _, name := range familyNames() {
		if !probed[name] {
			t.Errorf("family %q has no probe in familyProbes: its templates are unverified", name)
		}
	}
}

// TestSourceFamilies_ClauseReaderIsLanguageScoped is the non-vacuity test.
func TestSourceFamilies_ClauseReaderIsLanguageScoped(t *testing.T) {
	for _, p := range familyProbes {
		f := familyForPath(p.path)
		if got := f.packageClause([]byte(p.existing)); got != p.pkg {
			t.Errorf("%s: packageClause = %q, want %q", f.name, got, p.pkg)
		}
	}

	// Cross-reading: the Go reader must REFUSE a JVM clause rather than return
	// it with the terminator attached (the old goPackageClause defect), and the
	// JVM reader must strip Java's terminator while leaving Kotlin's clause
	// alone.
	java := []byte("package com.google.common.collect;\n\npublic final class ImmutableList {}\n")
	if got := readPackageClause(packageClauseGoIdent, java); got != "" {
		t.Errorf("the Go reader accepted a JVM clause as %q; a dotted, terminated name is not a Go package clause", got)
	}
	if got := readPackageClause(packageClauseJVMDotted, java); got != "com.google.common.collect" {
		t.Errorf("the JVM reader returned %q, want the clause with Java's terminator stripped", got)
	}
	if got := readPackageClause(packageClauseJVMDotted, []byte("package okio\n\nclass Buffer\n")); got != "okio" {
		t.Errorf("the JVM reader returned %q for Kotlin's unterminated clause, want %q", got, "okio")
	}

	// `absent` is absent for EVERY input, including one that contains the word.
	for _, src := range []string{
		"package okio\n",
		"import os\n",
		"# package com.example is a comment\n",
		"",
	} {
		if got := readPackageClause(packageClauseAbsent, []byte(src)); got != "" {
			t.Errorf("packageClauseAbsent returned %q for %q; a language with no package clause has no clause to read", got, src)
		}
	}
}

// TestSourceFamilies_GeneratedDeclarationsAreExtractable is the parseability
// test: the shipped parser must produce a symbol named GraphiEvalStepNNNN both
// for an APPEND into an existing file of that family and for a NEWLY ADDED
// file. A family whose template does not survive this cannot be measured.
func TestSourceFamilies_GeneratedDeclarationsAreExtractable(t *testing.T) {
	const step = 7
	symbol := changeSymbol(step)

	for _, p := range familyProbes {
		f := familyForPath(p.path)
		t.Run(f.name, func(t *testing.T) {
			// MODIFY / CROSS_PACKAGE: append into the existing file.
			modified := p.existing + f.appended(symbol, step)
			if !strings.HasPrefix(modified, p.existing) {
				t.Fatalf("the append rewrote the file instead of extending it")
			}
			if names := parsedSymbolNames(t, p.path, modified); !containsName(names, symbol) {
				t.Errorf("appending to %s did not yield %s; the parser extracted %v.\n\nappended text:\n%s",
					p.path, symbol, names, f.appended(symbol, step))
			}

			// ADD: a whole new file beside it.
			addedName := f.addedFileBase(symbol, step)
			added := f.added(p.pkg, symbol, step)
			if names := parsedSymbolNames(t, addedName, added); !containsName(names, symbol) {
				t.Errorf("the added file %s did not yield %s; the parser extracted %v.\n\nfile:\n%s",
					addedName, symbol, names, added)
			}
			if familyForPath(addedName) == nil || familyForPath(addedName).name != f.name {
				t.Errorf("the added file %s does not round-trip to family %s", addedName, f.name)
			}
		})
	}
}

// TestSourceFamilies_NonCodeExtensionsAreNotCandidates pins the deliberate
// NARROWING against the previous registry-driven filter. json, yaml, css,
// markdown and friends are parsed and indexed, but there is no "append one
// function" in them, so the sequence must not plan changes it cannot make.
func TestSourceFamilies_NonCodeExtensionsAreNotCandidates(t *testing.T) {
	for _, p := range []string{
		"package.json", "config/app.yaml", "config/app.yml", "Cargo.toml",
		// `.markdown` sits beside `.md` in the registry and was missing from this
		// list (SW-191 review MIN-8). Markdown is not a cosmetic entry here: the
		// intermediate registry-driven filter this narrowing replaced admitted
		// `.md`, which is what regressed the cobra Go control to 95/100 — the
		// harness appended a Go function to five markdown files and then could
		// never find the symbol.
		"README.md", "docs/guide.markdown",
		"docs/site.css", "schema/init.sql", "infra/main.tf", "infra/main.hcl",
	} {
		if f := familyForPath(p); f != nil {
			t.Errorf("familyForPath(%q) = %s; a data/markup file is not a mutation candidate", p, f.name)
		}
		if modifiableSourceFile(p) {
			t.Errorf("modifiableSourceFile(%q) = true; the sequence would plan a change it cannot render", p)
		}
	}
}

// TestSourceFamilies_ComplementIsCompleteAgainstTheShippedRegistry closes the
// hole the list above cannot close on its own: a hand-written list of data
// extensions omits whatever nobody thought of, and `.markdown` was exactly that
// omission (SW-191 review MIN-8). Rather than trusting the list, this walks
// EVERY extension the shipped registry registers and requires each one to be
// either a mutation family or an explicitly-declared data language. A parser
// added to the registry with a new extension therefore fails here until someone
// decides, in writing, which side of the line it is on.
func TestSourceFamilies_ComplementIsCompleteAgainstTheShippedRegistry(t *testing.T) {
	// The data/markup languages: parsed and indexed, but with no top-level
	// declaration a change class could append. Declared, not inferred.
	dataLanguages := map[string]bool{
		"json": true, "yaml": true, "toml": true, "css": true,
		"sql": true, "hcl": true, "markdown": true,
	}

	registry := parse.NewDefaultRegistry()
	sawFamily, sawData := 0, 0
	for _, lang := range registry.Languages() {
		parser, err := registry.ParserForLang(lang)
		if err != nil {
			t.Fatalf("registry lists language %q but has no parser for it: %v", lang, err)
		}
		for _, ext := range parser.Extensions() {
			probe := "probe" + ext
			family := familyForPath(probe)
			switch {
			case dataLanguages[lang]:
				sawData++
				if family != nil {
					t.Errorf("%s (%s) is a declared data language but claims mutation family %s; "+
						"the sequence would plan an append it cannot render", ext, lang, family.name)
				}
				if modifiableSourceFile(probe) {
					t.Errorf("modifiableSourceFile(%q) = true for data language %s", probe, lang)
				}
			default:
				sawFamily++
				if family == nil {
					t.Errorf("%s (%s) is a CODE language the registry parses, but cmd/eval/sourcefamily.go "+
						"states no mutation shape for it and it is not declared a data language; the "+
						"complement has drifted from the registry", ext, lang)
				}
			}
		}
	}
	// Non-vacuity in both directions: a registry that stopped reporting
	// languages, or a dataLanguages map that swallowed everything, must not
	// leave this test green.
	if sawFamily == 0 || sawData == 0 {
		t.Fatalf("walked %d code extensions and %d data extensions; the complement check is vacuous",
			sawFamily, sawData)
	}
}

// TestSourceFamilies_EveryCorpusPinLanguageHasAFamily reads the REAL
// corpus/manifest.json and requires every pinned repository's declared language
// to be a mutation family.
//
// SW-191 rebuild round 1. The table's divider comment used to group `ruby` under
// "languages with no pin" and state that nothing in that group "is exercised by
// a -full-run today". sinatra is a tier-3 ruby pin in the manifest, so the
// statement was false and nothing failed. A comment cannot be tested; the
// derivation it claims can be, and this is that derivation. It also fails when a
// NEW pin is added in a language the table has no family for — which would
// re-open EVALFRESH-001 for that pin without anybody noticing.
func TestSourceFamilies_EveryCorpusPinLanguageHasAFamily(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("read corpus/manifest.json: %v", err)
	}
	var manifest struct {
		Entries []struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Language string `json:"language"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse corpus/manifest.json: %v", err)
	}

	families := map[string]bool{}
	for _, f := range sourceFamilies {
		families[f.name] = true
	}
	pinned := map[string]bool{}
	for _, e := range manifest.Entries {
		// Tier-1 entries are local fixtures with no upstream URL; they are not
		// corpus pins and their "jvm" pseudo-language is not a family.
		if e.URL == "" || e.Language == "" {
			continue
		}
		if !families[e.Language] {
			t.Errorf("corpus pin %q declares language %q, and cmd/eval/sourcefamily.go states no "+
				"mutation shape for it; -full-run %s -incremental-changes N would abort with an "+
				"empty candidate set, which is EVALFRESH-001 re-opened for that pin",
				e.Name, e.Language, e.Name)
			continue
		}
		pinned[e.Language] = true
	}

	// Non-vacuity, and the specific fact the old comment got wrong: ruby is
	// pinned. If sinatra is ever removed from the corpus this fails and the
	// divider comment in sourcefamily.go has to be re-stated, not silently left
	// wrong again.
	if !pinned["ruby"] {
		t.Error("no ruby corpus pin found; sourcefamily.go's divider comment states ruby IS pinned " +
			"(sinatra) and that statement now has nothing behind it")
	}
	if len(pinned) < 2 {
		t.Fatalf("only %d pinned language(s) derived from the manifest; the check is vacuous", len(pinned))
	}
	// And the count the divider comment publishes: 7 of the 15 families carry a
	// corpus pin, 8 do not.
	if len(pinned) != 7 || len(families) != 15 {
		t.Errorf("sourcefamily.go's divider comment publishes 7 pinned of 15 families; the manifest "+
			"and the table now give %d pinned of %d (pinned: %v)", len(pinned), len(families), pinned)
	}
}

// ---------------------------------------------------------------------------
// The hermetic per-family freshness driver (AC-3).
//
// freshness{jvm,python,typescript}_test.go each build a corpus-shaped file
// inventory for their family and run it through THE SAME two production
// functions a -full-run uses — admitSourceFile (the directory gate) and
// buildChangeSequence (the plan) — then apply every planned step to an
// in-memory tree and re-parse with the SHIPPED parser. That is the hermetic
// analogue of "mutate a file of that language and re-measure": no clone, no
// index, no wall clock, but the same gate, the same plan, the same renderer and
// the same parser the measurement depends on.
// ---------------------------------------------------------------------------

// familyTree is an in-memory corpus-shaped checkout: repo-relative POSIX path
// to file bytes.
type familyTree map[string]string

// admitTree runs the production directory gate over the tree in canonical
// order and returns what a -full-run would have planned from.
func admitTree(tree familyTree) ([]string, map[string]string) {
	packages := map[string]string{}
	var files []string
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sortStrings(paths)
	for _, p := range paths {
		if admitSourceFile(p, []byte(tree[p]), packages) {
			files = append(files, p)
		}
	}
	sortStrings(files)
	return files, packages
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// runFamilyFreshness is the shared body of the three per-family pins. It
// asserts, for `count` changes over `tree`:
//
//   - the gate admitted files at all (the EVALFRESH-001 abort was "zero
//     candidates", so a non-empty list is the first thing to check);
//   - every planned step's target belongs to `wantFamily`;
//   - every ADD lands on a file with the family's own extension;
//   - after applying each MODIFY / ADD / CROSS_PACKAGE step the shipped parser
//     for that path extracts the step's symbol — i.e. the convergence probe
//     could actually find it;
//   - every DELETE removes exactly the file its cycle added.
func runFamilyFreshness(t *testing.T, wantFamily string, tree familyTree, count int) []changeStep {
	t.Helper()
	files, packages := admitTree(tree)
	if len(files) == 0 {
		t.Fatalf("the directory gate admitted NO file from a %s tree of %d files — this is "+
			"EVALFRESH-001's shape exactly ('the index contains no modifiable source files to change')",
			wantFamily, len(tree))
	}
	// A cross-package target is supplied so the third class is exercised too:
	// a family pin that silently skipped it would leave the class the harness
	// most often gets wrong untested.
	in := changeSequenceInput{
		files:    files,
		packages: packages,
		count:    count,
		crossPackage: evalreport.CrossPackageEvidence{
			Satisfied: true,
			Targets: []evalreport.CrossPackageTarget{
				{Path: files[len(files)-1], Symbol: "fixture", InboundFromOtherDirs: 2},
			},
		},
	}
	steps := buildChangeSequence(in)
	if len(steps) != count {
		t.Fatalf("planned %d steps, want %d", len(steps), count)
	}

	work := familyTree{}
	for k, v := range tree {
		work[k] = v
	}
	adds := map[string]bool{}
	for _, s := range steps {
		f := familyForPath(s.path)
		if f == nil {
			t.Fatalf("step %d targets %s, which belongs to no family", s.index, s.path)
		}
		if f.name != wantFamily {
			t.Fatalf("step %d targets %s (family %s), want the %s family", s.index, s.path, f.name, wantFamily)
		}
		switch s.class {
		case evalreport.ChangeClassDelete:
			if !adds[s.path] {
				t.Fatalf("step %d deletes %s, which this sequence never added — the pinned "+
					"checkout must stay intact", s.index, s.path)
			}
			delete(work, s.path)
			delete(adds, s.path)
			if _, still := work[s.path]; still {
				t.Fatalf("step %d: %s survives its own deletion", s.index, s.path)
			}
		case evalreport.ChangeClassAdd:
			step := s
			work[s.path] = string(addedFileContent(step))
			adds[s.path] = true
			if names := parsedSymbolNames(t, s.path, work[s.path]); !containsName(names, s.symbol) {
				t.Fatalf("step %d ADDED %s but the shipped parser extracted %v — the convergence "+
					"probe searches for %s and would never find it.\n\nfile:\n%s",
					s.index, s.path, names, s.symbol, work[s.path])
			}
		default: // modify, cross_package
			step := s
			work[s.path] = string(modifiedFileContent([]byte(work[s.path]), step))
			if names := parsedSymbolNames(t, s.path, work[s.path]); !containsName(names, s.symbol) {
				t.Fatalf("step %d (%s) appended to %s but the shipped parser extracted %v — the "+
					"convergence probe searches for %s and would never find it.\n\nfile:\n%s",
					s.index, s.class, s.path, names, s.symbol, work[s.path])
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range steps {
		seen[s.class] = true
	}
	for _, class := range evalreport.RequiredChangeClasses {
		if !seen[class] {
			t.Errorf("the %s sequence never exercises %s over %d changes: %v", wantFamily, class, count, seen)
		}
	}
	return steps
}
