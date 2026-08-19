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
// MIXED-LANGUAGE DIRECTORIES (.java and .kt side by side) are PARTITIONED, not
// exempted — ADR 0008 ruling D9, landed by SW-170. Each registrant claims the
// directory as its OWN unit and sweeps only the edges whose from-node is one of
// its own language's files (typeresolve.Resolver.Owns, the language half of the
// (directory, language) sweep key). Two units, no exemption, nothing to skip.
//
// WHAT THIS REPLACED, AND WHY. Until D9 a mixed directory was emitted DEGRADED
// by BOTH registrants under a named reason, which exempted it from the stale
// sweep. That was chosen to stop one enabled language sweeping away a
// kill-switched sibling's confirmed edges — a real hazard, but the cure was
// worse than the disease: an exemption is UNOBSERVABLE (no counter, no
// diagnostic, and the unit rows claimed a degradation that had not happened),
// and in exactly the layout Kotlin-in-Java adoption produces it kept superseded
// confirmed edges alive across every incremental sync, forever. Measured on the
// hermetic fixture the exemption cost one wrong surviving edge per superseded
// call (docs/rc/parity-classes-jvm.yaml, jvm_mixed_dir_* rows). The hazard it
// was guarding against is gone by construction: a registrant can no longer
// reach the sibling's edges at all, because the sibling's files are not in its
// Owns set.
//
// The DEGRADED slot still means what it always meant — a parse skip, an
// unreadable file — and still exempts its unit from the sweep. Being mixed is
// no longer one of its reasons.
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

// Owns implements typeresolve.Resolver: an own-language source file, the same
// set as Subject. The JVM registrants have no non-subject own-language files
// (nothing here is Go's _test.go), so Owns is Subject exactly — and because
// ".java" and ".kt" are disjoint, the java and kotlin registrants can never
// claim the same node. That disjointness is what makes a mixed directory two
// sweep units instead of one exemption (ADR 0008 D9).
func (r Resolver) Owns(relPath string) bool { return r.Subject(relPath) }

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

	// Directory bookkeeping: which dirs hold own-language files, and which
	// own-language files failed to table. The sibling language is deliberately
	// NOT tracked any more (ADR 0008 D9): a sibling file cannot degrade this
	// registrant's unit, because the sweep can no longer reach the sibling's
	// edges — see the package comment.
	ownDirs := map[string]string{} // dir -> declared package of the first (sorted) own-language file
	for fi := range tab.Files {
		f := &tab.Files[fi]
		if f.Language != r.lang {
			continue
		}
		dir := path.Dir(f.Path)
		if _, seen := ownDirs[dir]; !seen {
			ownDirs[dir] = f.Package // tab.Files is sorted by path
		}
	}
	degraded := map[string]string{}
	for _, s := range tab.Skipped {
		if !r.Subject(s.Path) {
			continue // the sibling registrant's file, and the sibling's problem
		}
		dir := path.Dir(s.Path)
		res.SkippedFiles = append(res.SkippedFiles, typeresolve.SkippedFile{Path: s.Path, Reason: s.Reason})
		if _, seen := ownDirs[dir]; !seen {
			ownDirs[dir] = ""
		}
		if _, dup := degraded[dir]; !dup {
			degraded[dir] = "jvm parse skip: " + s.Path
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
		}
		res.Units = append(res.Units, u)
	}
	return res, nil
}
