package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes the corpus manifest against a built graphi binary.
type Runner struct {
	// Binary is the path to the graphi executable under test.
	Binary string
	// WorkDir is where clones and per-repo stores are materialized.
	WorkDir string
	// PerEntryTimeout bounds one repository's full step sequence.
	PerEntryTimeout time.Duration
	// Log receives human-readable progress lines (nil = silent).
	Log func(format string, args ...any)
	// Tier, when > 0, runs only entries with this exact tier.
	Tier int
	// MaxTier, when > 0, runs only entries with tier <= MaxTier.
	MaxTier int
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

func (r *Runner) includes(e Entry) bool {
	if r.Tier > 0 && e.Tier != r.Tier {
		return false
	}
	if r.MaxTier > 0 && e.Tier > r.MaxTier {
		return false
	}
	return true
}

// Run executes every manifest entry and returns the aggregate report. It never
// returns an error for an entry FAILURE (that is the report's job) — only for
// harness-level faults like an unusable work dir.
func (r *Runner) Run(ctx context.Context, m Manifest) (Report, error) {
	if r.PerEntryTimeout <= 0 {
		r.PerEntryTimeout = 10 * time.Minute
	}
	if err := os.MkdirAll(r.WorkDir, 0o755); err != nil {
		return Report{}, fmt.Errorf("corpus: workdir: %w", err)
	}
	rep := Report{Pass: true}
	for _, e := range m.Entries {
		if e.Tier == 0 {
			e.Tier = 1
		}
		if !r.includes(e) {
			r.logf("corpus: %s: skipped (tier %d)", e.Name, e.Tier)
			continue
		}
		er := r.runEntry(ctx, e)
		rep.Pass = rep.Pass && er.Pass
		rep.Entries = append(rep.Entries, er)
	}
	return rep, nil
}

// runEntry materializes the repo and drives the full CLI flow. Steps run in
// order; the first failure records its detail and aborts the entry (later
// steps would only cascade noise), but never panics the harness.
func (r *Runner) runEntry(ctx context.Context, e Entry) EntryReport {
	start := time.Now()
	er := EntryReport{Name: e.Name, URL: e.URL, Ref: e.Ref, Tier: e.Tier, BudgetMS: e.BudgetMS}
	ctx, cancel := context.WithTimeout(ctx, r.PerEntryTimeout)
	defer cancel()

	fail := func(step, detail string) EntryReport {
		er.Steps = append(er.Steps, StepResult{Name: step, OK: false, Detail: detail})
		er.Pass = false
		er.DurationMS = time.Since(start).Milliseconds()
		r.logf("corpus: %s: FAIL at %s: %s", e.Name, step, firstLine(detail))
		return er
	}
	ok := func(step, detail string) {
		er.Steps = append(er.Steps, StepResult{Name: step, OK: true, Detail: detail})
	}

	// 1. Materialize the checkout.
	repoDir := e.Path
	if e.URL != "" {
		repoDir = filepath.Join(r.WorkDir, "repos", e.Name)
		if err := r.clone(ctx, e, repoDir); err != nil {
			return fail("clone", err.Error())
		}
	}
	if head, err := gitHead(ctx, repoDir); err == nil {
		er.HeadSHA = head
		if e.SHA != "" && !shaMatches(e.SHA, head) {
			return fail("pin", fmt.Sprintf("HEAD %s does not match pinned sha %s (ref %q moved?)", head, e.SHA, e.Ref))
		}
	} else if e.SHA != "" {
		return fail("pin", fmt.Sprintf("sha pinned but HEAD unreadable: %v", err))
	}
	ok("materialize", repoDir)

	// Per-entry store + meta sidecar.
	stateDir := filepath.Join(r.WorkDir, "state", e.Name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fail("state-dir", err.Error())
	}
	db := filepath.Join(stateDir, "graph.db")
	meta := filepath.Join(stateDir, "meta")

	// 2. Full index. This is the step the historical first-contact crashes
	// (.DS_Store, pnpm symlinks, malformed JSON) would have tripped.
	// Panic marker first: a panicking binary also exits non-zero, so checking
	// err first would bury the panic line in the generic exit-status detail.
	out, err := r.graphi(ctx, "index", "-root", repoDir, "-db", db, "-meta", meta)
	if p := panicMarker(out); p != "" {
		return fail("index", "panic in output: "+p)
	}
	if err != nil {
		return fail("index", err.Error()+"\n"+tail(out))
	}
	ok("index", "")

	// 3. Search assertions from the manifest; the first hit seeds step 4.
	seedID := ""
	for _, s := range e.Searches {
		out, err := r.graphi(ctx, "search", "-db", db, "-limit", "5", s.Query)
		if err != nil {
			return fail("search:"+s.Query, err.Error()+"\n"+tail(out))
		}
		var res struct {
			Matches []struct {
				NodeID string `json:"node_id"`
			} `json:"matches"`
		}
		lastLine := lastJSONLine(out)
		if len(lastLine) == 0 {
			return fail("search:"+s.Query, "search produced no output at all")
		}
		if err := json.Unmarshal(lastLine, &res); err != nil {
			return fail("search:"+s.Query, "output is not the canonical search JSON: "+err.Error()+"\n"+tail(out))
		}
		if s.ExpectNonEmpty && len(res.Matches) == 0 {
			return fail("search:"+s.Query, "expected non-empty matches, got none — index likely produced no symbols for this repo")
		}
		if seedID == "" && len(res.Matches) > 0 {
			seedID = res.Matches[0].NodeID
		}
		ok("search:"+s.Query, fmt.Sprintf("%d match(es)", len(res.Matches)))
	}

	// 4. Structural query + impact analysis over the seed symbol. Outcome may
	// legitimately be empty (a leaf symbol); the assertion is exit 0 + valid
	// canonical JSON + no panic.
	if seedID != "" {
		for _, step := range [][]string{
			{"query", "callers", "-symbol", seedID, "-db", db},
			{"analyze", "impact", "-symbol", seedID, "-db", db},
		} {
			out, err := r.graphi(ctx, step...)
			name := step[0] + ":" + step[1]
			if p := panicMarker(out); p != "" {
				return fail(name, "panic in output: "+p)
			}
			if err != nil {
				return fail(name, err.Error()+"\n"+tail(out))
			}
			if !json.Valid(lastJSONLine(out)) {
				return fail(name, "output is not valid JSON\n"+tail(out))
			}
			ok(name, "")
		}
	}

	// 5. Confirmed-tier assertions (v0.2.0 typeresolve acceptance): the graph
	// must contain type-checker-proven edges where the manifest promises them.
	for _, ce := range e.ConfirmedEdges {
		name := "confirmed:" + ce.Operation + ":" + ce.SymbolQuery
		anchor, detail := r.findAnchor(ctx, db, ce.SymbolQuery)
		if anchor == "" {
			return fail(name, detail)
		}
		out, err := r.graphi(ctx, "query", ce.Operation, "-symbol", anchor, "-db", db)
		if p := panicMarker(out); p != "" {
			return fail(name, "panic in output: "+p)
		}
		if err != nil {
			return fail(name, err.Error()+"\n"+tail(out))
		}
		var qr struct {
			Edges []struct {
				Tier string `json:"confidence_tier"`
			} `json:"edges"`
		}
		line := lastJSONLine(out)
		if len(line) == 0 {
			return fail(name, "query produced no output at all")
		}
		if err := json.Unmarshal(line, &qr); err != nil {
			return fail(name, "output is not the canonical query JSON: "+err.Error()+"\n"+tail(out))
		}
		confirmed := 0
		for _, ed := range qr.Edges {
			if ed.Tier == "confirmed" {
				confirmed++
			}
		}
		if confirmed < ce.Min {
			return fail(name, fmt.Sprintf("%d confirmed edge(s) of %d total, want >= %d — the typeresolve pass did not prove this relation", confirmed, len(qr.Edges), ce.Min))
		}
		ok(name, fmt.Sprintf("%d confirmed edge(s)", confirmed))
	}

	// 6. Diagnostics over the whole graph.
	if out, err := r.graphi(ctx, "diagnose", "-db", db); err != nil {
		return fail("diagnose", err.Error()+"\n"+tail(out))
	} else if !json.Valid(lastJSONLine(out)) {
		return fail("diagnose", "output is not valid JSON\n"+tail(out))
	}
	ok("diagnose", "")

	er.Pass = true
	er.DurationMS = time.Since(start).Milliseconds()
	r.logf("corpus: %s: PASS (%.1fs)", e.Name, time.Since(start).Seconds())
	return er
}

// findAnchor resolves a symbol query to a node id: the first search match
// whose qualified name's LAST dotted segment equals the query exactly. The
// exactness matters — a query like "ExecuteC" must not silently anchor on
// "ExecuteContext" just because fuzzy ranking put it first. Returns ("",
// detail) when no exact match exists.
func (r *Runner) findAnchor(ctx context.Context, db, symbolQuery string) (anchor, detail string) {
	out, err := r.graphi(ctx, "search", "-db", db, "-limit", "25", symbolQuery)
	if err != nil {
		return "", err.Error() + "\n" + tail(out)
	}
	var res struct {
		Matches []struct {
			NodeID        string `json:"node_id"`
			QualifiedName string `json:"qualified_name"`
		} `json:"matches"`
	}
	line := lastJSONLine(out)
	if len(line) == 0 {
		return "", "search produced no output at all"
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return "", "output is not the canonical search JSON: " + err.Error() + "\n" + tail(out)
	}
	for _, m := range res.Matches {
		bare := m.QualifiedName
		if i := strings.LastIndexByte(bare, '.'); i >= 0 {
			bare = bare[i+1:]
		}
		if bare == symbolQuery {
			return m.NodeID, ""
		}
	}
	return "", fmt.Sprintf("no search match with exact symbol name %q among %d match(es)", symbolQuery, len(res.Matches))
}

// clone shallow-clones the entry's ref into dir. Fresh every run: a stale
// cached checkout would silently un-pin the corpus.
func (r *Runner) clone(ctx context.Context, e Entry, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clean clone dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("clone parent dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", e.Ref, "--single-branch", e.URL, dir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s @ %s: %v\n%s", e.URL, e.Ref, err, tail(out))
	}
	return nil
}

// shaMatches reports whether head has the pinned sha as a case-insensitive
// prefix. Short pins are standard git practice; LoadManifest enforces a
// minimum pin length so a short prefix cannot be vacuously ambiguous.
func shaMatches(pinned, head string) bool {
	if len(pinned) > len(head) {
		return false
	}
	return strings.EqualFold(pinned, head[:len(pinned)])
}

func gitHead(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// graphi runs the binary under test with args and returns its combined output.
func (r *Runner) graphi(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	// The harness pins the store explicitly per entry; make sure per-repo
	// session auto-discovery never kicks in by running from the neutral workdir.
	cmd.Dir = r.WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("graphi %s: %v", strings.Join(args, " "), err)
	}
	return out, nil
}

// panicMarker returns the first panic line in out, or "".
func panicMarker(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "panic:") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// lastJSONLine returns the last non-empty line of out — the CLI prints the
// canonical JSON payload as its final line (progress lines may precede it).
func lastJSONLine(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := bytes.TrimSpace(lines[i]); len(l) > 0 {
			return l
		}
	}
	return nil
}

// tail bounds diagnostic output in step details.
func tail(out []byte) string {
	const max = 800
	s := strings.TrimSpace(string(out))
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
