package jvmresolve

// Slice 7 (WP-J3/J4): the seam adapter — jvmresolve as an ADR 0007
// typeresolve.Resolver, ONE registrant per language, because the kill
// switches, the capability ladder and the trust facts are all per-language.
//
// WHY Input SPANS BOTH LANGUAGES while Subject and the emitted edges stay
// per-language: the declaration table must see .java AND .kt sources so
// cross-language types resolve (a Java file importing a Kotlin class binds
// intra-repo), but each registrant OWNS only its own language's edges and
// units — engine/ingest's reconciliation sweeps stale confirmed edges of
// CHECKED units, so a registrant claiming the sibling language's directories
// would delete the sibling's fresh edges.
//
// MIXED-LANGUAGE DIRECTORIES (.java and .kt side by side) are claimed by
// NEITHER registrant: their unit rows are emitted DEGRADED with a named
// reason, which exempts them from the stale sweep. The alternative — either
// side claiming the dir — would let one enabled language sweep away a
// kill-switched sibling's confirmed edges: degradation never deletes
// knowledge, so the mixed dir keeps possibly-stale edges and says so in its
// evidence row instead.
//
// The walkers' and emitter's NAMED skip counters reach the outside world
// through typeresolve.Result.NamedSkips — a slot of their own, never folded
// into TypeErrors (which would be a lie: a skip is a refusal to bind, not a
// type error). They remain REPO-GLOBAL and un-attributed; see the field's own
// doc comment for what that forbids a consumer from claiming.

import (
	"path"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/typeresolve"
)

// reasonMixedDir is the degradation reason for a directory holding both
// languages' sources (sweep-exempt; see the package comment).
const reasonMixedDir = "mixed-language directory (java+kotlin): stale-sweep exempt so neither registrant deletes the other's edges"

// mergeSkips sums the walker's Phase-B named gaps and the emitter's Phase-C
// identity gaps into ONE counter map — both are abstentions of the same pass
// and the vocabularies are disjoint by construction. Zero counts are dropped
// so an empty map and a map of zeros can never be told apart downstream, and
// nil is returned when nothing was skipped (the "no named skip recorded"
// value; see typeresolve.Result.NamedSkips).
func mergeSkips(sets ...SkipCounts) map[string]int {
	out := map[string]int{}
	for _, s := range sets {
		for name, n := range s {
			if n > 0 {
				out[name] += n
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Resolver adapts the binder to the ADR 0007 seam for one language.
type Resolver struct{ lang string }

// NewResolver returns the seam registrant for LangJava or LangKotlin.
func NewResolver(lang string) Resolver { return Resolver{lang: lang} }

// Language implements typeresolve.Resolver.
func (r Resolver) Language() string { return r.lang }

func (r Resolver) suffix() string {
	if r.lang == LangJava {
		return ".java"
	}
	return ".kt"
}

// Subject implements typeresolve.Resolver: an own-language source file.
func (r Resolver) Subject(relPath string) bool { return strings.HasSuffix(relPath, r.suffix()) }

// Input implements typeresolve.Resolver: every JVM source, both languages —
// the cross-language type context (see the package comment).
func (r Resolver) Input(relPath string) bool {
	return strings.HasSuffix(relPath, ".java") || strings.HasSuffix(relPath, ".kt")
}

// Triggers implements typeresolve.Resolver: any JVM source change can change
// cross-language resolution, so the whole-repo recompute re-runs for either.
func (r Resolver) Triggers(relPath string) bool { return r.Input(relPath) }

// Resolve implements typeresolve.Resolver: table → walk own-language bodies →
// emit own-language edges → fold into the Result shape the trust facts and
// the ingest reconciliation consume. Pure and deterministic.
func (r Resolver) Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (typeresolve.Result, error) {
	tab := BuildTable(files)
	ix := NewIndex(tab)

	var sites []TypedSite
	var walkSkips SkipCounts
	if r.lang == LangJava {
		sites, walkSkips = ix.AnalyzeJavaBodies(files)
	} else {
		sites, walkSkips = ix.AnalyzeKotlinBodies(files)
	}
	er, err := ix.EmitForLanguage(r.lang, sites, committed)
	if err != nil {
		return typeresolve.Result{}, err
	}

	res := typeresolve.Result{
		Edges:          er.Edges,
		DroppedIntents: er.DroppedIntents,
		NamedSkips:     mergeSkips(walkSkips, er.Skips),
	}

	// Directory bookkeeping: which dirs hold own-language files, which hold
	// the sibling language too, and which own-language files failed to table.
	ownDirs := map[string]string{} // dir -> declared package of the first (sorted) own-language file
	siblingDirs := map[string]bool{}
	for fi := range tab.Files {
		f := &tab.Files[fi]
		dir := path.Dir(f.Path)
		if f.Language == r.lang {
			if _, seen := ownDirs[dir]; !seen {
				ownDirs[dir] = f.Package // tab.Files is sorted by path
			}
		} else {
			siblingDirs[dir] = true
		}
	}
	degraded := map[string]string{}
	for _, s := range tab.Skipped {
		dir := path.Dir(s.Path)
		if r.Subject(s.Path) {
			res.SkippedFiles = append(res.SkippedFiles, typeresolve.SkippedFile{Path: s.Path, Reason: s.Reason})
			if _, seen := ownDirs[dir]; !seen {
				ownDirs[dir] = ""
			}
			if _, dup := degraded[dir]; !dup {
				degraded[dir] = "jvm parse skip: " + s.Path
			}
		} else {
			siblingDirs[dir] = true
		}
	}

	dirs := make([]string, 0, len(ownDirs))
	for d := range ownDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		u := typeresolve.UnitResult{
			Dir:            dir,
			Name:           ownDirs[dir],
			DroppedIntents: er.DroppedByDir[dir],
		}
		if reason, isDegraded := degraded[dir]; isDegraded {
			u.Degraded = reason
		} else if siblingDirs[dir] {
			u.Degraded = reasonMixedDir
		}
		res.Units = append(res.Units, u)
	}
	return res, nil
}
