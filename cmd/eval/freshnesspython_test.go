package main

// SW-191 (EVALFRESH-001 closure, AC-3): the python family's freshness pin.
//
// PYTHON IS THE CASE THAT PROVES THE GATE, not just the filter. A `.py` file
// was already admitted by the registry-driven filter SW-191's first half
// shipped — and flask still could not complete its sequence, because the
// DIRECTORY gate then read
//
//	if packages[dir] == "" { continue }
//
// and Python has no package clause to put there. Every directory was dropped
// and the run aborted with "the index contains no modifiable Go source files to
// change" on a repository the filter had already accepted. The assertions below
// pin the clause-absent path end to end: admitted with no clause, planned,
// rendered as Python, and extracted by the shipped Python parser.
//
// Hermetic: no clone, no index, no wall clock.

import (
	"strings"
	"testing"
)

// pythonCorpusPaths are real repo-relative paths from the pinned flask clone
// (flask-3.0.0 @ 735a4701d6d5e848241e7d7535db898efb62d400).
var pythonCorpusPaths = []string{
	"src/flask/app.py",
	"src/flask/blueprints.py",
	"src/flask/json/provider.py",
	"tests/test_basic.py",
}

// TestFreshnessPython_FilterAndGateAdmitPythonWithoutAPackageClause is the
// EVALFRESH-001 gate pin.
func TestFreshnessPython_FilterAndGateAdmitPythonWithoutAPackageClause(t *testing.T) {
	packages := map[string]string{}
	for _, p := range pythonCorpusPaths {
		if !modifiableSourceFile(p) {
			t.Fatalf("modifiableSourceFile(%q) = false, want true", p)
		}
		if !admitSourceFile(p, []byte("import os\n\n\ndef existing():\n    return 1\n"), packages) {
			t.Fatalf("the directory gate refused %q. Python declares NO package clause, and the "+
				"gate must treat that as admissible rather than as a missing clause — the "+
				"opposite is EVALFRESH-001 on flask.", p)
		}
	}
	if len(packages) != 0 {
		t.Errorf("the gate recorded package clauses %v for a language that has none", packages)
	}
}

// TestFreshnessPython_TestModulesAreFirstClass keeps the deliberate asymmetry
// visible: Go's `_test.go` is excluded because its ingest shape differs, while
// Python test modules are ordinary modules and stay candidates.
func TestFreshnessPython_TestModulesAreFirstClass(t *testing.T) {
	if !modifiableSourceFile("tests/test_basic.py") {
		t.Error("tests/test_basic.py was refused: Python test modules are first-class modules, " +
			"not a build-mode carve-out like Go's _test.go")
	}
	if modifiableSourceFile("pkg/handler_test.go") {
		t.Error("pkg/handler_test.go was admitted: Go's _test.go build shape must stay excluded " +
			"so the Go change classes remain comparable")
	}
}

// TestFreshnessPython_SequenceMutatesAndConverges is the AC-3 pin: a
// flask-shaped tree through the production gate, the production plan and the
// shipped Python parser.
func TestFreshnessPython_SequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"src/flask/app.py":           "from __future__ import annotations\n\n\nclass Flask:\n    def run(self):\n        return 1\n",
		"src/flask/blueprints.py":    "from .app import Flask\n\n\nclass Blueprint:\n    def register(self):\n        return 2\n",
		"src/flask/json/provider.py": "import json\n\n\ndef dumps(obj):\n    return json.dumps(obj)\n",
		// Non-code files a real flask checkout carries: not targets.
		"pyproject.toml": "[project]\nname = \"flask\"\n",
		"CHANGES.rst":    "Changes\n",
		"package.json":   "{}\n",
	}
	steps := runFamilyFreshness(t, "python", tree, 12)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".py") {
			t.Errorf("step %d adds %s, which is not a Python file", s.index, s.path)
		}
		if s.pkg != "" {
			t.Errorf("step %d carries package clause %q; Python has none", s.index, s.pkg)
		}
	}
}

// TestFreshnessPython_AppendedDefClosesThePrecedingBlock pins the one shape
// question Python's append actually has: a column-0 `def` after a file that
// ended INSIDE an indented block must dedent out of it rather than land in it.
func TestFreshnessPython_AppendedDefClosesThePrecedingBlock(t *testing.T) {
	const inBlock = "class Flask:\n    def run(self):\n        return 1"
	step := changeStep{class: "modify", path: "src/flask/app.py", symbol: changeSymbol(1), index: 1}
	got := string(modifiedFileContent([]byte(inBlock), step))
	names := parsedSymbolNames(t, "src/flask/app.py", got)
	if !containsName(names, step.symbol) {
		t.Fatalf("the appended def was not extracted; parser saw %v:\n%s", names, got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "def "+step.symbol) && strings.HasPrefix(line, " ") {
			t.Errorf("the appended def is indented into the preceding block: %q", line)
		}
	}
}
