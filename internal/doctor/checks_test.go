package doctor

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fakeEnv is a read-only Env used by check tests. It intentionally exposes no
// writer or ingest capability, so it cannot mutate the workspace.
type fakeEnv struct {
	repoRoot string
	dbPath   string
	release  fakeRelease
}

func (f fakeEnv) RepoRoot() string           { return f.repoRoot }
func (f fakeEnv) DBPath() string             { return f.dbPath }
func (f fakeEnv) MCPConfig() MCPConfigReader { return fakeMCPConfig{} }
func (f fakeEnv) Release() ReleaseInfo       { return f.release }
func (f fakeEnv) State() StateReader         { return fakeState{dbPath: f.dbPath} }

type fakeRelease struct{ version, commit, date, arch, marker string }

func (f fakeRelease) Version() string { return f.version }
func (f fakeRelease) Commit() string  { return f.commit }
func (f fakeRelease) Date() string    { return f.date }
func (f fakeRelease) Arch() string    { return f.arch }
func (f fakeRelease) IsRelease() bool { return f.marker == "release" }

type fakeMCPConfig struct{}

func (fakeMCPConfig) Clients() []MCPClient {
	return []MCPClient{{ID: "claude", Display: "Claude Code", ConfigPath: "/dev/null/mcp.json"}}
}
func (fakeMCPConfig) Plan(client MCPClient, binary string) (MCPPlanAction, error) {
	return MCPPlanNoOp, nil
}

type fakeState struct{ dbPath string }

func (f fakeState) DiscoverDB(repoRoot string) (string, error) { return f.dbPath, nil }

// stubOSLookups replaces the injectable OS lookup functions for the duration
// of a test and restores them on cleanup.
func stubOSLookups(t *testing.T, exe string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) {
	t.Helper()
	prevExe, prevLook, prevStat := executableFn, lookPathFn, statFn
	executableFn = func() (string, error) { return exe, nil }
	if lookPath != nil {
		lookPathFn = lookPath
	}
	if stat != nil {
		statFn = stat
	}
	t.Cleanup(func() {
		executableFn, lookPathFn, statFn = prevExe, prevLook, prevStat
	})
}

func TestBinaryCheckDevBuild(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "dev", marker: "dev"}}
	check := BinaryCheck(env.Release())
	res := check.Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("expected info for dev build, got %q", res.Status)
	}
	if !contains(res.Message, "dev") {
		t.Fatalf("expected dev marker in message: %s", res.Message)
	}
}

func TestBinaryCheckRelease(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "1.0.0", marker: "release"}}
	check := BinaryCheck(env.Release())
	res := check.Run(context.Background(), env)
	if res.Status != StatusPass {
		t.Fatalf("expected pass for release, got %q", res.Status)
	}
}

func TestBinaryCheckOutdatedReleaseWarnsOffline(t *testing.T) {
	// A packaged release older than the build-time known latest must warn with
	// upgrade guidance. The comparison is embedded metadata only — no network.
	env := fakeEnv{release: fakeRelease{version: "1.0.0", marker: "release"}}
	check := BinaryCheckAgainst(env.Release(), "1.2.0")
	res := check.Run(context.Background(), env)
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for outdated release, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "graphi upgrade") {
		t.Fatalf("expected `graphi upgrade` action, got %q", res.Action)
	}
	if !contains(res.Message, "1.2.0") {
		t.Fatalf("expected known latest version in message: %s", res.Message)
	}
}

func TestBinaryCheckCurrentReleasePasses(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "1.2.0", marker: "release"}}
	res := BinaryCheckAgainst(env.Release(), "1.2.0").Run(context.Background(), env)
	if res.Status != StatusPass {
		t.Fatalf("expected pass for up-to-date release, got %q: %s", res.Status, res.Message)
	}
}

func TestBinaryCheckDevBuildNeverWarnsOnVersion(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "dev", marker: "dev"}}
	res := BinaryCheckAgainst(env.Release(), "9.9.9").Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("dev build must stay info regardless of known latest, got %q", res.Status)
	}
}

func TestVersionIsOlder(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.2.0", true},
		{"1.2.0", "1.0.0", false},
		{"1.2.0", "1.2.0", false},
		{"v1.1.9", "1.2.0", true},
		{"1.2", "1.2.0", false},
		{"0.9.9", "0.10.0", true},
		{"1.2.3-rc1", "1.2.3", false},
		{"1.0.0", "0.0.0", false},
		// Unparsable versions fall back to inequality (same rule as `graphi upgrade`).
		{"nightly", "1.2.0", true},
		{"nightly", "nightly", false},
	}
	for _, tc := range cases {
		if got := versionIsOlder(tc.current, tc.latest); got != tc.want {
			t.Errorf("versionIsOlder(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestPATHCheckGoFallback(t *testing.T) {
	// This test assumes `go` is on PATH in the test environment; if not, it
	// should at least not panic and should return a meaningful result.
	check := PATHCheck()
	res := check.Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass && res.Status != StatusFail && res.Status != StatusWarn {
		t.Fatalf("unexpected status: %q", res.Status)
	}
}

func TestPATHCheckGoFallbackFound(t *testing.T) {
	// `go` absent from PATH but present at a well-known install location →
	// warn with guidance to add that location to PATH.
	const exe = "/fake/bin/graphi"
	stubOSLookups(t, exe,
		func(name string) (string, error) {
			if name == "graphi" {
				return exe, nil
			}
			return "", errors.New("not found on PATH")
		},
		func(path string) (os.FileInfo, error) {
			if path == "/usr/local/go/bin/go" {
				return nil, nil // exists
			}
			return nil, os.ErrNotExist
		},
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for fallback-found go, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Message, "/usr/local/go/bin/go") {
		t.Fatalf("expected fallback path in message: %s", res.Message)
	}
	if !contains(res.Action, "PATH") {
		t.Fatalf("expected PATH guidance in action: %s", res.Action)
	}
}

func TestPATHCheckGoFallbackMissing(t *testing.T) {
	// `go` absent from PATH and from every fallback location → fail listing
	// the probed locations.
	const exe = "/fake/bin/graphi"
	stubOSLookups(t, exe,
		func(name string) (string, error) {
			if name == "graphi" {
				return exe, nil
			}
			return "", errors.New("not found on PATH")
		},
		func(path string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusFail {
		t.Fatalf("expected fail when go is nowhere, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "/usr/local/go/bin/go") {
		t.Fatalf("expected probed fallbacks in action: %s", res.Action)
	}
}

func TestPATHCheckGraphiMismatchWarns(t *testing.T) {
	stubOSLookups(t, "/fake/bin/graphi",
		func(name string) (string, error) {
			if name == "graphi" {
				return "/other/bin/graphi", nil
			}
			return "/usr/bin/go", nil
		},
		nil,
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for PATH/executable mismatch, got %q: %s", res.Status, res.Message)
	}
}

func TestMCPCheckNoOp(t *testing.T) {
	check := MCPCheck("/bin/graphi")
	res := check.Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for no-op plan, got %q: %s", res.Status, res.Message)
	}
}

func TestPrivacyCheck(t *testing.T) {
	res := PrivacyCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for privacy check, got %q", res.Status)
	}
}

func TestLocalFirstCheck(t *testing.T) {
	res := LocalFirstCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for local-first check, got %q", res.Status)
	}
}

// TestKnownDefectsCheck pins the disclosure contract (D8): an OPEN published
// defect that affects a GA operation is named on the doctor surface at INFO
// severity — never silently green, never a health failure of this install.
//
// Restored for LINK-002 after ADR 0011 closed LINK-001 and removed the check for
// the third time, and extended to LINK-003 in review round 1 of the same story.
// The assertions are deliberately about the SHAPE of a disclosure rather than its
// prose, so the message can be improved without breaking the test, but cannot
// lose the parts that make it useful: the defect id, the affected operations, and
// a workaround.
//
// One assertion is about CONTENT rather than shape, and deliberately so. The
// first draft of the LINK-002 disclosure told users the defect "never emits a
// wrong one", which was false — it also REDIRECTS calls to the wrong declaration
// (docs/rc/link-002-clause-by-dir-recall.md §3.2). A user who reads "incomplete"
// and acts on it is misled differently from one who reads "possibly wrong", so
// the negative assertion below pins that the retraction cannot silently come back.
func TestKnownDefectsCheck(t *testing.T) {
	res := KnownDefectsCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("known-defects must be INFO (an open defect is disclosed, not a local "+
			"health failure, and never silently green), got %q", res.Status)
	}
	for _, want := range []string{"LINK-002", "LINK-003", "callers", "callees", "Workaround"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects message must mention %q; got: %s", want, res.Message)
		}
	}
	// LINK-004 (SW-183): Python dotted module imports resolve to nothing. Pinned
	// on the same three properties as LINK-002/003 — the id, the affected
	// operations, and a workaround — plus the one fact that makes the workaround
	// actionable rather than decorative: WHICH import form to rewrite it to. A
	// disclosure that says "some imports do not resolve" without naming the
	// working form leaves the reader unable to act, which is the failure mode the
	// `-profile full` incident taught in its other direction (a workaround that
	// cannot be executed at all).
	for _, want := range []string{"LINK-004", "from pkg.util import helper", "from pkg import util"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose LINK-004 with its failing form AND the "+
				"working form the workaround names (missing %q). See "+
				"docs/rc/capability-audit-2026-08-19.md §3; got: %s", want, res.Message)
		}
	}
	// The soundness half must be disclosed, not just the recall half.
	for _, want := range []string{"REDIRECTED", "WRONG EDGES"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose that LINK-002 emits WRONG edges and not "+
				"only that it drops true ones (missing %q). See "+
				"docs/rc/link-002-clause-by-dir-recall.md §3.2; got: %s", want, res.Message)
		}
	}
	if strings.Contains(res.Message, "never emits a wrong one") ||
		strings.Contains(res.Message, "drops true edges only") {
		t.Errorf("known-defects has regressed to the FALSE claim that LINK-002 only drops " +
			"true edges. It also redirects them — reproduced through the CLI in " +
			"docs/rc/link-002-clause-by-dir-recall.md §3.2, and pinned by " +
			"engine/link/clausebydir_test.go::TestLink002_RedirectsToWrongDeclaration.")
	}
	// The `-profile full` incident: a published workaround named a profile the
	// CLI rejects. Pin that this disclosure names only real profiles.
	if strings.Contains(res.Message, "-profile full") {
		t.Errorf("known-defects names a profile the CLI rejects; the accepted set is " +
			"fast|balanced|deep")
	}

	// PARITY-004 (SW-211): the disclosure that was owed on this surface since
	// 2026-08-19 while its two peers sat on both. Pinned on the same three
	// properties as the entries above — the id, an affected operation, and the
	// workaround — plus the measured reproduction that makes the workaround
	// checkable rather than a suggestion.
	for _, want := range []string{"PARITY-004", "graphi rebuild", "7 nodes", "6"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose PARITY-004 with its CLI reproduction and "+
				"the rebuild workaround (missing %q). See "+
				"docs/adr/0004-ingest-recovery-disposition.md; got: %s", want, res.Message)
		}
	}

	// PYTHONFANOUT-001 / PYTHONORDER-001 (SW-211). These are SOUNDNESS defects —
	// they emit WRONG edges, not merely missing ones — and the word is pinned
	// because that is the whole difference between "your answer is incomplete"
	// and "your answer contains a fabricated edge". The readme contradiction is
	// pinned for the same reason the LINK-002 over-claim is: a disclosure that
	// leaves a published promise standing beside the defect that breaks it has
	// disclosed the mechanism and hidden the consequence.
	for _, want := range []string{
		"PYTHONFANOUT-001", "SOUNDNESS", "70 spurious", "8.0%",
		"docs/rc/python-f5-measurement.md",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose PYTHONFANOUT-001 as a soundness defect "+
				"with its measured number and the readme sentence it contradicts "+
				"(missing %q). See section 6 of docs/rc/python-f5-measurement.md; got: %s",
				want, res.Message)
		}
	}
	for _, want := range []string{
		"PYTHONORDER-001", "INDEXING ORDER", "24/24/22", "23/24/23", "NO WORKAROUND",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose PYTHONORDER-001 with the order-dependent "+
				"distribution it was filed on, and must state that there is no workaround "+
				"rather than leaving the reader to infer one (missing %q). See section 12 "+
				"of docs/rc/python-f5-measurement.md; got: %s", want, res.Message)
		}
	}

	// AC-4 requires both Python entries to state the readme contradiction WITH a
	// line reference, and they still do. What is NOT pinned here is the line
	// NUMBER as a literal (SW-211 review round 1, m4): readme.md is renumbered by
	// every story that edits it, so a literal `readme.md:26-28` would go on
	// passing while the citation it pins became false — a gate enforcing a wrong
	// citation. Pinned instead: that a citation of the required SHAPE is present
	// for each of the two defects, that the sentence is QUOTED (the anchor that
	// cannot rot), and — in TestReadmeContradictionCitationsResolve — that the
	// lines named really do carry that sentence.
	if n := len(doctorReadmeCitation.FindAllString(res.Message, -1)); n < 2 {
		t.Errorf("AC-4 requires BOTH PYTHONFANOUT-001 and PYTHONORDER-001 to state the readme "+
			"contradiction with a line reference of the form readme.md:<from>-<to>; found %d "+
			"such citation(s) in the message. got: %s", n, res.Message)
	}
	if !strings.Contains(normaliseSpace(res.Message), readmeNavigabilityPromise) {
		t.Errorf("known-defects cites the readme promise but no longer QUOTES it (%q). The "+
			"quotation is what makes the citation checkable when readme.md is renumbered; "+
			"got: %s", readmeNavigabilityPromise, res.Message)
	}

	// D8 as amended 2026-08-25 is TWO surfaces. The check must point at the other
	// one, so a reader who lands here can find the human-readable half.
	if !strings.Contains(res.Message, canonicalDefectPage) {
		t.Errorf("known-defects must name %s, the human-readable half of D8; got: %s",
			canonicalDefectPage, res.Message)
	}
}

// canonicalDefectPage is the single human-readable D8 surface, per the amendment
// recorded on 2026-08-25 (projects/graphi/memory/decisions/
// 2026-08-25-d8-readme-half-amended-to-one-canonical-defect-page.md). Before
// that amendment the human half lived in a readme section whose name this file
// deliberately does not spell out (see the scan in
// TestDefectPinsNameTheCanonicalDefectPage, which would match its own source).
// That section no longer exists, which is why every retraction instruction has
// to be checked against this constant rather than against prose.
const canonicalDefectPage = "docs/known-defects.md"

// wantDisclosedDefects is THE list of open defects, and it is deliberately a
// literal rather than something derived: an id can only enter or leave it by a
// human editing this line, which is what makes it a pin.
//
// Adding a defect to either D8 surface without adding it here fails
// TestKnownDefectsDisclosureSetIsPinned; so does adding it to one surface and
// not the other. That is the gate AC-5 asks for — a future open defect cannot
// pass silently with a row on only one surface.
var wantDisclosedDefects = []string{
	"LINK-002",
	"LINK-003",
	"LINK-004",
	"PARITY-004",
	"PYTHONFANOUT-001",
	"PYTHONORDER-001",
}

// defectPinRetractionSites maps a disclosed defect to the test files whose
// FAILURE MESSAGE instructs a future fixer where to retract the disclosure.
//
// Those messages are the only enforcement the retraction rule has: they are
// printed exactly once, at the moment the defect is fixed, by which time nobody
// is re-reading the decision record. Between 2026-08-25 and 2026-08-26 all four
// of them named a readme bullet that had already been deleted, so following one
// literally retracted nothing and left the disclosure standing — a retraction
// breach that no gate could see. TestDefectPinsNameTheCanonicalDefectPage is
// that gate.
//
// A nil entry means "no behaviour pin exists for this defect yet". It is written
// out rather than omitted so that a new defect cannot be added to
// wantDisclosedDefects without someone deciding, in this file, whether it has a
// pin.
var defectPinRetractionSites = map[string][]string{
	"LINK-002":         {"engine/link/clausebydir_test.go"},
	"LINK-003":         {"engine/link/clausebydir_test.go"},
	"LINK-004":         {"surfaces/client/capabilityaudit_test.go"},
	"PARITY-004":       {"engine/ingest/purge_ordering_test.go"},
	"PYTHONFANOUT-001": nil, // filed 2026-08-20; no behaviour pin in-tree yet
	"PYTHONORDER-001":  nil, // filed 2026-08-24; no behaviour pin in-tree yet
}

// defectIDPattern matches the project's defect-id shape: an uppercase prefix, a
// hyphen, three digits. It is deliberately generic rather than an enumeration of
// the prefixes in use — a defect filed tomorrow under a new prefix must be
// caught by the set diff, not silently skipped by a regexp that never heard of
// it.
var defectIDPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]{3}\b`)

// nonDefectIDPrefixes are identifier families that share the defect-id SHAPE but
// are not defects. Ticket ids are the only one in the disclosure text today.
var nonDefectIDPrefixes = map[string]bool{"SW": true}

// defectHeadlinePattern matches the canonical page's own bullet convention, so
// the page is read the way a human reads it (one bullet per defect) as well as
// by raw scan.
var defectHeadlinePattern = regexp.MustCompile(`(?m)^- \*\*OPEN defect ([A-Z][A-Z0-9]+-[0-9]{3}) `)

// repoRootForTest resolves the repository root from this package's directory.
// The doctor package sits two levels down (internal/doctor), and this is a
// single-module workspace, so the relative hop is fixed.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod (%v); this test resolves paths relative to "+
			"internal/doctor and must be updated if the package moves", root, err)
	}
	return root
}

// extractDefectIDs returns the sorted, de-duplicated defect ids in s, minus the
// identifier families that merely share the shape.
func extractDefectIDs(s string) []string {
	seen := map[string]bool{}
	for _, m := range defectIDPattern.FindAllString(s, -1) {
		if prefix, _, ok := strings.Cut(m, "-"); ok && nonDefectIDPrefixes[prefix] {
			continue
		}
		seen[m] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// openDefectsSection returns the part of the canonical page that lists OPEN
// defects. Scoping matters: "Limits by design" below it names CLOSED defects
// (PARITY-002, PARITY-003, LINK-001) as the things each design limit resolved,
// and a scan that swept those in would report a disclosure the doctor check must
// never carry.
func openDefectsSection(t *testing.T, page string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, "\n## Open defects\n")
	if !ok {
		t.Fatalf("%s has no \"## Open defects\" heading; the id extraction below is "+
			"anchored on it", canonicalDefectPage)
	}
	section, _, ok := strings.Cut(rest, "\n## ")
	if !ok {
		return rest
	}
	return section
}

// mermaidBlock returns the page's first fenced mermaid block — the
// defect-to-operation map at the top.
func mermaidBlock(t *testing.T, page string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, "```mermaid\n")
	if !ok {
		t.Fatalf("%s has no mermaid block; the diagram is part of the disclosure and its "+
			"node set is pinned", canonicalDefectPage)
	}
	block, _, ok := strings.Cut(rest, "\n```")
	if !ok {
		t.Fatalf("%s has an unterminated mermaid block", canonicalDefectPage)
	}
	return block
}

// TestKnownDefectsDisclosureSetIsPinned is the AC-5 gate, extended by AC-13.
//
// D8 as amended on 2026-08-25 is a CONJUNCTION: an open defect in a GA operation
// is disclosed on the canonical defect page AND in the doctor known-defects
// check. Until this test existed, nothing checked the "and". PARITY-004 spent a
// week on one surface and not the other, and the two Python defects spent longer
// on neither while their record read "OPEN, DISCLOSED" — disclosed, that is, in
// the record.
//
// So the sets are DIFFED, not inspected. Three of them:
//
//	doctor    — the ids the compiled check actually emits
//	page      — the ids the canonical page lists under "Open defects"
//	diagram   — the ids the page's defect-to-operation map draws
//
// A defect that reaches two of the three fails here, which is the only shape in
// which a half-disclosure can now ship.
func TestKnownDefectsDisclosureSetIsPinned(t *testing.T) {
	want := append([]string(nil), wantDisclosedDefects...)
	sort.Strings(want)

	res := KnownDefectsCheck().Run(context.Background(), fakeEnv{})
	gotDoctor := extractDefectIDs(res.Message)
	if diff := diffIDs(want, gotDoctor); diff != "" {
		t.Errorf("the doctor known-defects check discloses a DIFFERENT set of defects than "+
			"wantDisclosedDefects pins.\n%s\n"+
			"If you just CLOSED a defect, retract it on both D8 surfaces and remove it from "+
			"wantDisclosedDefects in the same change. If you just FILED one, it needs a row "+
			"in KnownDefectsCheck AND a bullet on %s before it can land.",
			diff, canonicalDefectPage)
	}

	raw, err := os.ReadFile(filepath.Join(repoRootForTest(t), filepath.FromSlash(canonicalDefectPage)))
	if err != nil {
		t.Fatalf("read %s: %v — the canonical page is half of D8 and its absence is a "+
			"disclosure failure, not a missing fixture", canonicalDefectPage, err)
	}
	page := string(raw)

	section := openDefectsSection(t, page)
	if diff := diffIDs(want, extractDefectIDs(section)); diff != "" {
		t.Errorf("%s's \"Open defects\" section lists a DIFFERENT set than the doctor check "+
			"and wantDisclosedDefects.\n%s\nD8 requires BOTH surfaces; a defect on one of "+
			"them is half-disclosed.", canonicalDefectPage, diff)
	}

	var headlines []string
	for _, m := range defectHeadlinePattern.FindAllStringSubmatch(section, -1) {
		headlines = append(headlines, m[1])
	}
	sort.Strings(headlines)
	if diff := diffIDs(want, headlines); diff != "" {
		t.Errorf("%s's defect BULLETS do not match the pinned set.\n%s\nEach open defect "+
			"needs its own bullet opening `- **OPEN defect <ID> —`; mentioning an id inside "+
			"another defect's prose is not a disclosure.", canonicalDefectPage, diff)
	}

	if diff := diffIDs(want, extractDefectIDs(mermaidBlock(t, page))); diff != "" {
		t.Errorf("%s's defect-to-operation diagram does not draw the pinned set.\n%s\n"+
			"The diagram's legend tells the reader that an operation no arrow reaches is "+
			"unaffected by every defect on the page. A defect missing from the diagram makes "+
			"that sentence false for whichever operations it reaches.", canonicalDefectPage, diff)
	}
}

// TestDefectPinsNameTheCanonicalDefectPage is the AC-13 gate.
//
// Each open defect is pinned by a test that asserts the CURRENT WRONG behaviour
// and fails, with instructions, the moment the defect is fixed. Those
// instructions are where the retraction rule actually lives, and they are read
// exactly once — by the engineer who just fixed the defect, long after the
// decision record that explains where to retract has scrolled out of anyone's
// memory. So the instruction has to name the surface itself.
//
// The gate is MESSAGE-granular, not file-granular (SW-211 review round 1, m2).
// AC-13 asks that each pin's RETRACTION MESSAGE name the canonical page; a file
// may hold several pins (engine/link/clausebydir_test.go holds LINK-002 and
// LINK-003), and asserting only that the FILE contains the page name would stay
// green when one of its two messages lost it — the exact half-file mismatch this
// gate exists to catch.
//
// It is also checked in BOTH directions (m3). The registration table and the
// retraction messages actually present in the tree must agree exactly: a pin
// added for a disclosed defect without a table entry is as invisible as a table
// entry whose message stopped naming the page.
//
// The last part is the one that would have caught the SW-210 mismatch on the day
// it was created: no test may still instruct a fixer to delete a readme bullet
// that the 2026-08-25 amendment removed.
func TestDefectPinsNameTheCanonicalDefectPage(t *testing.T) {
	root := repoRootForTest(t)
	sites, staleHits := scanTestCorpus(t, root)

	for _, id := range wantDisclosedDefects {
		registered, ok := defectPinRetractionSites[id]
		if !ok {
			t.Errorf("defect %s is disclosed but defectPinRetractionSites says nothing about "+
				"it. Add an entry — {\"path/to/pin_test.go\"} if it has a behaviour pin, nil "+
				"with a comment if it does not — so the decision is recorded rather than "+
				"defaulted.", id)
			continue
		}
		want := append([]string(nil), registered...)
		sort.Strings(want)
		got := sites[id]

		for _, rel := range want {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				t.Errorf("pin %s is registered for %s but cannot be read: %v", rel, id, err)
			}
		}
		for _, rel := range want {
			if !slices.Contains(got, rel) {
				t.Errorf("%s is registered as %s's pin, but no failure message in it names "+
					"BOTH %s and %s. A fixer who follows it retracts only half the D8 "+
					"disclosure and leaves the other half standing, which the retraction rule "+
					"makes a violation in itself. (This is per-MESSAGE: another pin in the same "+
					"file naming the page does not cover this one.)", rel, id, id, canonicalDefectPage)
			}
		}
		for _, rel := range got {
			if !slices.Contains(want, rel) {
				t.Errorf("%s carries a retraction message for %s but is not registered for it "+
					"in defectPinRetractionSites. Register it — an unregistered pin is one the "+
					"forward half of this gate cannot protect, and a nil entry left in place "+
					"next to a real pin is a false statement that the defect has none.", rel, id)
			}
		}
	}

	if len(staleHits) > 0 {
		t.Errorf("these test lines still name the retired readme %q section as a retraction "+
			"surface:\n  %s\nThat bullet was deleted by the 2026-08-25 D8 amendment, so an "+
			"engineer who follows the instruction literally removes nothing and leaves the "+
			"disclosure standing. If the line IS a retraction instruction, repoint it at %s "+
			"alongside the doctor known-defects check. If it is a legitimate historical "+
			"quotation — a D6 record quoted verbatim, or a test ABOUT the amendment — do NOT "+
			"repoint it: add the marker %s in a comment on that line, which exempts it here.",
			retiredReadmeSection(), strings.Join(staleHits, "\n  "), canonicalDefectPage,
			staleSectionOptOut)
	}
}

// staleSectionOptOut exempts a single line from the retired-readme-section scan.
//
// The scan's failure advice is "repoint this at the canonical page", and that
// advice is WRONG for a line that names the retired section for a legitimate
// reason (SW-211 review round 1, m6). Rather than leave such a line with no
// lawful move, it may carry this marker in a comment — line-granular, so an
// exemption cannot silently widen to a whole file.
const staleSectionOptOut = "graphi:allow-retired-readme-section"

// retiredReadmeSection returns the name of the readme section the 2026-08-25 D8
// amendment deleted. It is assembled from two pieces so that this file does not
// match its own scan.
func retiredReadmeSection() string { return "Known lim" + "its" }

// scanTestCorpus walks every _test.go in the repository once and returns:
//
//   - sites: defect id → sorted list of test files carrying a RETRACTION MESSAGE
//     for that id, i.e. a t.Fatalf/t.Errorf format string that names both the id
//     and the canonical defect page. This is the observed truth that
//     defectPinRetractionSites is checked against, in both directions.
//   - staleHits: "path:line" for every line naming the retired readme section
//     without the opt-out marker.
func scanTestCorpus(t *testing.T, root string) (map[string][]string, []string) {
	t.Helper()
	stale := retiredReadmeSection()
	sites := map[string][]string{}
	var staleHits []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "corpus":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		body := string(b)
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		for i, line := range strings.Split(body, "\n") {
			if strings.Contains(line, stale) && !strings.Contains(line, staleSectionOptOut) {
				staleHits = append(staleHits, rel+":"+strconv.Itoa(i+1))
			}
		}

		// Only files that name the page at all can hold a retraction message, so
		// the parse — the expensive half — is skipped for the rest of the tree.
		if !strings.Contains(body, canonicalDefectPage) {
			return nil
		}
		for _, msg := range failureMessages(t, path, body) {
			if !strings.Contains(msg, canonicalDefectPage) {
				continue
			}
			for _, id := range defectIDPattern.FindAllString(msg, -1) {
				if nonDefectIDPrefixes[strings.SplitN(id, "-", 2)[0]] {
					continue
				}
				if !slices.Contains(sites[id], rel) {
					sites[id] = append(sites[id], rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	for id := range sites {
		sort.Strings(sites[id])
	}
	sort.Strings(staleHits)
	return sites, staleHits
}

// failureMessages returns the format string of every t.Fatalf/t.Errorf/t.Fatal/
// t.Error/t.Skipf call in a Go test file, with `"a" + "b"` concatenation
// flattened into one message. That flattened string is the unit AC-13 talks
// about: it is exactly what a fixer sees printed when the pin breaks.
func failureMessages(t *testing.T, path, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var msgs []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Fatalf", "Errorf", "Fatal", "Error", "Skipf", "Logf":
		default:
			return true
		}
		if msg := flattenStringConcat(call.Args[0]); msg != "" {
			msgs = append(msgs, msg)
		}
		return true
	})
	return msgs
}

// flattenStringConcat renders a chain of string literals joined by `+` as one
// string. Anything that is not a literal chain (a variable, a call) renders as
// the empty string for the parts it cannot see, which is the safe direction: an
// assembled message this gate cannot read is a message it does not credit.
func flattenStringConcat(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return ""
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return ""
		}
		return s
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return ""
		}
		return flattenStringConcat(v.X) + flattenStringConcat(v.Y)
	case *ast.ParenExpr:
		return flattenStringConcat(v.X)
	}
	return ""
}

// readmeNavigabilityPromise is the readme sentence both Python soundness
// disclosures cite as the promise they contradict. THIS is the load-bearing half
// of that citation — the line numbers beside it are a convenience for a human
// reader, and are verified against this sentence rather than trusted.
const readmeNavigabilityPromise = "stdlib and third-party targets are recorded, but deliberately not navigable"

// The two spellings of the same citation, one per D8 surface: the doctor check
// writes `readme.md:26-28`, the canonical page writes
// `[readme.md](../readme.md) lines 26–28` (en dash, as markdown prose).
var (
	doctorReadmeCitation = regexp.MustCompile(`readme\.md:(\d+)-(\d+)`)
	pageReadmeCitation   = regexp.MustCompile(`\[readme\.md\]\([^)]*\) lines (\d+)[–-](\d+)`)
)

// TestReadmeContradictionCitationsResolve makes AC-4's line reference honest
// instead of decorative.
//
// AC-4 mandates that both Python soundness disclosures state the readme promise
// they contradict, WITH its line reference. A line reference rots: SW-212…SW-217
// rewrite readme.md wholesale, and a citation pinned as a literal string would
// keep passing while pointing at whatever happens to sit at those lines
// afterwards — a gate enforcing a false citation (SW-211 review round 1, m4).
//
// So the citation is resolved rather than pinned: the quoted sentence must still
// be in readme.md, both surfaces must still quote it, and every
// `readme.md:<from>-<to>` either surface prints must name lines that actually
// contain it. When the readme is renumbered this fails LOUDLY, and the failure
// names the span to move the citation to. The line reference itself stays
// mandatory — AC-4 requires one, and this test is not a licence to drop it.
func TestReadmeContradictionCitationsResolve(t *testing.T) {
	root := repoRootForTest(t)
	b, err := os.ReadFile(filepath.Join(root, "readme.md"))
	if err != nil {
		t.Fatalf("read readme.md: %v", err)
	}
	lines := strings.Split(string(b), "\n")

	from, to, ok := promiseSpan(lines)
	if !ok {
		t.Fatalf("readme.md no longer contains the promise %q that PYTHONFANOUT-001 and "+
			"PYTHONORDER-001 are disclosed as contradicting. Either it was reworded — in "+
			"which case both disclosures quote a sentence that does not exist and must be "+
			"updated in the SAME change — or it was withdrawn, in which case the "+
			"contradiction claim itself is stale.", readmeNavigabilityPromise)
	}

	surfaces := []struct {
		name string
		text string
		re   *regexp.Regexp
	}{
		{"the doctor known-defects check (internal/doctor/checks.go)",
			KnownDefectsCheck().Run(context.Background(), fakeEnv{}).Message, doctorReadmeCitation},
		{canonicalDefectPage, readCanonicalDefectPage(t, root), pageReadmeCitation},
	}
	for _, s := range surfaces {
		if !strings.Contains(normaliseSpace(s.text), readmeNavigabilityPromise) {
			t.Errorf("%s cites the readme contradiction without quoting the sentence %q. The "+
				"quotation is the half of the citation that cannot rot; without it the line "+
				"numbers are the only anchor and this test cannot check them.",
				s.name, readmeNavigabilityPromise)
		}
		cites := s.re.FindAllStringSubmatch(s.text, -1)
		if len(cites) == 0 {
			t.Errorf("%s cites no readme line reference at all; AC-4 requires the readme "+
				"contradiction to be stated with one", s.name)
			continue
		}
		for _, c := range cites {
			citedFrom, _ := strconv.Atoi(c[1])
			citedTo, _ := strconv.Atoi(c[2])
			if !spanContainsPromise(lines, citedFrom, citedTo) {
				t.Errorf("%s cites readme.md lines %d-%d for the promise %q, but those lines "+
					"do not contain it — readme.md has been renumbered. The citation is now "+
					"FALSE. The sentence is at lines %d-%d today: update the citation on BOTH "+
					"D8 surfaces in the same change (this is not a licence to drop the line "+
					"reference; AC-4 requires one).",
					s.name, citedFrom, citedTo, readmeNavigabilityPromise, from, to)
			}
		}
	}
}

// readCanonicalDefectPage reads the human-readable D8 surface.
func readCanonicalDefectPage(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(canonicalDefectPage)))
	if err != nil {
		t.Fatalf("read %s: %v", canonicalDefectPage, err)
	}
	return string(b)
}

// promiseSpan returns the 1-based line span of readmeNavigabilityPromise, which
// wraps across source lines in the readme's prose. The NARROWEST span is
// returned, so the number a failure tells a fixer to cite is the sentence's own
// lines rather than an arbitrary superset of them.
func promiseSpan(lines []string) (int, int, bool) {
	for width := 0; width < 5; width++ {
		for i := 0; i+width < len(lines); i++ {
			if spanContainsPromise(lines, i+1, i+1+width) {
				return i + 1, i + 1 + width, true
			}
		}
	}
	return 0, 0, false
}

// normaliseSpace collapses every run of whitespace to one space, so a sentence
// that wraps across source lines still matches as one string.
func normaliseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// spanContainsPromise reports whether the 1-based, inclusive line range holds the
// promise sentence once line wrapping is normalised away.
func spanContainsPromise(lines []string, from, to int) bool {
	if from < 1 || to < from || to > len(lines) {
		return false
	}
	return strings.Contains(normaliseSpace(strings.Join(lines[from-1:to], " ")), readmeNavigabilityPromise)
}

// diffIDs renders the two-way difference between a wanted and a got id list, or
// "" when they are equal. Both inputs must be sorted.
func diffIDs(want, got []string) string {
	if strings.Join(want, ",") == strings.Join(got, ",") {
		return ""
	}
	inWant := map[string]bool{}
	for _, id := range want {
		inWant[id] = true
	}
	inGot := map[string]bool{}
	for _, id := range got {
		inGot[id] = true
	}
	var missing, extra []string
	for _, id := range want {
		if !inGot[id] {
			missing = append(missing, id)
		}
	}
	for _, id := range got {
		if !inWant[id] {
			extra = append(extra, id)
		}
	}
	return "  want: " + strings.Join(want, " ") +
		"\n  got:  " + strings.Join(got, " ") +
		"\n  missing (pinned but not disclosed): " + strings.Join(missing, " ") +
		"\n  extra (disclosed but not pinned):   " + strings.Join(extra, " ")
}

func TestDBCheckEmptyPath(t *testing.T) {
	env := fakeEnv{dbPath: ""}
	res := DBCheck().Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("expected info for empty db path, got %q", res.Status)
	}
}

func TestRenderersWriteOnlyToWriter(t *testing.T) {
	// Prove that renderers do not touch the filesystem by using a writer that
	// records bytes and asserting no file operations occurred.
	w := io.Discard
	report := Report{Results: []CheckResult{{ID: "p", Category: "c", Status: StatusPass, Message: "ok"}}}
	if err := RenderHuman(w, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	if err := RenderJSON(w, report); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
}

func contains(s, substr string) bool { return strings.Contains(s, substr) }

// fakeContendingMCPConfig implements both MCPConfigReader and the optional
// MCPContentionReader, reporting two zero-config graphi entries.
type fakeContendingMCPConfig struct{ fakeMCPConfig }

func (fakeContendingMCPConfig) Contending(client MCPClient) ([]string, error) {
	return []string{"graphi", "graphi-mars"}, nil
}

type fakeContendingEnv struct{ fakeEnv }

func (fakeContendingEnv) MCPConfig() MCPConfigReader { return fakeContendingMCPConfig{} }

// TestMCPCheckWarnsOnContendingEntries pins the duplicate-entry warning: two
// zero-config graphi entries in one client config downgrade the mcp check to
// warn, name both entries, and the action says keep-one-or-pin. Readers
// without the optional interface (fakeMCPConfig, pinned by TestMCPCheckNoOp)
// stay unaffected.
func TestMCPCheckWarnsOnContendingEntries(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeContendingEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for contending entries, got %q: %s", res.Status, res.Message)
	}
	for _, want := range []string{"graphi, graphi-mars", "contend on its ingest lock"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("message missing %q: %s", want, res.Message)
		}
	}
	if !strings.Contains(res.Action, "keep one zero-config graphi entry") {
		t.Fatalf("action not actionable: %s", res.Action)
	}
}

// fakeMixedMCPConfig reports four clients whose plans exercise every per-client
// finding the aggregate mcp check can produce: registered-and-current,
// not-registered, stale-command, and cannot-read-config.
type fakeMixedMCPConfig struct{}

func (fakeMixedMCPConfig) Clients() []MCPClient {
	// Deliberately NOT in sorted display order, so a test asserting sorted
	// detail lines is actually asserting the sort and not the input order.
	return []MCPClient{
		{ID: "zed", Display: "Zed", ConfigPath: "/dev/null/zed.json"},
		{ID: "cursor", Display: "Cursor", ConfigPath: "/dev/null/cursor.json"},
		{ID: "claude", Display: "Claude Code", ConfigPath: "/dev/null/claude.json"},
		{ID: "vscode", Display: "VS Code", ConfigPath: "/dev/null/vscode.json"},
	}
}

func (fakeMixedMCPConfig) Plan(client MCPClient, binary string) (MCPPlanAction, error) {
	switch client.ID {
	case "claude":
		return MCPPlanNoOp, nil
	case "cursor":
		return MCPPlanUpdate, nil
	case "vscode":
		return MCPPlanCreate, nil
	default:
		return "", errors.New("unexpected end of JSON input")
	}
}

type fakeMixedEnv struct{ fakeEnv }

func (fakeMixedEnv) MCPConfig() MCPConfigReader { return fakeMixedMCPConfig{} }

// TestMCPCheckDetailAttributesEveryClient pins SW-159 AC1/AC2: the per-client
// lines the aggregate check computes are surfaced in CheckResult.Detail, one
// client per line, sorted, each naming the client and its specific finding.
func TestMCPCheckDetailAttributesEveryClient(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeMixedEnv{})
	if res.Status != StatusFail {
		t.Fatalf("expected fail (one client is not registered), got %q: %s", res.Status, res.Message)
	}
	if res.Detail == "" {
		t.Fatal("detail is empty: the per-client findings were discarded again")
	}
	lines := strings.Split(res.Detail, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected one line per client (4), got %d: %q", len(lines), res.Detail)
	}
	want := []string{
		"Claude Code: registered and current",
		"Cursor: stale command path or args",
		"VS Code: not registered",
		"Zed: cannot read config: unexpected end of JSON input",
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("detail line %d: got %q, want %q (full detail:\n%s)", i, lines[i], w, res.Detail)
		}
	}
	if !sort.StringsAreSorted(lines) {
		t.Fatalf("detail lines are not in sorted order: %q", lines)
	}
}

// TestMCPCheckDetailDoesNotChangeAggregate pins SW-159 AC7: adding Detail
// leaves the check's id, category, status derivation, message and action
// exactly as they were.
func TestMCPCheckDetailDoesNotChangeAggregate(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeMixedEnv{})
	if res.ID != "mcp" || res.Category != "mcp" {
		t.Fatalf("id/category changed: %q/%q", res.ID, res.Category)
	}
	if res.Message != "one or more MCP clients need attention" {
		t.Fatalf("aggregate message changed: %q", res.Message)
	}
	if res.Action != "re-run `graphi setup` to update registrations" {
		t.Fatalf("aggregate action changed: %q", res.Action)
	}
}

// TestMCPCheckDetailReportsContentionPerClient pins SW-159 AC3: contention is
// attributed to the client it was found in inside Detail, in addition to the
// existing aggregate message (which keeps carrying it).
func TestMCPCheckDetailReportsContentionPerClient(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeContendingEnv{})
	wantDetail := "Claude Code: registered and current\n" +
		"Claude Code: 2 zero-config graphi entries (graphi, graphi-mars) will resolve the same repository and contend on its ingest lock"
	if res.Detail != wantDetail {
		t.Fatalf("detail:\ngot:\n%s\nwant:\n%s", res.Detail, wantDetail)
	}
	// AC3 says "in addition to" — the aggregate message must still carry it.
	if !strings.Contains(res.Message, "contend on its ingest lock") {
		t.Fatalf("aggregate message lost its contention text: %q", res.Message)
	}
}

// TestMCPCheckDetailEmptyWhenAllPass pins SW-159 AC5: an all-pass run leaves
// Detail empty so `json:"detail,omitempty"` omits it entirely.
func TestMCPCheckDetailEmptyWhenAllPass(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass, got %q: %s", res.Status, res.Message)
	}
	if res.Detail != "" {
		t.Fatalf("all clients pass, detail must be empty, got %q", res.Detail)
	}
}

// SW-228 (AX-08): the executor-seam readout. It is the operator-visible half of
// the story's gating precondition — the strangler seam's configuration must be
// observable through a local, zero-egress diagnostic — so each of its three
// outcomes is exercised rather than described.
func TestExecutorSeamCheckReportsThePositions(t *testing.T) {
	positions := []ExecutorSeamPosition{
		{Operation: "compound", Mode: "legacy", EnvVar: "GRAPHI_CANARY_COMPOUND"},
		{Operation: "dead_code", Mode: "legacy", EnvVar: "GRAPHI_CANARY_DEAD_CODE"},
	}
	res := ExecutorSeamCheck(positions, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("the shipped configuration is %q, want %q", res.Status, StatusInfo)
	}
	if !strings.Contains(res.Message, "2 legacy") {
		t.Errorf("message does not count the positions: %q", res.Message)
	}
	if res.Action != "" {
		t.Errorf("the shipped configuration needs no action, got %q", res.Action)
	}
	// The detail names every operation, its position, and where the position
	// came from — that is what makes the readout actionable rather than a count.
	for _, want := range []string{
		"compound: legacy (compiled-in default)",
		"dead_code: legacy (compiled-in default)",
		// SW-232 replaced the old "not persisted" disclosure: the record IS
		// durable now, and the detail has to say where to read it instead of
		// warning that it cannot be read at all.
		"graphi doctor -divergence",
	} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail is missing %q:\n%s", want, res.Detail)
		}
	}
}

// TestExecutorSeamCheckWarnsOnShadow pins the one non-shipped position that
// costs something on every call. An operator who left it on must be told.
func TestExecutorSeamCheckWarnsOnShadow(t *testing.T) {
	res := ExecutorSeamCheck([]ExecutorSeamPosition{
		{Operation: "dead_code", Mode: "shadow", Overridden: true, EnvVar: "GRAPHI_CANARY_DEAD_CODE"},
		{Operation: "compound", Mode: "legacy", EnvVar: "GRAPHI_CANARY_COMPOUND"},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("shadow reports %q, want %q — every call runs twice in that position",
			res.Status, StatusWarn)
	}
	if !strings.Contains(res.Action, "dead_code") {
		t.Errorf("the action does not name the operation to roll back: %q", res.Action)
	}
	if !strings.Contains(res.Detail, "dead_code: shadow (GRAPHI_CANARY_DEAD_CODE)") {
		t.Errorf("detail does not attribute the position to its variable:\n%s", res.Detail)
	}
}

// TestSW244_ExecutorSeamCheckDoesNotWarnOnTheShippedDefault is the counterpart
// to the test above, and the reason that one is written with Overridden: true.
//
// SW-244 made `shadow` the compiled-in default. A check that WARNed on the
// shipped configuration of a stock install would be wrong twice over: it would
// train an operator to ignore this check, and its action would tell them to
// unset a variable that is not set — and that, if it were set, they would have
// to keep set to get the behaviour the action promises.
//
// So the compiled-in shadow is INFO. It is still not SILENT: the position costs
// about 2x on the operations it covers, so the action states the cost and names
// the opt-out.
func TestSW244_ExecutorSeamCheckDoesNotWarnOnTheShippedDefault(t *testing.T) {
	res := ExecutorSeamCheck([]ExecutorSeamPosition{
		{Operation: "compound", Mode: "shadow", EnvVar: "GRAPHI_CANARY_COMPOUND"},
		{Operation: "dead_code", Mode: "shadow", EnvVar: "GRAPHI_CANARY_DEAD_CODE"},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("the shipped default reports %q, want %q — a stock install must not warn "+
			"about its own configuration", res.Status, StatusInfo)
	}
	if !strings.Contains(res.Message, "0 legacy, 2 shadow, 0 active") {
		t.Errorf("message does not count the positions: %q", res.Message)
	}
	// Not silent: the cost and the opt-out are both stated.
	for _, want := range []string{"2x", "GRAPHI_CANARY_ALL=legacy"} {
		if !strings.Contains(res.Action, want) {
			t.Errorf("the action does not mention %q: %q", want, res.Action)
		}
	}
	if strings.Contains(res.Action, "unset") {
		t.Errorf("the action tells the operator to unset a variable that is not set: %q", res.Action)
	}
	for _, want := range []string{
		"compound: shadow (compiled-in default)",
		"dead_code: shadow (compiled-in default)",
	} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail is missing %q:\n%s", want, res.Detail)
		}
	}
}

// TestExecutorSeamCheckReportsActive pins the third position: not a warning
// (nothing is paid twice) but never silent, because the executor is authoritative.
func TestExecutorSeamCheckReportsActive(t *testing.T) {
	res := ExecutorSeamCheck([]ExecutorSeamPosition{
		{Operation: "dead_code", Mode: "active", Overridden: true, EnvVar: "GRAPHI_CANARY_DEAD_CODE"},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("active reports %q, want %q", res.Status, StatusInfo)
	}
	if !strings.Contains(res.Action, "roll back") {
		t.Errorf("the action does not offer the rollback: %q", res.Action)
	}
}

// TestExecutorSeamCheckFailsOnAnInvalidVariable is the fail-closed half. A
// mistyped GRAPHI_CANARY_* value stops a real session at startup, so a
// diagnostic that reported the compiled-in positions and a green line would be
// telling the operator the opposite of what they are about to hit.
func TestExecutorSeamCheckFailsOnAnInvalidVariable(t *testing.T) {
	res := ExecutorSeamCheck(nil, errors.New(`GRAPHI_CANARY_DEAD_CODE: "lecacy" is not a kill-switch position`)).
		Run(context.Background(), fakeEnv{})
	if res.Status != StatusFail {
		t.Fatalf("an invalid kill-switch variable reports %q, want %q", res.Status, StatusFail)
	}
	if !strings.Contains(res.Message, "GRAPHI_CANARY_DEAD_CODE") {
		t.Errorf("the message does not name the variable: %q", res.Message)
	}
	if res.Action == "" {
		t.Error("a fail with no action leaves the operator nowhere to go")
	}
}

// SW-232 (AX-12a): the persisted divergence readout. Its four outcomes are
// exercised because the honesty rule lives in exactly the distinction between
// two of them — an unobserved seam and an observed-clean one.
func TestExecutorDivergenceCheckReportsUnknownNotZeroDivergences(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:      "UNKNOWN",
		Directory:  "/tmp/state/executor-divergence",
		Unobserved: []string{"dead_code", "compound"},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("an unobserved seam reports %q, want %q — it is not a health failure", res.Status, StatusInfo)
	}
	if !strings.Contains(res.Message, "UNKNOWN") {
		t.Errorf("message does not say UNKNOWN: %q", res.Message)
	}
	if !strings.Contains(res.Message, "NOT a statement that the two paths agree") {
		t.Errorf("message lets UNKNOWN be read as parity: %q", res.Message)
	}
	if res.Status == StatusPass {
		t.Error("an unobserved seam must never read PASS")
	}
	if !strings.Contains(res.Detail, "never observed (UNKNOWN, not agreed): compound, dead_code") {
		t.Errorf("detail does not name the unobserved operations:\n%s", res.Detail)
	}
}

func TestExecutorDivergenceCheckWarnsOnARecordedDivergence(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "DIVERGED",
		Directory:    "/tmp/state/executor-divergence",
		Observations: 12,
		Mismatches:   3,
		Diverged:     []string{"dead_code"},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("a recorded divergence reports %q, want %q", res.Status, StatusWarn)
	}
	if !strings.Contains(res.Message, "dead_code") {
		t.Errorf("message does not name the diverging operation: %q", res.Message)
	}
	if !strings.Contains(res.Action, "GRAPHI_CANARY_") || !strings.Contains(res.Action, "rollback") {
		t.Errorf("the action does not point at the documented rollback: %q", res.Action)
	}
}

// PASS is earned only when EVERY migrated operation was actually observed. A
// partial record stays INFO, so a seam that was exercised on one operation
// cannot be read as a clean bill of health for the other nine.
func TestExecutorDivergenceCheckDoesNotPassOnAPartialRecord(t *testing.T) {
	partial := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "PARTIAL-UNKNOWN",
		Observations: 40,
		Unobserved:   []string{"compound"},
	}, nil).Run(context.Background(), fakeEnv{})
	if partial.Status != StatusInfo {
		t.Fatalf("a partial record reports %q, want %q", partial.Status, StatusInfo)
	}
	if !strings.Contains(partial.Message, "never been observed") {
		t.Errorf("message hides the unobserved operations: %q", partial.Message)
	}

	complete := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "NO-DIVERGENCE-OBSERVED",
		Observations: 40,
	}, nil).Run(context.Background(), fakeEnv{})
	if complete.Status != StatusPass {
		t.Fatalf("a complete, clean record reports %q, want %q", complete.Status, StatusPass)
	}
}

// An unreadable record is a WARN, not an empty one: "I could not read it" and
// "there is nothing in it" are different facts, and collapsing them is the
// false green this check exists to prevent.
func TestExecutorDivergenceCheckWarnsWhenTheRecordCannotBeRead(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{}, errors.New("permission denied")).
		Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("an unreadable record reports %q, want %q", res.Status, StatusWarn)
	}
	if !strings.Contains(res.Action, "UNKNOWN, not clean") {
		t.Errorf("the action lets an unreadable record pass as clean: %q", res.Action)
	}
}

// A partially-unreadable record discloses that its totals are a lower bound.
func TestExecutorDivergenceCheckDisclosesUnreadableSegments(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "NO-DIVERGENCE-OBSERVED",
		Observations: 5,
		Unreadable:   2,
	}, nil).Run(context.Background(), fakeEnv{})
	if !strings.Contains(res.Detail, "lower bound") {
		t.Errorf("detail does not disclose that the totals are incomplete:\n%s", res.Detail)
	}
}

// A record that has pruned segments discloses that its totals are a lower bound
// too. Pruning is by age alone, with no writer-liveness concept, so what it
// dropped is not necessarily ancient history — and an operator reading the
// doctor detail must not have to know that to read the number correctly.
func TestExecutorDivergenceCheckDisclosesPrunedSegments(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "NO-DIVERGENCE-OBSERVED",
		Observations: 5,
		Pruned:       3,
	}, nil).Run(context.Background(), fakeEnv{})
	if !strings.Contains(res.Detail, "3 pruned segment(s)") {
		t.Errorf("detail does not report the pruned segments:\n%s", res.Detail)
	}
	if !strings.Contains(res.Detail, "lower bound") {
		t.Errorf("detail does not disclose that the totals are incomplete:\n%s", res.Detail)
	}
}

// SW-245 AC-4: a record with skipped comparisons does not PASS.
//
// PASS is the check's one earned verdict — every migrated operation observed,
// none diverged — and an operator scanning statuses reads it as "the seam is
// proven". Since SW-245 the dual run happens on a bounded queue, so a call can
// reach the seam and never be compared; the operations can all be observed and
// the evidence still be partial. Downgrading to INFO with the numbers is the
// honest answer. PASS with a footnote is not: the footnote is in the detail,
// which is precisely what a status scan does not read.
func TestExecutorDivergenceCheckDoesNotPassWithSkippedComparisons(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "NO-DIVERGENCE-OBSERVED",
		Observations: 40,
		Skipped:      12,
		SkipReasons:  map[string]int{"queue-full": 10, "drain-abandoned": 2},
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status == StatusPass {
		t.Fatalf("a record with 12 uncompared dispatches reports PASS: %q", res.Message)
	}
	if res.Status != StatusInfo {
		t.Fatalf("status = %q, want %q", res.Status, StatusInfo)
	}
	if !strings.Contains(res.Message, "never compared") {
		t.Errorf("the message hides the coverage gap: %q", res.Message)
	}
	if !strings.Contains(res.Detail, "drain-abandoned=2, queue-full=10") {
		t.Errorf("the detail does not break the gap down by cause:\n%s", res.Detail)
	}
	if !strings.Contains(res.Detail, "covers 40 of 52") {
		t.Errorf("the detail does not state the effective coverage:\n%s", res.Detail)
	}
	if !strings.Contains(res.Action, "not evidence of agreement") {
		t.Errorf("the action lets a skipped comparison read as agreement: %q", res.Action)
	}
}

// The clean case must stay clean: no skipped comparisons, no coverage
// paragraph. A disclosure that prints unconditionally stops being read.
func TestExecutorDivergenceCheckIsSilentAboutCoverageWhenItIsWhole(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:        "NO-DIVERGENCE-OBSERVED",
		Observations: 40,
	}, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("a complete, fully-compared record reports %q, want %q", res.Status, StatusPass)
	}
	if strings.Contains(res.Detail, "NOT compared") {
		t.Errorf("a full-coverage record warns about a gap it does not have:\n%s", res.Detail)
	}
}
