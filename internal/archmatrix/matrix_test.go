package archmatrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	return root
}

func loadLiveMatrix(t *testing.T) (Matrix, string) {
	t.Helper()
	root := testModuleRoot(t)
	m, err := Load(filepath.Join(root, filepath.FromSlash(MatrixYAMLPath)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m, root
}

// TestMatrix_CoversLiveClientContract is the drift guard the PRD asks for: every
// method of the live client has exactly one owning context, and every error
// sentinel is inventoried. Running it as an ordinary test means the standard test
// gate enforces it — no new CI workflow is needed.
func TestMatrix_CoversLiveClientContract(t *testing.T) {
	m, root := loadLiveMatrix(t)

	sentinels, err := LiveSentinels(root)
	if err != nil {
		t.Fatalf("LiveSentinels: %v", err)
	}
	if len(sentinels) == 0 {
		t.Fatal("no error sentinels found in surfaces/client; the source scan is looking in the wrong place")
	}

	report := Check(m, LiveMethods(), sentinels)
	if !report.Pass() {
		t.Errorf("migration matrix has drifted from the live client contract:\n%s", report.Format())
	}
}

// TestMatrix_DeclaredStubsMatchSource pins the compatibility-stub freeze: a new
// bare sentinel stub on any client implementation must be recorded in the matrix,
// so "0 new remote-client stubs" is checkable rather than asserted.
func TestMatrix_DeclaredStubsMatchSource(t *testing.T) {
	m, root := loadLiveMatrix(t)

	scan, err := ScanStubs(root)
	if err != nil {
		t.Fatalf("ScanStubs: %v", err)
	}
	if problems := CheckStubs(m, scan); len(problems) > 0 {
		t.Errorf("declared implementation statuses disagree with the source:\n  %s", strings.Join(problems, "\n  "))
	}

	// Direct is the reference implementation: if it were ever reduced to bare
	// stubs the whole matrix would read as "everything is unavailable" and the
	// check above would still pass, because it only compares declaration to code.
	if len(scan[ImplNameDirect]) > 0 {
		t.Errorf("Direct should implement every use case, but these are bare sentinel stubs: %v", scan[ImplNameDirect])
	}
	// The remote clients are expected to carry real stub debt today. If that count
	// hit zero, this scan would be silently matching nothing.
	if len(scan[ImplNameHTTP]) == 0 && len(scan[ImplNameDaemon]) == 0 {
		t.Error("stub scan found no compatibility stubs on either remote client; the detector is probably not matching")
	}
}

// TestMatrix_RenderedTableIsFresh keeps the generated document honest.
func TestMatrix_RenderedTableIsFresh(t *testing.T) {
	m, root := loadLiveMatrix(t)

	usage, err := ScanSurfaceUsage(root)
	if err != nil {
		t.Fatalf("ScanSurfaceUsage: %v", err)
	}
	refs, err := ScanSentinelRefs(root)
	if err != nil {
		t.Fatalf("ScanSentinelRefs: %v", err)
	}
	want := RenderMarkdown(m, usage, refs)

	current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(MatrixMDPath)))
	if err != nil {
		t.Fatalf("read %s: %v", MatrixMDPath, err)
	}
	if string(current) != want {
		t.Errorf("%s is stale — run `go run ./cmd/archmatrix -generate`", MatrixMDPath)
	}
}

// TestMatrix_RenderIsDeterministic guards the artifact against map-order leakage.
func TestMatrix_RenderIsDeterministic(t *testing.T) {
	m, root := loadLiveMatrix(t)
	usage, err := ScanSurfaceUsage(root)
	if err != nil {
		t.Fatalf("ScanSurfaceUsage: %v", err)
	}
	refs, err := ScanSentinelRefs(root)
	if err != nil {
		t.Fatalf("ScanSentinelRefs: %v", err)
	}
	first := RenderMarkdown(m, usage, refs)
	for i := 0; i < 5; i++ {
		if got := RenderMarkdown(m, usage, refs); got != first {
			t.Fatalf("RenderMarkdown is not deterministic (iteration %d differs)", i+1)
		}
	}
}

// TestMatrix_DerivedColumnsAreNonVacuous proves the source scans actually resolve
// something. A silently-empty scan would render an all-dashes table that looks
// tidy and says nothing.
func TestMatrix_DerivedColumnsAreNonVacuous(t *testing.T) {
	root := testModuleRoot(t)

	usage, err := ScanSurfaceUsage(root)
	if err != nil {
		t.Fatalf("ScanSurfaceUsage: %v", err)
	}
	// Query is reached by the CLI and MCP surfaces in every shipped configuration.
	for _, surface := range []string{"cli", "mcp"} {
		found := false
		for _, got := range usage["Query"] {
			if got == surface {
				found = true
			}
		}
		if !found {
			t.Errorf("surface usage scan did not find the %s surface calling Query; got %v", surface, usage["Query"])
		}
	}

	refs, err := ScanSentinelRefs(root)
	if err != nil {
		t.Fatalf("ScanSentinelRefs: %v", err)
	}
	if got := refs.For("TrustReport"); !strings.Contains(got, "ErrTrustUnavailable") {
		t.Errorf("sentinel scan lost TrustReport's refusal path; got %q", got)
	}
	// Scoping matters: a file-level text scan would attribute neighbouring
	// methods' sentinels to Query, which has no refusal path of its own.
	if got := refs.For("Query"); got != "—" {
		t.Errorf("Query has no sentinel path, but the scan attributed %q to it (function scoping is leaking)", got)
	}
}

// TestParseYAML_RejectsMalformedRows exercises the validation the matrix relies
// on. Each case is a way the inventory could quietly become wrong.
func TestParseYAML_RejectsMalformedRows(t *testing.T) {
	sentinelBlock := "\nsentinels:\n  - name: ErrX\n    kind: capability\n"
	cases := map[string]string{
		"duplicate method": "methods:\n" +
			"  - method: Query\n    context: graphread\n    direct: full\n    http: full\n    daemon: full\n" +
			"  - method: Query\n    context: knowledge\n    direct: full\n    http: full\n    daemon: full\n" +
			sentinelBlock,
		"unknown context": "methods:\n" +
			"  - method: Query\n    context: nowhere\n    direct: full\n    http: full\n    daemon: full\n" +
			sentinelBlock,
		"invalid implementation status": "methods:\n" +
			"  - method: Query\n    context: graphread\n    direct: mostly\n    http: full\n    daemon: full\n" +
			sentinelBlock,
		"invalid sentinel kind": "methods:\n" +
			"  - method: Query\n    context: graphread\n    direct: full\n    http: full\n    daemon: full\n" +
			"\nsentinels:\n  - name: ErrX\n    kind: vibes\n",
		"unknown field": "methods:\n" +
			"  - method: Query\n    context: graphread\n    direct: full\n    http: full\n    daemon: full\n    owner: someone\n" +
			sentinelBlock,
		"no sentinels block": "methods:\n" +
			"  - method: Query\n    context: graphread\n    direct: full\n    http: full\n    daemon: full\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := parseYAML(text)
			if err == nil {
				err = validate(m)
			}
			if err == nil {
				t.Errorf("parser accepted a matrix that should be rejected (%s)", name)
			}
		})
	}
}

// TestParseYAML_AcceptsWellFormedMatrix is the positive control: without it, a
// parser that rejected everything would pass every test above.
func TestParseYAML_AcceptsWellFormedMatrix(t *testing.T) {
	text := "# comment\n" +
		"methods:\n" +
		"  - method: Query\n" +
		"    context: graphread\n" +
		"    direct: full\n" +
		"    http: full\n" +
		"    daemon: typed-skip\n" +
		"    pilot: true\n" +
		"    note: \"a note with a # inside\"\n" +
		"\n" +
		"sentinels:\n" +
		"  - name: ErrX\n" +
		"    kind: safety\n" +
		"    note: refuses\n"

	m, err := parseYAML(text)
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	if err := validate(m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(m.Methods) != 1 || len(m.Sentinels) != 1 {
		t.Fatalf("got %d methods and %d sentinels, want 1 and 1", len(m.Methods), len(m.Sentinels))
	}
	row := m.Methods[0]
	if !row.Pilot {
		t.Error("pilot flag not parsed")
	}
	if row.Note != "a note with a # inside" {
		t.Errorf("note = %q; the # inside a quoted scalar was treated as a comment", row.Note)
	}
	if row.Service() != "app/graphread" || row.Phase() != 4 {
		t.Errorf("derived target = (%s, phase %d), want (app/graphread, phase 4)", row.Service(), row.Phase())
	}
}

// TestCheck_DetectsDriftInBothDirections proves the guard is not one-sided: an
// unmapped method and a phantom row must both fail.
func TestCheck_DetectsDriftInBothDirections(t *testing.T) {
	m := Matrix{
		Methods:   []Method{{Name: "Query", Context: ContextGraphRead}},
		Sentinels: []Sentinel{{Name: "ErrKnown", Kind: SentinelCapability}},
	}

	report := Check(m, []string{"Query", "Unmapped"}, []string{"ErrKnown"})
	if report.Pass() {
		t.Error("a live method missing from the matrix did not fail the check")
	}
	if len(report.MissingMethods) != 1 || report.MissingMethods[0] != "Unmapped" {
		t.Errorf("MissingMethods = %v, want [Unmapped]", report.MissingMethods)
	}

	report = Check(m, []string{}, []string{"ErrKnown"})
	if report.Pass() || len(report.PhantomMethods) != 1 {
		t.Errorf("a matrix row for a deleted method did not fail the check: %+v", report)
	}

	report = Check(m, []string{"Query"}, []string{"ErrKnown", "ErrNew"})
	if report.Pass() || len(report.MissingSentinels) != 1 {
		t.Errorf("a new sentinel missing from the inventory did not fail the check: %+v", report)
	}
}

// TestCheckStubs_DetectsBothDirections proves the stub freeze catches a newly
// added stub as well as a stale declaration.
func TestCheckStubs_DetectsBothDirections(t *testing.T) {
	m := Matrix{Methods: []Method{{
		Name: "Query", Context: ContextGraphRead,
		Direct: ImplFull, HTTP: ImplFull, Daemon: ImplFull,
	}}}

	newStub := StubScan{
		ImplNameDirect: {},
		ImplNameHTTP:   {"Query": true},
		ImplNameDaemon: {},
	}
	problems := CheckStubs(m, newStub)
	if len(problems) != 1 || !strings.Contains(problems[0], "http.Query") {
		t.Errorf("a new HTTP stub was not reported: %v", problems)
	}

	m.Methods[0].HTTP = ImplUnavailable
	if problems := CheckStubs(m, StubScan{ImplNameDirect: {}, ImplNameHTTP: {}, ImplNameDaemon: {}}); len(problems) != 1 {
		t.Errorf("a stale unavailable declaration was not reported: %v", problems)
	}
}
