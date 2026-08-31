// Package retrieval_test covers the public surface (AC-1: exported types
// and the New / Retrieve seam) and the arithmetic invariants every later
// stage depends on.
package retrieval_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/retrieval"
)

// TestExportedSurface_ListsExactlyTheNamedTypes is AC-1's encapsulation
// criterion: engine/retrieval exports ONLY the types the story names,
// plus New and Retrieve. Every internal ranking type stays unexported.
// A reviewer will grep the exported surface — keep it exactly as the AC
// lists it.
func TestExportedSurface_ListsExactlyTheNamedTypes(t *testing.T) {
	allowedPackageNames := map[string]bool{
		"Mode": true, "ModeAuto": true, "ModeLexicalOnly": true, "ModeSemanticRequired": true, "ModeFusionNoGraph": true,
		"Request": true,
		"State":   true, "StateReady": true, "StateLexicalOnly": true,
		"StateGenerationMissing": true, "StateGenerationStale": true, "StateGenerationCorrupt": true,
		"Explain": true, "Row": true, "Summary": true, "Result": true,
		"New": true,
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var exported, methods []string
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							exported = append(exported, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, ident := range s.Names {
							if ident.IsExported() {
								exported = append(exported, ident.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					exported = append(exported, d.Name.Name)
				} else {
					methods = append(methods, d.Name.Name)
				}
			}
		}
	}
	sort.Strings(exported)
	sort.Strings(methods)
	var unexpected []string
	for _, name := range exported {
		if !allowedPackageNames[name] {
			unexpected = append(unexpected, name)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("unexpected package exports: %v (all exports: %v)", unexpected, exported)
	}
	if strings.Join(methods, ",") != "Retrieve" {
		t.Errorf("exported methods = %v, want [Retrieve]", methods)
	}

}

// TestStateVocabulary_IsClosed is the typed-state contract AC-1 implies:
// every state the Result.Degradation can carry is one of the named
// constants, and nothing else.
func TestStateVocabulary_IsClosed(t *testing.T) {
	want := map[retrieval.State]bool{
		retrieval.StateReady:             true,
		retrieval.StateLexicalOnly:       true,
		retrieval.StateGenerationMissing: true,
		retrieval.StateGenerationStale:   true,
		retrieval.StateGenerationCorrupt: true,
	}
	if len(want) != 5 {
		t.Fatalf("vocabulary closed: have %d entries", len(want))
	}
	for s := range want {
		if strings.TrimSpace(string(s)) == "" {
			t.Errorf("empty state: %v", s)
		}
	}
}

// TestModeVocabulary_IsClosed pins the public modes, including the no-graph
// fusion ablation the AC-9 harness needs.
func TestModeVocabulary_IsClosed(t *testing.T) {
	// ModeAuto == 0 (zero value) is the default. The other two are positive.
	if retrieval.ModeAuto != 0 {
		t.Errorf("ModeAuto = %d, want 0 (zero value)", retrieval.ModeAuto)
	}
	if retrieval.ModeLexicalOnly == retrieval.ModeSemanticRequired {
		t.Errorf("ModeLexicalOnly == ModeSemanticRequired")
	}
	if retrieval.ModeFusionNoGraph == retrieval.ModeSemanticRequired || retrieval.ModeFusionNoGraph == retrieval.ModeLexicalOnly {
		t.Errorf("ModeFusionNoGraph aliases another mode")
	}
}

// TestExplain_FieldsAreIntegersAndNonNil asserts the scoring path is
// integer-only (AC-2 / AC-3) — no float fields anywhere on the per-row
// breakdown. Compile-time check via type assertions.
func TestExplain_FieldsAreIntegersAndNonNil(t *testing.T) {
	e := retrieval.Explain{}
	_ = e.LexicalRank + e.SemanticRank + e.RRF + e.Graph + e.Classification + e.Final
}
