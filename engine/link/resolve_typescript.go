package link

// tsResolver is the FU-5 registration for the TypeScript family (TypeScript, TSX,
// and JavaScript share one resolver impl — the same ESM/CJS import surface and the
// same cstWalk extraction conventions). It is registered three times under the
// three language ids the parsers emit ("typescript", "tsx", "javascript").
//
// Semantics modelled (NOT Go's): ES module imports bind names into file scope —
//
//   - import { X } from "./mod"      → bare use of X resolves to mod's symbol X;
//   - import * as ns from "./mod"    → ns.X resolves to mod's symbol X;
//   - import Def from "./mod"        → default import: no provable named binding,
//     but the file→file imports edge is still emitted;
//   - import "./side"                → side-effect import: imports edge only.
//
// Path resolution is RELATIVE-ONLY (design decision D1): "./x" / "../x" resolve
// against the importing file's directory; non-relative / aliased specifiers
// ("react", "@app/x", tsconfig `paths`) are treated as external and skipped+counted
// — no tsconfig path-mapping is attempted. Every cross-file resolution is heuristic
// tier; nothing is fabricated (unresolved/ambiguous → st.Skipped / st.Ambiguous).
type tsResolver struct{ lang string }

// tsExts are the module-file extension candidates the TypeScript family resolves a
// relative specifier against (specifiers omit the extension).
var tsExts = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

// Language implements Resolver.
func (r tsResolver) Language() string { return r.lang }

// Resolve implements Resolver for the TypeScript family via the shared core.
func (tsResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	b := binder{
		selBaseDirs:        map[string][]string{},
		bareNameDirs:       map[string][]string{},
		selBaseImportPath:  map[string]string{},
		bareNameImportPath: map[string]string{},
		// WP-14: a name imported from a NON-relative package specifier (`import {fn}
		// from "pkg"`, `import * as ns from "pkg"`) that resolves to no committed
		// node is a genuine external reference — mint an interned external node keyed
		// by the package-qualified FQN ("pkg.fn", "@scope/pkg.member").
		externalQN: externalMemberQN,
		// TS module resolution is relative-only (D1): a non-relative specifier is
		// NEVER a repo package, so its bindings must not be crossModule-resolved
		// (which would false-match an unrelated repo directory sharing the
		// specifier's basename) — they resolve straight to an external node.
		importPathsExternalOnly: true,
	}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		dirs := relModuleDir(in.Dir, imp.Path)
		if dirs == nil {
			// Non-relative / aliased specifier: not resolvable to a repo file (D1),
			// but the imported binding names an external symbol with a known package
			// path. Record it so an otherwise-unresolved use materializes as an
			// external node (WP-14) instead of a silent skip.
			if imp.Alias != "" {
				b.selBaseImportPath[imp.Alias] = imp.Path
				b.bareNameImportPath[imp.Alias] = imp.Path
			}
			continue
		}
		if imp.Alias != "" {
			// The alias is either a named binding (import {X}) or a namespace alias
			// (import * as ns). Both are honest candidates: a named-import bare use
			// resolves via bareNameDirs; a namespace selector via selBaseDirs. A
			// lookup only succeeds against a committed node, so registering the alias
			// in both maps never fabricates an edge.
			b.bareNameDirs[imp.Alias] = dirs
			b.selBaseDirs[imp.Alias] = dirs
		}
		b.importFileTargets = append(b.importFileTargets, relModuleFileCandidates(in.Dir, imp.Path, tsExts)...)
	}
	return resolveRefs(in, idx, st, b)
}
