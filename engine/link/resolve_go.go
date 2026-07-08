package link

import (
	"path"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// goResolver is the Go registration behind the Resolver seam. It is pure and
// store-free; it produces resolved intents only against committed NodeIds in idx
// and never fabricates a target.
type goResolver struct{}

// Language implements Resolver.
func (goResolver) Language() string { return "go" }

// Resolve turns one Go file's pending refs + imports into resolved intents:
//
//   - bare-ident call/ref → same-package (directory) lookup → `derived` intent;
//   - selector alias.Name → alias → import path → package clause → name →
//     `heuristic` intent (cross-package call/ref);
//   - selector recv.Method → unique receiver-name method match → `heuristic`;
//   - import declaration → file→file `imports` edge (Open Q2, file nodes exist).
//
// Dot (".") and blank ("_") imports never yield a resolvable selector and are
// skipped (no phantom edge). Unresolved stdlib / 3rd-party / local references are
// dropped and counted (st.Skipped / st.Ambiguous) — never an edge.
func (goResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	var out []intent

	// Map import alias → import path for selector resolution. A package imported
	// without an alias binds to the last segment of its path (Go's default).
	// Dot/blank imports are excluded: they never produce a resolvable selector.
	aliasToPath := map[string]string{}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		alias := imp.Alias
		switch alias {
		case ".", "_":
			continue // no selector qualifier ⇒ unresolvable here, no phantom
		case "":
			alias = path.Base(imp.Path)
		}
		aliasToPath[alias] = imp.Path
	}

	// Resolve each pending reference. fromQN is the owning symbol; we look up its
	// NodeId in the caller's own directory (same package).
	for _, p := range in.Pending {
		from, ok := idx.sameDir(in.Dir, lastSeg(p.FromQN))
		if !ok {
			// The owning symbol is not indexed (should not happen for committed
			// files); skip without fabricating an endpoint.
			st.Skipped++
			continue
		}
		ev := evidenceFor(in.SourcePath, p.Line)

		if !p.Selector {
			// Same-package (directory) bare-name resolution → derived.
			to, ok := idx.sameDir(in.Dir, p.Name)
			if !ok {
				// WP-03: a bare-ident miss is NOT materialized as an external node.
				// In Go a bare unresolved identifier is almost always a LOCAL
				// variable / parameter / builtin (e.g. `id`, `rows`, `_`), NOT an
				// external symbol — minting external nodes here would flood the graph
				// with mislabeled locals. External materialization is deliberately
				// scoped to SELECTOR misses with a known import path (drop-point 1)
				// or a receiver-qualified call (drop-point 2) below.
				st.Skipped++
				continue
			}
			if to == from && p.Kind == edgeReferences {
				// A symbol referencing itself by bare name (its own decl) is not
				// a meaningful reference edge; skip silently.
				st.Skipped++
				continue
			}
			out = append(out, intent{
				from: from, to: to, kind: p.Kind, class: classSamePackage,
				reason:   "same-package cross-file " + p.Kind + " resolved by name within the directory",
				evidence: ev,
			})
			st.ResolvedDerived++
			continue
		}

		// Selector reference: try alias → import path (cross-package) first, then
		// receiver-method heuristic.
		if impPath, isPkg := aliasToPath[p.SelectorBase]; isPkg {
			to, ok := idx.crossPackage(impPath, p.Name)
			if !ok {
				// WP-03 drop-point 1 (HIGH confidence): the selector base IS a known
				// import alias but the target is not in the repo (stdlib / 3rd-party).
				// The import path is known, so the external qualified name is exact
				// (impPath + "." + Name, e.g. "os/exec.Command", "os.ReadFile"). Mint
				// a heuristic-tier edge to an interned external node so name-keyed
				// analyses (taint sinks/sources) have a real node to match. These are
				// explicitly second-class heuristic nodes — never confirmed.
				out = append(out, intent{
					from: from, toExternalQN: impPath + "." + p.Name, kind: p.Kind, class: classSelector,
					reason:   "external " + p.Kind + " (unresolved import " + impPath + ")",
					evidence: ev,
				})
				st.ResolvedExternal++
				continue
			}
			out = append(out, intent{
				from: from, to: to, kind: p.Kind, class: classSelector,
				reason:   "cross-package " + p.Kind + " resolved via import " + impPath + " (alias " + p.SelectorBase + ")",
				evidence: ev,
			})
			st.ResolvedHeuristic++
			continue
		}

		// Not a known import alias ⇒ treat the base as a receiver name and try a
		// unique recv.Method heuristic. Only methods (calls) are attempted. A real
		// internal match ALWAYS wins over external materialization.
		if p.Kind == edgeCalls {
			to, ok := idx.receiverMethod(in.Dir, p.SelectorBase, p.Name)
			if ok {
				out = append(out, intent{
					from: from, to: to, kind: p.Kind, class: classSelector,
					reason:   "receiver-method call resolved heuristically by unique receiver-name match",
					evidence: ev,
				})
				st.ResolvedHeuristic++
				continue
			}
		}
		// A selector whose base is NOT an import alias and whose receiver-method
		// attempt missed is a receiver-qualified access on a value whose TYPE is
		// often unknown here: a local-var field/method (`res.Nodes`, `e.tier`,
		// `a.From`) or a chained selector (base == ""). These are NOT materialized as
		// `external` nodes (untyped): on real code most are local-var accesses, and a
		// best-effort QN (`db.Query`) cannot recover the receiver type, so it never
		// matches a config sink keyed on the fully-qualified stdlib name — minting
		// them floods the graph with mislabeled nodes (measured ~55% phantom on
		// engine/query) for zero taint value.
		//
		// Receiver-qualified external targets (WP-05b-1): when the extractor
		// SYNTACTICALLY inferred the receiver's declared type (a `db *sql.DB`
		// parameter / method receiver typed `[*]alias.T`), p.ReceiverType holds the
		// fully-qualified type ("database/sql.DB"). If that type's package is NOT in
		// the repo (the stdlib / 3rd-party case), mint a PRECISE external method node
		// keyed "<ReceiverType>.<method>" (e.g. "database/sql.DB.Query") so config
		// sinks match. This is type-guarded, so it does NOT reintroduce the WP-03
		// flood: an internal-package or unknown-typed receiver produces no node.
		if p.ReceiverType != "" {
			ti := strings.LastIndexByte(p.ReceiverType, '.')
			impPath := ""
			if ti > 0 {
				impPath = p.ReceiverType[:ti]
			}
			if impPath != "" && !idx.hasPackage(impPath) {
				out = append(out, intent{
					from: from, toExternalQN: p.ReceiverType + "." + p.Name, kind: p.Kind, class: classSelector,
					reason:   "external method call on typed receiver (" + p.ReceiverType + ")",
					evidence: ev,
				})
				st.ResolvedExternal++
				continue
			}
			// Internal-package receiver type: a real internal match wins / honest skip
			// — never mint external for a type committed in the repo.
		}
		// Otherwise: a receiver-qualified access on a value whose type is unknown
		// here (local-var field/method, chained selector) — an honest skip.
		// (Drop-point 1 above — the import-alias selector miss, where the import path
		// IS known — stays; its QN is exact.)
		st.Skipped++
	}

	// Imports edges: file→file. Each import path that resolves to an indexed
	// package directory yields one imports edge from this file node to that
	// package's file node(s). For FU-1 we link the importing file to the
	// directory's file node when uniquely determinable.
	if fileID, ok := idx.fileNode(in.SourcePath); ok {
		for _, imp := range in.Imports {
			if imp.Path == "" {
				continue
			}
			for _, targetFile := range idx.packageFileNodes(imp.Path) {
				if targetFile == fileID {
					continue
				}
				out = append(out, intent{
					from: fileID, to: targetFile, kind: edgeImports, class: classSelector,
					reason:   "file imports package " + imp.Path,
					evidence: evidenceFor(in.SourcePath, 1),
				})
				st.ResolvedHeuristic++
			}
		}
	}

	return out
}

// evidenceFor renders a deterministic, repo-relative POSIX "file:line" citation
// from the already-sanitized source path. Because SourcePath is normalized by
// model.NormalizePath upstream, the evidence never reveals an absolute or
// host-specific path even for a malicious repo.
func evidenceFor(sourcePath string, line int) string {
	return model.NormalizePath(sourcePath) + ":" + strconv.Itoa(line)
}

// lastSeg returns the trailing segment of a dotted qualified name (the bare name
// the same-package index keys on). "shop.checkout" → "checkout";
// "shop.Cart.Add" → "Add".
func lastSeg(qn string) string {
	_, bare := splitQN(qn)
	return bare
}
