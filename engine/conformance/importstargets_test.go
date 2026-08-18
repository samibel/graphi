package conformance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"testing"
)

// mixedExtensionTree is the LINK-001 / ADR 0011 fixture: an importer, and an
// imported package DIRECTORY holding a source file, a test file, a Markdown file
// and a YAML file.
//
// Every one of the four matters, and the two non-Go ones are the load-bearing
// part: Markdown and YAML are in the DEFAULT, CGo-free parser registry
// (core/parse/defaults.go), so `.md` and `.yml` become real committed `file`
// nodes with real symbols — which is precisely how `imports` edges onto
// `README.md` and `.golangci.yml` were measured on cobra and grpc-go. A fixture
// whose imported directory holds only `.go` files cannot tell "targets the
// directory" from "targets the package", and that blind spot is why every
// pre-existing hermetic fixture in this repository was green while the defect
// shipped.
func mixedExtensionTree() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"app/main.go": `package app

import "example.com/m/tax"

func Main() int { return tax.Rate() }
`,
		"tax/tax.go": `package tax

func Rate() int { return 7 }
`,
		// A package member that is NOT importable: no import declaration anywhere
		// can reach TestRate, so an `imports` edge onto this file is wrong.
		"tax/tax_test.go": `package tax

import "testing"

func TestRate(t *testing.T) {
	if Rate() != 7 {
		t.Fatal("no")
	}
}
`,
		"tax/README.md":     "# tax\n\n## Overview\n\nRates.\n",
		"tax/.golangci.yml": "linters:\n  enable:\n    - govet\n",
	}
}

// importsTargetsFrom returns the sorted source paths of every `imports` edge
// leaving the file node at fromPath.
func importsTargetsFrom(g *graphView, fromPath string) ([]string, error) {
	var from string
	for _, n := range g.nodes {
		if n.Kind() == "file" && n.QualifiedName() == fromPath {
			from = string(n.ID())
		}
	}
	if from == "" {
		return nil, fmt.Errorf("no file node %q in the graph; the fixture did not index", fromPath)
	}
	var out []string
	for _, e := range g.edges {
		if string(e.From()) != from || e.Kind() != "imports" {
			continue
		}
		out = append(out, g.byID[e.To()].QualifiedName())
	}
	sort.Strings(out)
	return out, nil
}

// TestImportsEdge_TargetsOnlyPackageSourceFiles is AC-5 of the LINK-001 fix: the
// mixed-extension package directory above must yield EXACTLY ONE `imports` edge,
// onto `tax/tax.go`.
//
// AXES. It runs over parityBackends() × parityProfiles() — MemStore and SQLite,
// library-default and Balanced index profile — the same two axes the change-class
// table runs, and for the same reason: PARITY-003 lived exclusively in the
// Balanced branch of engine/ingest/linkfiles.go and survived a full 19-row
// two-store table because that table only ever drove ingest.New's zero value. A
// gate for a linker change that does not run the profile the CLI resolves is not
// a gate for the shipped configuration.
//
// WHY THIS IS NOT A DECLARED CHANGE CLASS, stated so the omission is a decision
// and not an oversight: docs/rc/parity-classes.yaml's rows are joined by
// internal/parity to the REAL-REPOSITORY matrix, so adding a row there changes
// that matrix's denominator and would invalidate the published 19/19 measurement
// mid-story. Re-publishing the matrix is SW-167's job, deliberately sequenced
// after this change. This is a hermetic assertion about the graph the linker
// produces, not a full-vs-incremental parity class, so it needs neither the
// declaration nor the denominator.
func TestImportsEdge_TargetsOnlyPackageSourceFiles(t *testing.T) {
	ctx := context.Background()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					t.Parallel()
					axis := b.name + "/" + pr.name
					root := t.TempDir()
					writeTree(t, root, mixedExtensionTree())

					store := newBackendStore(t, b)
					ing := newIngester(t, store, pr.p)
					if err := ing.IngestAll(ctx, root); err != nil {
						t.Fatalf("[%s] IngestAll: %v", axis, err)
					}
					g, err := newGraphView(ctx, store)
					if err != nil {
						t.Fatalf("[%s] read graph: %v", axis, err)
					}

					// NON-VACUITY FIRST, exactly as the change-class harness does
					// it. If the non-source files never became `file` nodes, the
					// assertion below would pass for a reason that has nothing to
					// do with the fix, and the row would prove nothing.
					for _, p := range []string{"tax/README.md", "tax/.golangci.yml", "tax/tax_test.go"} {
						found := false
						for _, n := range g.nodes {
							if n.Kind() == "file" && n.QualifiedName() == p {
								found = true
							}
						}
						if !found {
							t.Fatalf("[%s] VACUOUS FIXTURE: %q has no committed file node, so this "+
								"test cannot observe whether an imports edge would have targeted it", axis, p)
						}
					}

					got, err := importsTargetsFrom(g, "app/main.go")
					if err != nil {
						t.Fatalf("[%s] %v", axis, err)
					}
					want := []string{"tax/tax.go"}
					if len(got) != len(want) || (len(got) == 1 && got[0] != want[0]) {
						t.Errorf("[%s] app/main.go imports targets = %v, want %v.\n"+
							"An `imports` edge targets the imported package's SOURCE files (ADR 0011); "+
							"a README, a lint config and a _test.go file are not importable package members.",
							axis, got, want)
					}
				})
			}
		})
	}
}

// immuneLanguageTree is the AC-7 control fixture: one directory per language
// family the LINK-001 fix does NOT touch, each carrying the same mixed-extension
// shape that makes the Go/Python/C#/Rust fan-out visible.
//
// The two immune mechanisms, and why each is immune:
//
//   - Java and Kotlin emit ONE file→package edge to an interned `package` node
//     (engine/link/resolve_common.go, b.packageImports). There is no directory
//     fan-out to filter, so no filter was added and none can apply.
//   - TypeScript and C resolve EXACT target file paths (b.importFileTargets):
//     the resolver already names `./util.ts` / `util.h` and only a committed node
//     at that exact path emits an edge. A README was never a candidate.
//
// The fixture exists so that claim is measured rather than asserted: the digest
// this test logs is compared across the fix in the story's Verification section.
func immuneLanguageTree() map[string]string {
	return map[string]string{
		// Java: pkg-header language, one file→package edge.
		"java/com/shop/App.java": "package com.shop;\n\nimport com.tax.Rate;\n\npublic class App {\n  int run() { return new Rate().of(); }\n}\n",
		"java/com/tax/Rate.java": "package com.tax;\n\npublic class Rate {\n  public int of() { return 7; }\n}\n",
		"java/com/tax/README.md": "# tax\n\n## Notes\n",
		"java/com/tax/pom.xml":   "<project></project>\n",
		// Kotlin: same mechanism.
		"kt/app/Main.kt":  "package app\n\nimport tax.rate\n\nfun main(): Int = rate()\n",
		"kt/tax/Rate.kt":  "package tax\n\nfun rate(): Int = 7\n",
		"kt/tax/notes.md": "# rate\n\n## Notes\n",
		// TypeScript: exact relative target paths.
		"ts/main.ts":           "import { rate } from './tax/rate';\n\nexport function main(): number { return rate(); }\n",
		"ts/tax/rate.ts":       "export function rate(): number { return 7; }\n",
		"ts/tax/README.md":     "# rate\n\n## Notes\n",
		"ts/tax/tsconfig.json": "{\"compilerOptions\":{}}\n",
		// C: exact include paths.
		"c/main.c":            "#include \"util/helper.h\"\n\nint main(void) { return helper(); }\n",
		"c/util/helper.h":     "int helper(void);\n",
		"c/util/helper.c":     "#include \"helper.h\"\n\nint helper(void) { return 7; }\n",
		"c/util/README.md":    "# util\n\n## Notes\n",
		"c/util/Makefile.yml": "build: true\n",
	}
}

// TestImportsEdge_ImmuneLanguagesUnchanged is AC-7. It runs the immune-language
// fixture on both stores and both profile axes, asserts the two immune mechanisms
// behave as claimed, and LOGS the canonical snapshot digest.
//
// The digest is logged rather than pinned as a literal. A pinned literal would
// turn every unrelated parser or serialization change into a red gate here, which
// is a maintenance tax this row does not earn; what the story needs is a
// before/after comparison across ONE change, and `go test -v` at each side of the
// commit provides exactly that. The behavioural assertions below are the part that
// stays green forever.
func TestImportsEdge_ImmuneLanguagesUnchanged(t *testing.T) {
	ctx := context.Background()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					t.Parallel()
					axis := b.name + "/" + pr.name
					root := t.TempDir()
					writeTree(t, root, immuneLanguageTree())

					store := newBackendStore(t, b)
					ing := newIngester(t, store, pr.p)
					if err := ing.IngestAll(ctx, root); err != nil {
						t.Fatalf("[%s] IngestAll: %v", axis, err)
					}
					g, err := newGraphView(ctx, store)
					if err != nil {
						t.Fatalf("[%s] read graph: %v", axis, err)
					}

					// Non-vacuity: the non-source files really are committed file
					// nodes, so "no edge onto them" is a measurement and not an
					// accident of the fixture never having indexed them.
					for _, p := range []string{
						"java/com/tax/README.md", "kt/tax/notes.md",
						"ts/tax/README.md", "c/util/README.md", "c/util/Makefile.yml",
					} {
						found := false
						for _, n := range g.nodes {
							if n.Kind() == "file" && n.QualifiedName() == p {
								found = true
							}
						}
						if !found {
							t.Fatalf("[%s] VACUOUS FIXTURE: %q has no committed file node", axis, p)
						}
					}

					// Every imports edge in the immune tree targets a `file` or a
					// `package` node, and NONE targets one of the non-source files
					// above. Asserted over the whole edge set rather than per
					// importer, so an edge from a file the fixture forgot to name
					// still cannot slip through.
					for _, e := range g.edges {
						if e.Kind() != "imports" {
							continue
						}
						fromN, toN := g.byID[e.From()], g.byID[e.To()]
						if toN.Kind() != "file" && toN.Kind() != "package" {
							t.Errorf("[%s] imports edge %s -> %s targets kind %q",
								axis, fromN.QualifiedName(), toN.QualifiedName(), toN.Kind())
						}
						// NO immune-language imports edge may target a non-source file.
						switch toN.QualifiedName() {
						case "java/com/tax/README.md", "java/com/tax/pom.xml", "kt/tax/notes.md",
							"ts/tax/README.md", "ts/tax/tsconfig.json", "c/util/README.md",
							"c/util/Makefile.yml":
							t.Errorf("[%s] imports edge %s -> %s targets a non-source file",
								axis, fromN.QualifiedName(), toN.QualifiedName())
						}
					}

					sum := sha256.Sum256(snapshot(t, store))
					t.Logf("[%s] AC-7 immune-language snapshot sha256 = %s", axis, hex.EncodeToString(sum[:]))
				})
			}
		})
	}
}
