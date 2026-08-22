package main

// SW-191 (EVALFRESH-001 closure, AC-3): the TypeScript family's freshness pin.
//
// THE FAMILY IS THREE GRAMMARS AND ONE RESOLVER, so it gets three family
// entries and three pins here: `.ts` (ky), `.tsx` (ky) and `.js` (express).
// Like Python the family has no package clause, so it hit the same directory
// gate; unlike Python it also has a MODULE-SYSTEM question that the generated
// declaration has to survive — express is CommonJS, so the appended
// declaration must not be an ESM `export`, which would be valid in only one of
// the two module systems the family's pins use.
//
// Hermetic: no clone, no index, no wall clock.

import (
	"strings"
	"testing"
)

// tsCorpusPaths are real repo-relative paths from the pinned TS-family clones:
// ky-v1.2.0 @ 38ac18bc1ac3268130de766891ce9b718eb8145a and express-4.18.2 @
// 8368dc178af16b91b576c4c1d135f701a0007e5d.
var tsCorpusPaths = []string{
	"source/index.ts",
	"source/core/Ky.ts",
	"source/utils/timeout.ts",
	"lib/express.js",
	"lib/router/index.js",
}

// TestFreshnessTypeScript_FilterAndGateAdmitTheFamilyWithoutAPackageClause is
// the EVALFRESH-001 gate pin for ky and express — the two pins that exited 1.
func TestFreshnessTypeScript_FilterAndGateAdmitTheFamilyWithoutAPackageClause(t *testing.T) {
	packages := map[string]string{}
	for _, p := range tsCorpusPaths {
		if !modifiableSourceFile(p) {
			t.Fatalf("modifiableSourceFile(%q) = false, want true", p)
		}
		if !admitSourceFile(p, []byte("export const x = 1;\n"), packages) {
			t.Fatalf("the directory gate refused %q. The TypeScript family declares NO package "+
				"clause; the gate must treat that as admissible. The opposite is why ky and "+
				"express aborted with exit 1.", p)
		}
	}
	if len(packages) != 0 {
		t.Errorf("the gate recorded package clauses %v for a family that has none", packages)
	}
}

// TestFreshnessTypeScript_DeclarationFilesAreNotCandidates: a `.d.ts` declares
// types only, so an appended function BODY would not be valid there.
func TestFreshnessTypeScript_DeclarationFilesAreNotCandidates(t *testing.T) {
	if modifiableSourceFile("source/types/options.d.ts") {
		t.Error("a .d.ts file was admitted: it declares types only and cannot host a function body")
	}
	if !modifiableSourceFile("source/types/options.ts") {
		t.Error("an ordinary .ts file was refused; the .d.ts exclusion must not swallow it")
	}
}

// TestFreshnessTypeScript_TSSequenceMutatesAndConverges is the AC-3 pin for the
// typescript language over a ky-shaped tree.
func TestFreshnessTypeScript_TSSequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"source/index.ts":         "import {Ky} from './core/Ky.js';\n\nexport function ky(): number {\n\treturn 1;\n}\n",
		"source/core/Ky.ts":       "export class Ky {\n\tfetch(): number {\n\t\treturn 2;\n\t}\n}\n",
		"source/utils/timeout.ts": "export function timeout(ms: number): number {\n\treturn ms;\n}\n",
		"package.json":            "{\"name\": \"ky\"}\n",
		"readme.md":               "# ky\n",
	}
	steps := runFamilyFreshness(t, "typescript", tree, 12)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".ts") {
			t.Errorf("step %d adds %s, which is not a TypeScript file", s.index, s.path)
		}
	}
}

// TestFreshnessTypeScript_TSXSequenceMutatesAndConverges pins tsx separately.
// SW-182's AC-2 discipline is that a gate discharged on typescript does not
// discharge for tsx; the same rule applies to this harness's own coverage.
func TestFreshnessTypeScript_TSXSequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"source/App.tsx":            "export function App(): number {\n\treturn 1;\n}\n",
		"source/components/Row.tsx": "export function Row(): number {\n\treturn 2;\n}\n",
	}
	steps := runFamilyFreshness(t, "tsx", tree, 8)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".tsx") {
			t.Errorf("step %d adds %s, which is not a TSX file", s.index, s.path)
		}
	}
}

// TestFreshnessTypeScript_JavaScriptSequenceMutatesAndConverges pins javascript
// over an express-shaped, CommonJS tree.
func TestFreshnessTypeScript_JavaScriptSequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"lib/express.js":      "var mixin = require('merge-descriptors');\n\nfunction createApplication() {\n  return 1;\n}\n\nmodule.exports = createApplication;\n",
		"lib/router/index.js": "var debug = require('debug');\n\nfunction Router() {\n  return 2;\n}\n\nmodule.exports = Router;\n",
		"lib/utils.js":        "exports.etag = function etag() {\n  return 3;\n};\n",
		"package.json":        "{\"name\": \"express\"}\n",
	}
	steps := runFamilyFreshness(t, "javascript", tree, 12)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".js") {
			t.Errorf("step %d adds %s, which is not a JavaScript file", s.index, s.path)
		}
	}
}

// TestFreshnessTypeScript_JavaScriptDeclarationIsModuleSystemNeutral is the
// express-specific pin: the generated JavaScript must not assume ESM, because
// the pin that exercises it is CommonJS.
func TestFreshnessTypeScript_JavaScriptDeclarationIsModuleSystemNeutral(t *testing.T) {
	js := familyForPath("lib/express.js")
	if js == nil || js.name != "javascript" {
		t.Fatalf("a .js path resolves to %v, want the javascript family", js)
	}
	decl := js.declaration("GraphiEvalStep0001", 1)
	if strings.Contains(decl, "export ") {
		t.Errorf("the JavaScript declaration uses an ESM export; express-4.18.2 is CommonJS "+
			"and the declaration must be valid in both module systems:\n%s", decl)
	}
	// TypeScript, by contrast, is ESM in every pin the corpus carries, and its
	// export is what makes the symbol part of the module's surface.
	ts := familyForPath("source/index.ts")
	if !strings.Contains(ts.declaration("GraphiEvalStep0001", 1), "export function") {
		t.Error("the TypeScript declaration is not exported; ky is an ESM package")
	}
}
