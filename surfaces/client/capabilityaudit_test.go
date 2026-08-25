package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
)

// SW-183 — THE CAPABILITY AUDIT.
//
// `surfaces/client/capability_test.go` already binds the capability matrix to
// the live registries: it proves the DERIVATION is faithful to what is
// REGISTERED. This file asks the question that one cannot: is what is
// registered faithful to what the product can actually DO?
//
// The distinction is the whole point of the story. `trust.Capabilities` grades
// `cross-file-heuristic` on "is a resolver registered for L?"
// (engine/trust/capability.go:113-116). A resolver that resolves nothing is
// still a registered resolver, so the derivation can over-claim without any
// existing test noticing — every one of them is downstream of the same
// registration.
//
// This audit closes that loop with EVIDENCE rather than registration: for every
// shipped language it ingests a two-file fixture carrying a real cross-file
// relationship, written in that language's own syntax, and asks the committed
// graph whether a cross-file edge exists. The published table is
// docs/rc/capability-audit-2026-08-19.md.
//
// WHY THE FIXTURES ARE EMBEDDED HERE AND NOT UNDER corpus/.
// The per-language GA template (docs/plan/2026-08-per-language-ga-template-v1.md
// §3/S10) puts fixtures under `corpus/hero-<fam>/`. This audit deliberately
// diverges, on the strength of that same document's §7 enforcement audit: every
// corpus-backed slot is REVIEW-ONLY for existence, because the guards read a
// hardcoded const path and nothing globs `corpus/hero-*` — a family that ships
// no fixture directory is SILENT, not red. An audit whose entire claim is
// exhaustiveness must not be built on the one storage choice that cannot fail by
// omission. Embedded in the table below, an unfixtured language is a compile-time
// map miss caught by TestCapabilityAudit_EveryShippedLanguageIsFixtured.
// Recorded as a template divergence in the published audit.
//
// WHY THE FIXTURES LOOK OVER-SPECIFIED.
// Several of them were WRONG on the first pass, and each wrong one would have
// re-graded a language downward on the strength of a fixture defect:
//
//   - ruby `helper` (no parens) records no call ref at all, so Ruby looked like
//     it resolved no cross-file symbol reference. With `helper()` it resolves.
//   - javascript `from "./util.js"` (explicit extension) yields the `calls` edge
//     but no `imports` edge; `from "./util"` yields both.
//   - python `from pkg.util import helper` yields NOTHING — and that one is NOT
//     a fixture defect. It is LINK-004, pinned separately below.
//
// This is the SW-180 fixture-design rule ("a fixture that cannot express the
// relation under test cannot validate the rule about it") turned on the audit
// itself. The canonical, working form is committed here; the failing forms are
// published in the audit table as measured negative results, never deleted (D5).

// auditCase is one language's audit fixture: a minimal repository carrying a
// genuine cross-file relationship, plus the two facts needed to grade the
// outcome without guessing.
type auditCase struct {
	// files is the whole fixture repository, repo-relative path → content.
	files map[string]string

	// referrer and target are the two SOURCE PATHS the relationship spans. A
	// cross-file edge counts for this case only if it joins these two files, so
	// an unrelated edge elsewhere in the fixture cannot make a language look
	// capable.
	referrer, target string

	// witness is a qualified name that MUST exist as a committed node. It is the
	// non-vacuity guard: if the target symbol never entered the graph, "no
	// cross-file edge" proves nothing about cross-file resolution, and the row
	// would be green (or red) for the wrong reason.
	witness string

	// askable records the per-language GA template §5.5 disposition: does L's OWN
	// SPECIFICATION define a construct that names another file? Where it does
	// not, the cross-file question is not askable of L and the fixture's
	// negative result is an abstention, not a measurement. specCite carries the
	// citation — the LANGUAGE spec, never graphi's own parser comment, which
	// would make the abstention circular.
	askable  bool
	specCite string
}

// auditCases is the fixture table. One entry per shipped language; the key is
// the canonical language id from the parser registry.
func auditCases() map[string]auditCase {
	return map[string]auditCase{
		// ---- typed-confirmed --------------------------------------------
		"go": {
			files: map[string]string{
				"go.mod":      "module example.com/m\n\ngo 1.26\n",
				"pkg/util.go": "package pkg\n\nfunc Helper() int { return 1 }\n",
				"main.go":     "package main\n\nimport \"example.com/m/pkg\"\n\nfunc Run() int { return pkg.Helper() }\n",
			},
			referrer: "main.go", target: "pkg/util.go", witness: "pkg.Helper",
			askable: true, specCite: "Go spec, Import declarations",
		},

		// ---- cross-file-heuristic ---------------------------------------
		"python": {
			// `from pkg import util` — the form that RESOLVES. The dotted-module
			// form `from pkg.util import helper` is LINK-004 and is pinned by
			// TestLink004_PythonDottedModuleImportResolvesNothing, not here.
			files: map[string]string{
				"pkg/__init__.py": "",
				"pkg/util.py":     "def helper():\n    return 1\n",
				"app.py":          "from pkg import util\n\n\ndef run():\n    return util.helper()\n",
			},
			referrer: "app.py", target: "pkg/util.py", witness: "pkg.helper",
			askable: true, specCite: "Python Language Reference §7.11 'The import statement'",
		},
		"typescript": {
			files: map[string]string{
				"util.ts": "export function helper(): number {\n  return 1;\n}\n",
				"main.ts": "import { helper } from \"./util\";\n\nexport function run(): number {\n  return helper();\n}\n",
			},
			referrer: "main.ts", target: "util.ts", witness: "util.helper",
			askable: true, specCite: "ECMAScript §16.2 Modules / TypeScript module resolution",
		},
		"tsx": {
			files: map[string]string{
				"util.tsx": "export function helper(): number {\n  return 1;\n}\n",
				"main.tsx": "import { helper } from \"./util\";\n\nexport function run(): number {\n  return helper();\n}\n",
			},
			referrer: "main.tsx", target: "util.tsx", witness: "util.helper",
			askable: true, specCite: "ECMAScript §16.2 Modules / TypeScript module resolution",
		},
		"javascript": {
			// Extensionless specifier. With "./util.js" the `calls` edge still
			// resolves but the file→file `imports` edge does not — recorded in
			// the audit table as a measured recall quirk, not a level change.
			files: map[string]string{
				"util.js": "export function helper() {\n  return 1;\n}\n",
				"main.js": "import { helper } from \"./util\";\n\nexport function run() {\n  return helper();\n}\n",
			},
			referrer: "main.js", target: "util.js", witness: "util.helper",
			askable: true, specCite: "ECMAScript §16.2 Modules",
		},
		"java": {
			files: map[string]string{
				"com/app/lib/Util.java": "package com.app.lib;\n\npublic class Util {\n    public static int helper() { return 1; }\n}\n",
				"com/app/Main.java":     "package com.app;\n\nimport com.app.lib.Util;\n\npublic class Main {\n    public static int run() { return Util.helper(); }\n}\n",
			},
			referrer: "com/app/Main.java", target: "com/app/lib/Util.java", witness: "lib.helper",
			askable: true, specCite: "JLS §7.5 Import Declarations",
		},
		"kotlin": {
			files: map[string]string{
				"com/app/lib/Util.kt": "package com.app.lib\n\nobject Util {\n    fun helper(): Int = 1\n}\n",
				"com/app/Main.kt":     "package com.app\n\nimport com.app.lib.Util\n\nfun run(): Int = Util.helper()\n",
			},
			referrer: "com/app/Main.kt", target: "com/app/lib/Util.kt", witness: "lib.helper",
			askable: true, specCite: "Kotlin language specification, 'Packages and imports'",
		},
		"c_sharp": {
			files: map[string]string{
				"Lib/Util.cs": "namespace App.Lib\n{\n    public class Util\n    {\n        public static int Helper() { return 1; }\n    }\n}\n",
				"Main.cs":     "using App.Lib;\n\nnamespace App\n{\n    public class Program\n    {\n        public static int Run() { return Util.Helper(); }\n    }\n}\n",
			},
			referrer: "Main.cs", target: "Lib/Util.cs", witness: "Lib.Helper",
			askable: true, specCite: "ECMA-334 §13.5 Using directives",
		},
		"c": {
			files: map[string]string{
				"util.h": "int helper(void) { return 1; }\n",
				"main.c": "#include \"util.h\"\n\nint run(void) { return helper(); }\n",
			},
			referrer: "main.c", target: "util.h", witness: "util.helper",
			askable: true, specCite: "ISO/IEC 9899 §6.10.2 Source file inclusion",
		},
		"cpp": {
			files: map[string]string{
				"util.hpp": "int helper() { return 1; }\n",
				"main.cpp": "#include \"util.hpp\"\n\nint run() { return helper(); }\n",
			},
			referrer: "main.cpp", target: "util.hpp", witness: "util.helper",
			askable: true, specCite: "ISO/IEC 14882 [cpp.include]",
		},
		"rust": {
			files: map[string]string{
				"util.rs": "pub fn helper() -> i32 {\n    1\n}\n",
				"main.rs": "mod util;\n\nfn run() -> i32 {\n    util::helper()\n}\n",
			},
			referrer: "main.rs", target: "util.rs", witness: "util.helper",
			askable: true, specCite: "Rust Reference, 'Modules' (mod item, out-of-line module file)",
		},
		"ruby": {
			// `helper()` WITH parentheses. Bare `helper` is parsed as an
			// identifier read, records no call ref, and made Ruby look
			// cross-file-incapable on the first audit pass.
			files: map[string]string{
				"util.rb": "def helper\n  1\nend\n",
				"main.rb": "require_relative 'util'\n\ndef run\n  helper()\nend\n",
			},
			referrer: "main.rb", target: "util.rb", witness: "util.helper",
			askable: true, specCite: "Ruby core: Kernel#require_relative",
		},
		"php": {
			files: map[string]string{
				"util.php": "<?php\nfunction helper() { return 1; }\n",
				"main.php": "<?php\nrequire_once 'util.php';\n\nfunction run() { return helper(); }\n",
			},
			referrer: "main.php", target: "util.php", witness: "util.helper",
			askable: true, specCite: "PHP language reference, 'require_once'",
		},
		"lua": {
			files: map[string]string{
				"util.lua": "function helper()\n  return 1\nend\n",
				"main.lua": "require(\"util\")\n\nfunction run()\n  return helper()\nend\n",
			},
			referrer: "main.lua", target: "util.lua", witness: "util.helper",
			askable: true, specCite: "Lua 5.4 Reference Manual §6.3 'require'",
		},
		"bash": {
			// AC-4: the programme plan's Wave 3 sketch assumed bash was
			// `intra-file-only`; the live derivation says `cross-file-heuristic`.
			// This fixture settles it — see the audit table.
			files: map[string]string{
				"util.sh": "helper() {\n  echo 1\n}\n",
				"main.sh": "source ./util.sh\n\nrun() {\n  helper\n}\n",
			},
			referrer: "main.sh", target: "util.sh", witness: "util.helper",
			askable: true, specCite: "POSIX.1-2017 Shell & Utilities, the `.` (dot) special built-in",
		},
		"sql": {
			// AC-3. The published claim was that SQL's resolver "proves no
			// cross-file references and counts skips instead". It does not: a
			// view referencing a table declared in a sibling file resolves at the
			// `derived` tier, and the edge disappears when the registration is
			// removed. See the audit table's counterfactual.
			files: map[string]string{
				"schema.sql": "CREATE TABLE users (id INT);\n",
				"query.sql":  "CREATE VIEW active_users AS SELECT id FROM users;\n",
			},
			referrer: "query.sql", target: "schema.sql", witness: "schema.users",
			// SQL has no include construct in ISO/IEC 9075 — `\i` is a psql
			// client command, `SOURCE` a mysql client command. The cross-file
			// question is nonetheless ASKABLE of SQL, because SQL's own spec
			// defines cross-STATEMENT name resolution (a view's FROM clause names
			// a table by schema-qualified identifier) and graphi's unit of
			// storage is the file. §5.5's test is about naming another file; SQL
			// names another OBJECT, which the product then locates in another
			// file. Marked askable and MEASURED rather than substituted, which is
			// the stronger claim of the two.
			askable: true, specCite: "ISO/IEC 9075-2 §7.6 <table reference>",
		},

		// ---- intra-file-only ---------------------------------------------
		"css": {
			files: map[string]string{
				"base.css": ".btn { color: red; }\n",
				"main.css": "@import \"base.css\";\n\n.page { color: blue; }\n",
			},
			referrer: "main.css", target: "base.css", witness: "base..btn",
			askable: true, specCite: "CSS Cascading and Inheritance, @import",
		},
		"markdown": {
			files: map[string]string{
				"base.md":   "# Base\n\nShared prose.\n",
				"readme.md": "# Readme\n\nSee [base](./base.md).\n",
			},
			referrer: "readme.md", target: "base.md", witness: "base.Base",
			askable: true, specCite: "CommonMark §6.3 Links (inline link destination)",
		},
		"yaml": {
			files: map[string]string{
				"base.yaml": "name: base\n",
				"main.yaml": "include: ./base.yaml\nname: main\n",
			},
			referrer: "main.yaml", target: "base.yaml", witness: "base.name",
			askable:  false,
			specCite: "YAML 1.2.2 defines no include directive; `include:` is an ordinary mapping key and %YAML/%TAG name no file",
		},
		"toml": {
			files: map[string]string{
				"base.toml": "name = \"base\"\n",
				"main.toml": "include = \"base.toml\"\nname = \"main\"\n",
			},
			referrer: "main.toml", target: "base.toml", witness: "base.name",
			askable:  false,
			specCite: "TOML v1.0.0 defines no include; a path in a value is a string the reading application interprets",
		},
		"hcl": {
			files: map[string]string{
				"base.tf": "variable \"name\" {\n  default = \"base\"\n}\n",
				"main.tf": "module \"base\" {\n  source = \"./base.tf\"\n}\n",
			},
			referrer: "main.tf", target: "base.tf", witness: "base.variable.name",
			askable:  false,
			specCite: "HCL's native syntax spec defines no file reference; `module { source }` is Terraform's schema layered on HCL, structurally identical to `include:` being an application's schema layered on YAML",
		},

		// ---- parse-only ---------------------------------------------------
		"json": {
			files: map[string]string{
				"base.json": "{ \"name\": \"base\" }\n",
				"main.json": "{ \"$ref\": \"./base.json\", \"name\": \"main\" }\n",
			},
			referrer: "main.json", target: "base.json",
			// JSON extracts no symbols and mints no committed `file` node, so
			// there is no witness to demand. The empty string opts this case out
			// of the non-vacuity check, and the absence is itself asserted.
			witness:  "",
			askable:  false,
			specCite: "RFC 8259 defines no reference construct; JSON Schema's `$ref` is a downstream vocabulary, not part of JSON",
		},
	}
}

// TestCapabilityAudit_EveryShippedLanguageIsFixtured is AC-1's class-level
// discipline and the reason the fixture table lives in this file.
//
// The audit's whole claim is EXHAUSTIVENESS, and the SW-150 lesson this project
// has now learned twice is that a sweep returning exactly its own input is not
// evidence of it. So the language set is taken from the LIVE derivation — the
// same languageCapabilities() assembly the trust report serves — and compared to
// the fixture table in BOTH directions. A grammar landing tomorrow fails this
// test until somebody writes it a fixture; a fixture for a language that is no
// longer shipped fails it too.
func TestCapabilityAudit_EveryShippedLanguageIsFixtured(t *testing.T) {
	derived := languageCapabilities()
	if len(derived) == 0 {
		t.Fatal("VACUOUS: languageCapabilities() returned no rows; the audit would pass by having nothing to check")
	}
	cases := auditCases()

	derivedSet := make(map[string]trust.CapabilityLevel, len(derived))
	for _, c := range derived {
		derivedSet[c.Language] = c.Level
	}

	var unfixtured []string
	for lang := range derivedSet {
		if _, ok := cases[lang]; !ok {
			unfixtured = append(unfixtured, lang)
		}
	}
	sort.Strings(unfixtured)
	if len(unfixtured) > 0 {
		t.Errorf("UNFIXTURED SHIPPED LANGUAGE(S) %v.\n"+
			"A language whose capability level is derived but never measured is a GAP, not a pass (AC-8).\n"+
			"Add a case to auditCases() carrying a real cross-file relationship in that language's own\n"+
			"syntax, then re-publish docs/rc/capability-audit-2026-08-19.md with its row.", unfixtured)
	}

	var stale []string
	for lang := range cases {
		if _, ok := derivedSet[lang]; !ok {
			stale = append(stale, lang)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("FIXTURE(S) FOR UNSHIPPED LANGUAGE(S) %v — the audit table has drifted past the registry", stale)
	}

	// The published table's row count. Pinned so that a silent change to the
	// shipped set cannot leave the published audit claiming a denominator it no
	// longer has.
	if len(derived) != 22 {
		t.Errorf("shipped language count = %d, published audit table has 22 rows.\n"+
			"Re-publish docs/rc/capability-audit-2026-08-19.md by dated amendment (D6) before changing this number.",
			len(derived))
	}
}

// TestCapabilityAudit_DerivedLevelMatchesProducibleEvidence is the audit proper
// (AC-1, AC-5, AC-6).
//
// For every shipped language: ingest its fixture and ask the COMMITTED GRAPH
// whether an edge spans the two files the relationship spans. Then grade:
//
//	typed-confirmed / cross-file-heuristic  ⇒  MUST produce one
//	intra-file-only / parse-only            ⇒  MUST NOT produce one
//
// Both directions are asserted, and the second is the one that makes this a
// grading test rather than a smoke test: a language quietly gaining cross-file
// resolution while still declaring `intra-file-only` is an UNDER-claim, and this
// programme's honesty rule cuts both ways.
//
// AC-6's binding lives here. The level is read from languageCapabilities(), not
// from a copied table, so a re-grade that edits only prose cannot make this test
// agree with it — the derivation has to move.
func TestCapabilityAudit_DerivedLevelMatchesProducibleEvidence(t *testing.T) {
	ctx := context.Background()
	derived := languageCapabilities()
	cases := auditCases()

	for _, cap := range derived {
		cap := cap
		fx, ok := cases[cap.Language]
		if !ok {
			continue // reported by the completeness test above
		}
		t.Run(cap.Language, func(t *testing.T) {
			t.Parallel()
			// The profile the CLI actually resolves for every user-facing pass
			// (core/profile.ResolveProfile). A capability claim measured under a
			// profile no user runs is not a claim about the product.
			g := auditIngest(t, ctx, fx.files, profile.Balanced)

			// NON-VACUITY FIRST. Without this, "no cross-file edge" is
			// indistinguishable from "the fixture never parsed".
			if fx.witness != "" {
				if !g.hasQN(fx.witness) {
					t.Fatalf("VACUOUS FIXTURE: witness %q is not a committed node, so this row proves\n"+
						"nothing about cross-file resolution. Committed qualified names: %v",
						fx.witness, g.qualifiedNames())
				}
			} else if g.hasSource(fx.referrer) {
				// json: the ABSENCE of a committed node is itself the claim.
				t.Fatalf("fixture %q declares no witness (no symbols expected) but %q IS a committed node —\n"+
					"the parse-only assumption behind this row no longer holds", cap.Language, fx.referrer)
			}

			spanning := g.edgesSpanning(fx.referrer, fx.target)
			produced := len(spanning) > 0

			// The published table's per-row evidence is REGENERATED from here
			// (`go test -run TestCapabilityAudit_DerivedLevel -v ./surfaces/client/`),
			// not transcribed from a session. A published figure a reader cannot
			// re-derive from the committed tree is a figure that drifts.
			if produced {
				t.Logf("AUDIT %s [%s]: %s", cap.Language, cap.Level, strings.Join(spanning, " | "))
			} else {
				askable := "askable"
				if !fx.askable {
					askable = "NOT askable — " + fx.specCite
				}
				t.Logf("AUDIT %s [%s]: no cross-file edge (%s)", cap.Language, cap.Level, askable)
			}

			switch cap.Level {
			case trust.CapabilityTypedConfirmed, trust.CapabilityCrossFileHeuristic:
				if !produced {
					t.Errorf("OVER-CLAIM: %s is derived %q but its fixture produces NO cross-file edge\n"+
						"between %q and %q.\n"+
						"Per AC-5 the language does not hold that level regardless of what is registered.\n"+
						"Either the registration is wrong (re-grade DOWN in engine/link.New) or this fixture\n"+
						"cannot express the relation (fix the fixture — see the header note on ruby/javascript).\n"+
						"All committed edges: %s",
						cap.Language, cap.Level, fx.referrer, fx.target, g.describeEdges())
				}
			case trust.CapabilityIntraFileOnly, trust.CapabilityParseOnly:
				if produced {
					t.Errorf("UNDER-CLAIM: %s is derived %q but its fixture DOES produce cross-file edge(s)\n"+
						"between %q and %q: %s\n"+
						"An under-claim is as dishonest as an over-claim. Re-grade UP by registering the\n"+
						"resolver that is evidently doing the work, and re-publish the audit table.",
						cap.Language, cap.Level, fx.referrer, fx.target, strings.Join(spanning, ", "))
				}
				// §5.5: where the cross-file question is not askable of L, the
				// negative result is an abstention and must carry the LANGUAGE
				// spec citation, never graphi's own parser comment.
				if !fx.askable && strings.TrimSpace(fx.specCite) == "" {
					t.Errorf("%s abstains from the cross-file question but cites no language-spec reason;\n"+
						"an uncited abstention is indistinguishable from an untested one (template §5.5)", cap.Language)
				}
			default:
				t.Fatalf("%s has level %q, outside the closed vocabulary", cap.Language, cap.Level)
			}
		})
	}
}

// TestLink004_PythonDottedModuleImportResolvesNothing pins the one genuine
// defect this audit found, in the SW-168 shape: it asserts the CURRENT BROKEN
// BEHAVIOUR and fails WITH INSTRUCTIONS the moment the defect is fixed, so the
// fix cannot land while the disclosure stays up.
//
// `from pkg.util import helper` and `import pkg.util` — the two commonest
// Python import forms in real code — resolve to nothing. The mechanism:
// pyClause (engine/link/resolve_python.go:86-91) keys a module path on its LAST
// dotted segment, but a symbol's clause is its PARENT DIRECTORY base
// (core/parse/parser_tswalk.go:240-251). For `pkg.util` those are "util" and
// "pkg" — they can only coincide when the module path has ONE segment, which is
// exactly the shape every existing test uses.
//
// NOT FIXED HERE. Fixing it is a product-byte change to the linker with its own
// ADR and its own re-measurement; this story is an audit. Filed as LINK-004 and
// disclosed per D8.
func TestLink004_PythonDottedModuleImportResolvesNothing(t *testing.T) {
	ctx := context.Background()
	forms := []struct {
		name  string
		files map[string]string
	}{
		{"from pkg.util import helper", map[string]string{
			"pkg/__init__.py": "",
			"pkg/util.py":     "def helper():\n    return 1\n",
			"app.py":          "from pkg.util import helper\n\n\ndef run():\n    return helper()\n",
		}},
		{"import pkg.util", map[string]string{
			"pkg/__init__.py": "",
			"pkg/util.py":     "def helper():\n    return 1\n",
			"app.py":          "import pkg.util\n\n\ndef run():\n    return pkg.util.helper()\n",
		}},
	}
	for _, f := range forms {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			g := auditIngest(t, ctx, f.files, profile.Balanced)
			if !g.hasQN("pkg.helper") {
				t.Fatalf("VACUOUS: pkg/util.py's helper is not a committed node; this pin would be green\n" +
					"for a reason unrelated to LINK-004")
			}
			spanning := g.edgesSpanning("app.py", "pkg/util.py")
			if len(spanning) != 0 {
				t.Fatalf("LINK-004 APPEARS FIXED for %q — %d cross-file edge(s) now resolve: %v\n\n"+
					"THIS IS GOOD NEWS AND THIS TEST IS NOW WRONG. Do all of the following in the SAME change:\n"+
					"  1. delete this test (its whole purpose was to pin the defect);\n"+
					"  2. RETRACT the LINK-004 disclosure on BOTH D8 surfaces — the canonical defect page\n"+
					"     docs/known-defects.md (its bullet AND its node and arrows in the mermaid diagram)\n"+
					"     AND the doctor known-defects check (internal/doctor/checks.go, plus its assertions\n"+
					"     and its id-set pin in checks_test.go). D8 permits retraction ONLY in the\n"+
					"     change that closes the defect, so leaving either up is now the violation;\n"+
					"  3. move the python row of docs/rc/capability-audit-2026-08-19.md by DATED AMENDMENT\n"+
					"     (D6 — add, never rewrite);\n"+
					"  4. carry the full product-byte ceremony (D7): the linker changed.",
					f.name, len(spanning), spanning)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// auditGraph is a read-only view of one fixture's committed graph.
type auditGraph struct {
	nodes []model.Node
	edges []model.Edge
	byID  map[model.NodeId]model.Node
}

// auditIngest materializes files under a private temp root, indexes it through
// the real Ingester under the given profile, and returns the committed graph.
func auditIngest(t *testing.T, ctx context.Context, files map[string]string, prof profile.Profile) *auditGraph {
	t.Helper()
	root := t.TempDir()
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(files[rel]), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	ing.WithProfile(prof)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	g := &auditGraph{nodes: nodes, edges: edges, byID: make(map[model.NodeId]model.Node, len(nodes))}
	for _, n := range nodes {
		g.byID[n.ID()] = n
	}
	return g
}

func (g *auditGraph) hasQN(qn string) bool {
	for _, n := range g.nodes {
		if n.QualifiedName() == qn {
			return true
		}
	}
	return false
}

func (g *auditGraph) hasSource(path string) bool {
	for _, n := range g.nodes {
		if n.SourcePath() == path {
			return true
		}
	}
	return false
}

func (g *auditGraph) qualifiedNames() []string {
	out := make([]string, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n.Kind()+" "+n.QualifiedName())
	}
	sort.Strings(out)
	return out
}

// edgesSpanning returns a description of every edge joining a node in `from` to
// a node in `to`, in EITHER direction.
//
// Direction is deliberately not constrained. `related_files` itself queries with
// `direction: both`, and the audit's question is whether the two files are
// joined by evidence at all — an inbound `imports` edge is as much a cross-file
// relationship as an outbound `calls` edge. Constraining direction here would
// have re-graded Ruby down on its `imports`-only result.
//
// An edge whose far end is an `external` node NEVER counts: minting a stub for
// an unresolved reference is the product's honest-miss path, and counting it
// would let "we failed to resolve this" masquerade as cross-file capability.
// That is the single most important line in this harness.
func (g *auditGraph) edgesSpanning(from, to string) []string {
	var out []string
	for _, e := range g.edges {
		a, okA := g.byID[e.From()]
		b, okB := g.byID[e.To()]
		if !okA || !okB {
			continue
		}
		if a.Kind() == "external" || b.Kind() == "external" {
			continue
		}
		if a.SourcePath() == b.SourcePath() {
			continue // intra-file by construction (`defines`, intra-file calls)
		}
		spans := (a.SourcePath() == from && b.SourcePath() == to) ||
			(a.SourcePath() == to && b.SourcePath() == from)
		if !spans {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s→%s (%s, tier=%s)",
			e.Kind(), a.QualifiedName(), b.QualifiedName(), a.SourcePath()+"→"+b.SourcePath(), e.Tier()))
	}
	sort.Strings(out)
	return out
}

func (g *auditGraph) describeEdges() string {
	var out []string
	for _, e := range g.edges {
		a, okA := g.byID[e.From()]
		b, okB := g.byID[e.To()]
		if !okA || !okB {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s(%s)→%s(%s)",
			e.Kind(), a.QualifiedName(), a.SourcePath(), b.QualifiedName(), b.SourcePath()))
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, "; ")
}
