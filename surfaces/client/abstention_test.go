package client

// W0.g — the surface half of legible abstention. Two documents must carry the
// binder's named skips: the `strict_query` envelope's Limitations (the place
// this repository already reports "what you are not seeing") and the trust
// report's own block.
//
// These tests hold the deliverable to the standard the story exists to
// enforce, which is not "a field exists" but "a user is told":
//
//   - the abstention fixture FORCES real skips, so no assertion here is
//     satisfied by an empty record;
//   - a Go-only control proves the same code path stays silent when there is
//     nothing to report, so the notice is information rather than boilerplate;
//   - the fail-closed case tries to make abstention DROP and asserts it becomes
//     a visible unavailability instead of silence.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/semantic"
)

// javaAbstentionFiles forces three named skip reasons in one Java package
// while keeping one call that genuinely binds. Identical to the ingest-level
// fixture on purpose: the numbers asserted here are the same numbers that
// test hand-computes, so a divergence localizes to the surface.
var javaAbstentionFiles = map[string]string{
	"com/tax/Rate.java": `package com.tax;
public class Rate {
    public void value() {}
}
`,
	"com/shop/Shop.java": `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        var inferred = param;
        inferred.value();
        param.missing();
        java.util.List<String> ext = null;
        ext.size();
        Unknown u = null;
        u.thing();
    }
}
`,
}

// goControlFiles is the CONTROL repository: same pipeline, a registrant with
// no named-skip vocabulary, so every abstention surface must stay quiet.
var goControlFiles = map[string]string{
	"go.mod":       "module example.com/fix\n\ngo 1.26\n",
	"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
	"main.go":      "package main\n\nimport \"example.com/fix/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
}

// buildAbstentionFixture indexes files through the production ingest path and
// returns the repo root, store path, meta dir and a live read client. The JVM
// registrants are opted in by the caller via t.Setenv where relevant — they are
// default-off in shipped binaries (WP-J11), so an abstention demonstration has
// to say out loud that it runs behind the opt-in.
func buildAbstentionFixture(t *testing.T, files map[string]string) (root, dbPath, metaDir string, c Client) {
	t.Helper()
	ctx := context.Background()

	root = t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	metaDir = t.TempDir()
	dbPath = filepath.Join(t.TempDir(), "graph.db")

	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close ingester: %v", err)
	}

	// Reopen read-only for querying; the composition opens its own read-only
	// handles for evidence, exactly as it does in production.
	ro, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		_ = store.Close()
		t.Fatalf("OpenSQLiteReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close(); _ = store.Close() })
	return root, dbPath, metaDir, NewDirect(query.New(ro), search.New(ro))
}

// nodeIDByName finds a committed node by qualified name.
func nodeIDByName(t *testing.T, dbPath, qname string) string {
	t.Helper()
	store, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, n := range nodes {
		if n.QualifiedName() == qname {
			return string(n.ID())
		}
	}
	var have []string
	for _, n := range nodes {
		have = append(have, n.QualifiedName())
	}
	t.Fatalf("node %q not found; have %v", qname, have)
	return ""
}

func strictEnvelope(t *testing.T, c Client, opts StrictQueryOptions) StrictEnvelope {
	t.Helper()
	b, _, _, err := ComposeStrictQuery(context.Background(), c, opts)
	if err != nil {
		t.Fatalf("ComposeStrictQuery: %v", err)
	}
	var env StrictEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, b)
	}
	return env
}

// findLimitation returns the first limitation containing needle.
func findLimitation(env StrictEnvelope, needle string) (string, bool) {
	for _, l := range env.Limitations {
		if strings.Contains(l, needle) {
			return l, true
		}
	}
	return "", false
}

// TestStrictQuery_SurfacesJVMAbstention is the AC-3/AC-7 pin and the
// end-to-end demonstration: a strict query whose result covers a Java package
// with recorded skips carries a limitation that NAMES the reasons and their
// counts, and states the repo-global/no-attribution scope in the same breath.
func TestStrictQuery_SurfacesJVMAbstention(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, c := buildAbstentionFixture(t, javaAbstentionFiles)
	target := nodeIDByName(t, dbPath, "tax.value")

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target,
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})

	got, ok := findLimitation(env, "abstention")
	if !ok {
		t.Fatalf("no abstention limitation on a result covering a package with recorded skips: %#v", env.Limitations)
	}
	// The named reasons and their counts, individually — a total alone would
	// be exactly the illegible number this story exists to replace.
	for _, want := range []string{
		"java_var_inferred 1",
		"java_lookup_not_found 1",
		"java_receiver_external 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("limitation does not name %q: %s", want, got)
		}
	}
	// AC-7: the scope limit travels with the numbers, in the surfaced text.
	for _, want := range []string{
		"repository-global",
		"no file, package, symbol or call-site attribution",
		"NOT counted inside it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("limitation does not state the scope (%q): %s", want, got)
		}
	}
	// The covered package is named, so the notice is actionable rather than
	// an unowned warning.
	if !strings.Contains(got, "com/shop") && !strings.Contains(got, "com/tax") {
		t.Errorf("limitation names no covered package: %s", got)
	}
}

// TestStrictQuery_GoControlStaysSilent is the non-vacuity control. Same
// composition, same code path, a repository whose only registrant has no named
// skips: NO abstention limitation may appear. Without this, the assertions
// above would also pass on an implementation that appended the notice
// unconditionally.
func TestStrictQuery_GoControlStaysSilent(t *testing.T) {
	root, dbPath, metaDir, c := buildAbstentionFixture(t, goControlFiles)
	target := nodeIDByName(t, dbPath, "util.Answer")

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target,
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if l, ok := findLimitation(env, "abstention"); ok {
		t.Errorf("a Go-only repository fabricated an abstention notice: %s", l)
	}
	if env.Limitations == nil {
		t.Error("Limitations must be [] and never null")
	}
}

// TestStrictQuery_AbstainedEmptinessIsNotProvenEmptiness is AC-6: when the
// result is empty AND the binder abstained, the envelope says so explicitly.
// The symbol queried is one whose only inbound call sites are exactly the ones
// the binder refused, so "no callers" here is genuinely a consequence of
// abstention rather than of absence — which is the situation the rule is about.
func TestStrictQuery_AbstainedEmptinessIsNotProvenEmptiness(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, c := buildAbstentionFixture(t, javaAbstentionFiles)
	// Shop.run itself has no callers at all: an empty result over a package
	// that carries a recorded abstention row.
	target := nodeIDByName(t, dbPath, "shop.run")

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target,
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if len(env.Result.Edges) != 0 {
		t.Fatalf("fixture assumption broken: expected an empty result, got %d edges", len(env.Result.Edges))
	}
	l, ok := findLimitation(env, "EMPTY")
	if !ok {
		t.Fatalf("an empty result with abstention present carried no emptiness caveat: %#v", env.Limitations)
	}
	if !strings.Contains(l, "not proven emptiness") {
		t.Errorf("the emptiness caveat does not refuse to launder the absence: %s", l)
	}
}

// TestStrictQuery_UnreadableEvidenceFailsLoud is the fail-closed attack: make
// the abstention record unreadable and assert the envelope says so rather than
// falling silent. Silence is the dangerous outcome here — it is
// indistinguishable from "nothing was skipped", which is precisely the false
// all-clear this surface exists to prevent.
func TestStrictQuery_UnreadableEvidenceFailsLoud(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, _, c := buildAbstentionFixture(t, javaAbstentionFiles)
	target := nodeIDByName(t, dbPath, "tax.value")

	// Point the composition at an empty meta directory: the graph store still
	// answers the query, but the evidence sidecar backing it holds nothing.
	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target,
		Root: root, DBPath: dbPath, MetaDir: t.TempDir(),
	})
	l, ok := findLimitation(env, "UNAVAILABLE")
	if !ok {
		t.Fatalf("abstention was silently dropped when the record could not be read: %#v", env.Limitations)
	}
	if !strings.Contains(l, "not evidence that nothing was skipped") {
		t.Errorf("the unavailability notice does not refuse the all-clear reading: %s", l)
	}
}

// TestStrictQuery_NotFoundResultAttractsNoNotice guards the other direction: a
// result that anchors nothing covers no package, so there is no scope an
// abstention notice could be about, and inventing one would be noise dressed
// as diligence.
func TestStrictQuery_NotFoundResultAttractsNoNotice(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, c := buildAbstentionFixture(t, javaAbstentionFiles)

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: "no_such_symbol_xyz",
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if len(env.Limitations) != 0 {
		t.Errorf("a not-found result fabricated limitations: %#v", env.Limitations)
	}
}

// TestTrustReport_CarriesAbstentionBlock is AC-4: the whole-repository
// document carries the roll-up keyed by the registrant that recorded it, with
// its scope note, beside the capability and boundary information.
func TestTrustReport_CarriesAbstentionBlock(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, _ := buildAbstentionFixture(t, javaAbstentionFiles)

	b, _, _, err := TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	var doc struct {
		Abstention AbstentionFacts `json:"abstention"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !doc.Abstention.Available {
		t.Fatalf("abstention block unavailable: %s", doc.Abstention.UnavailableReason)
	}
	if len(doc.Abstention.Languages) != 1 || doc.Abstention.Languages[0].Language != "java" {
		t.Fatalf("abstention languages = %#v, want exactly the java registrant", doc.Abstention.Languages)
	}
	java := doc.Abstention.Languages[0]
	if java.Total != 4 {
		t.Errorf("java total = %d, want 4 (1 var + 1 not-found + 2 external)", java.Total)
	}
	want := map[string]int{"java_lookup_not_found": 1, "java_receiver_external": 2, "java_var_inferred": 1}
	got := map[string]int{}
	for k, s := range java.Skips {
		got[s.Name] = s.Count
		if k > 0 && java.Skips[k-1].Name > s.Name {
			t.Errorf("skips are not canonically ordered: %#v", java.Skips)
		}
	}
	if len(got) != len(want) {
		t.Errorf("skips = %#v, want %#v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("skip %s = %d, want %d", name, got[name], n)
		}
	}
	if !strings.Contains(doc.Abstention.Scope, "repository-global") {
		t.Errorf("abstention block omits its scope note: %q", doc.Abstention.Scope)
	}
}

// TestTrustReport_AbstentionControlIsAvailableAndEmpty is the control for the
// report block, and it pins the distinction the whole fail-closed design turns
// on: a Go-only repository reports available=true with NO languages — read,
// and holding nothing — while an unreadable one reports available=false with a
// stated reason. Collapsing those two is how a silent gap becomes an all-clear.
func TestTrustReport_AbstentionControlIsAvailableAndEmpty(t *testing.T) {
	root, dbPath, metaDir, _ := buildAbstentionFixture(t, goControlFiles)

	decode := func(opts TrustReportOptions) AbstentionFacts {
		t.Helper()
		b, _, _, err := TrustReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("TrustReport: %v", err)
		}
		var doc struct {
			Abstention AbstentionFacts `json:"abstention"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		return doc.Abstention
	}

	ok := decode(TrustReportOptions{Root: root, DBPath: dbPath, MetaDir: metaDir})
	if !ok.Available {
		t.Fatalf("Go-only repository reported abstention unavailable: %s", ok.UnavailableReason)
	}
	if len(ok.Languages) != 0 {
		t.Errorf("Go-only repository reported abstention languages: %#v", ok.Languages)
	}

	bad := decode(TrustReportOptions{Root: root, DBPath: filepath.Join(t.TempDir(), "absent.db"), MetaDir: metaDir})
	if bad.Available {
		t.Error("an unreadable store reported the abstention record as available")
	}
	if bad.UnavailableReason == "" {
		t.Error("unavailability carries no reason — a user cannot act on a bare false")
	}
	if bad.Languages == nil {
		t.Error("Languages must be [] and never null")
	}
}

// TestAbstention_PackageRollUpIsNotMultipliedByPackageCount pins the arithmetic
// trap this design walks past: the counters are repository-global per language,
// so every package row of one language carries the SAME numbers. Summing them
// per covered package would multiply a repo-global total by the number of
// packages a result happens to touch and publish the product as a skip count.
// The java fixture's result covers TWO java packages; the reported total must
// still be the repository's 4.
func TestAbstention_PackageRollUpIsNotMultipliedByPackageCount(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, _ := buildAbstentionFixture(t, javaAbstentionFiles)

	pa := readPackageAbstention(context.Background(), root, dbPath, metaDir, []string{"com/shop", "com/tax"}, "")
	if len(pa.packages) != 2 {
		t.Fatalf("fixture assumption broken: %d covered packages, want 2 (%#v)", len(pa.packages), pa.packages)
	}
	if pa.total != 4 {
		t.Errorf("total = %d over 2 covered packages, want the repository-global 4 (double counting)", pa.total)
	}
}

// TestTrustReport_RegistrantsDisambiguateAnEmptyAbstentionList is the Major-3
// pin, and it is the exact experiment that refuted this story's round-1 claim
// that `capabilities` already disambiguates an empty abstention list.
//
// The repository is indexed WITHOUT the JVM binder and read WITH the opt-in
// set, which is what a user gets after enabling the flag on an existing store.
// capabilities then reports java as typed-confirmed — it is derived from the
// READING process's registry and knows nothing about the generation — beside an
// abstention block with no languages. Those two together read as "the typed
// binder ran and found nothing to abstain from", over a generation the binder
// never touched. Registrants is the fact that separates them, and it comes from
// the pass, not from the reader.
func TestTrustReport_RegistrantsDisambiguateAnEmptyAbstentionList(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "") // indexed with NO semantic JVM registrant
	root, dbPath, metaDir, _ := buildAbstentionFixture(t, javaAbstentionFiles)

	t.Setenv(semantic.EnvJVM, "1") // ...and read by a process that has one
	b, _, _, err := TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	var doc struct {
		Capabilities []struct {
			Language string `json:"language"`
			Level    string `json:"level"`
		} `json:"capabilities"`
		Abstention AbstentionFacts `json:"abstention"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode report: %v", err)
	}

	// The trap, asserted rather than assumed: capabilities really does claim
	// java at the top level here. If this ever stops being true the test below
	// is no longer testing the ambiguity it was written for.
	claimed := ""
	for _, c := range doc.Capabilities {
		if c.Language == "java" {
			claimed = c.Level
		}
	}
	if claimed != "typed-confirmed" {
		t.Fatalf("fixture assumption broken: capabilities.java = %q, want typed-confirmed (the ambiguity under test)", claimed)
	}
	if !doc.Abstention.Available {
		t.Fatalf("abstention unavailable: %s", doc.Abstention.UnavailableReason)
	}
	if len(doc.Abstention.Languages) != 0 {
		t.Fatalf("a pass with no JVM registrant recorded abstention languages: %#v", doc.Abstention.Languages)
	}
	for _, l := range doc.Abstention.Registrants {
		if l == "java" {
			t.Errorf("registrants claim java for a generation indexed without the binder: %#v", doc.Abstention.Registrants)
		}
	}
	// And the positive half: with the binder on, the same repository's
	// registrants DO name java — so the assertion above is discriminating, not
	// a field that is always empty.
	root2, db2, meta2, _ := buildAbstentionFixture(t, javaAbstentionFiles)
	b2, _, _, err := TrustReport(context.Background(), TrustReportOptions{Root: root2, DBPath: db2, MetaDir: meta2})
	if err != nil {
		t.Fatalf("TrustReport (binder on): %v", err)
	}
	var doc2 struct {
		Abstention AbstentionFacts `json:"abstention"`
	}
	if err := json.Unmarshal(b2, &doc2); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	found := false
	for _, l := range doc2.Abstention.Registrants {
		if l == "java" {
			found = true
		}
	}
	if !found {
		t.Errorf("registrants with the binder ON = %#v, want java among them", doc2.Abstention.Registrants)
	}
}

// TestAbstention_UnrecordedGenerationIsNotAnAllClear is the Critical-2 pin at
// the surface, where the damage was done.
//
// A store whose generation carries no abstention provenance — every store
// indexed before this feature existed, and still every such store after a
// `graphi sync` migrates the sidecar without re-running the binders — must read
// as a NAMED unavailability on both surfaces. Round 1 gated availability on the
// sidecar's schema version instead, so the migration alone flipped those stores
// to "available, nothing skipped": a false all-clear over passes that had
// really abstained, reachable by the ordinary upgrade path.
//
// The pre-state is produced by deleting the provenance rows of a live
// generation, which is byte-for-byte the state a pre-feature pass leaves behind.
func TestAbstention_UnrecordedGenerationIsNotAnAllClear(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	root, dbPath, metaDir, c := buildAbstentionFixture(t, javaAbstentionFiles)
	target := nodeIDByName(t, dbPath, "tax.value")

	// Control first: while the generation IS recorded, both surfaces answer.
	if got, ok := findLimitation(strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target, Root: root, DBPath: dbPath, MetaDir: metaDir,
	}), "abstention"); !ok || strings.Contains(got, "UNAVAILABLE") {
		t.Fatalf("fixture assumption broken: the recorded generation does not report abstention: %q", got)
	}

	// Now reproduce a generation that predates the record: the counters and the
	// package rows survive, only the provenance is absent — exactly what a
	// migrated pre-feature store looks like.
	meta, err := sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db"))
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	if _, err := meta.Exec("DELETE FROM trust_skip_provenance"); err != nil {
		t.Fatalf("clear provenance: %v", err)
	}
	if err := meta.Close(); err != nil {
		t.Fatalf("close sidecar: %v", err)
	}

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target, Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	l, ok := findLimitation(env, "UNAVAILABLE")
	if !ok {
		t.Fatalf("an unrecorded generation produced no unavailability notice — silence here IS the false all-clear: %#v", env.Limitations)
	}
	if !strings.Contains(l, "rebuild") {
		t.Errorf("the notice does not tell the user how to record one: %s", l)
	}
	for _, forbidden := range []string{"java_var_inferred", "java_receiver_external"} {
		if strings.Contains(l, forbidden) {
			t.Errorf("counters were published for a generation with no record: %s", l)
		}
	}

	b, _, _, err := TrustReport(context.Background(), TrustReportOptions{Root: root, DBPath: dbPath, MetaDir: metaDir})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	var doc struct {
		Abstention AbstentionFacts `json:"abstention"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if doc.Abstention.Available {
		t.Errorf("the trust report reported an unrecorded generation as answered: %#v", doc.Abstention)
	}
	if !strings.Contains(doc.Abstention.UnavailableReason, "rebuild") {
		t.Errorf("unavailability reason does not name the remedy: %q", doc.Abstention.UnavailableReason)
	}
}

// TestStrictQuery_EmptyResultOverUnexaminedPackagesSaysSo covers the other way
// an abstention notice can be silent for the wrong reason: the result is empty
// and NO semantic registrant holds evidence for the packages it covers. Nothing
// there was ever bound, so nothing there could be recorded as abstained — and a
// quiet "no callers" over an unexamined package is the same laundered
// confidence AC-6 forbids for a filtered one.
func TestStrictQuery_EmptyResultOverUnexaminedPackagesSaysSo(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "") // no registrant claims the java packages
	root, dbPath, metaDir, c := buildAbstentionFixture(t, javaAbstentionFiles)
	target := nodeIDByName(t, dbPath, "shop.run") // no callers at all

	env := strictEnvelope(t, c, StrictQueryOptions{
		Operation: "callers", Symbol: target,
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if len(env.Result.Edges) != 0 {
		t.Fatalf("fixture assumption broken: expected an empty result, got %d edges", len(env.Result.Edges))
	}
	l, ok := findLimitation(env, "unexamined")
	if !ok {
		t.Fatalf("an empty result over packages no binder examined carried no caveat: %#v", env.Limitations)
	}
	if !strings.Contains(l, "com/shop") {
		t.Errorf("the caveat names no unexamined package: %s", l)
	}
	if !strings.Contains(l, "no semantic registrant ran") {
		t.Errorf("the caveat does not say who recorded the generation: %s", l)
	}
}

// TestResultPackages_DedupesAndSorts pins the key space the gate uses: the
// evidence rows are keyed by repo-relative unit directory ("." for the root),
// so the result's nodes must map to exactly that, deduplicated and ordered.
func TestResultPackages_DedupesAndSorts(t *testing.T) {
	res := query.Result{Nodes: []query.ResultNode{
		{ID: model.NodeId("a"), SourcePath: "com/shop/Shop.java"},
		{ID: model.NodeId("b"), SourcePath: "com/shop/Other.java"},
		{ID: model.NodeId("c"), SourcePath: "com/tax/Rate.java"},
		{ID: model.NodeId("d"), SourcePath: "main.go"},
		{ID: model.NodeId("e"), SourcePath: ""},
	}}
	got := resultPackages(res)
	want := []string{".", "com/shop", "com/tax"}
	if len(got) != len(want) {
		t.Fatalf("resultPackages = %#v, want %#v", got, want)
	}
	for k := range want {
		if got[k] != want[k] {
			t.Fatalf("resultPackages = %#v, want %#v", got, want)
		}
	}
}
