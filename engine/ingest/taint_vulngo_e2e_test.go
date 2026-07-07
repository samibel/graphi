package ingest_test

// RED GATE (WP-00a / gate-first). This end-to-end fixture is the acceptance
// test the vuln-go field-finding demanded: ingest real Go sources with four
// planted injection flows (2x SQLi, 1x RCE, 1x LFI) plus two must-not-flag
// sanitized/safe paths, run the production ingest+link pipeline, then run the
// real taint analyzer with its default config and measure recall + precision.
//
// It is RED today by construction: unresolved external call targets
// (os/exec.Command, database/sql.DB.Query, os.ReadFile, ...) are dropped during
// linking and never materialized as graph nodes (engine/link/resolve_go.go:86),
// so the default sinks — which key on exactly those qualified names
// (engine/analysis/taint/config.go:205) — can never match. Measured recall is
// therefore 0/4.
//
// The gate is checked in DISARMED (gateArmed=false): it exercises the full
// pipeline and LOGS the measured recall/precision every run, but does not fail
// CI. WP-03 materializes external call targets as interned `external` nodes;
// WP-05 flips gateArmed to true and this test becomes the hard 4/4-recall,
// 0-false-positive gate that would have caught the field finding from day one.

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/taint"
)

// gateArmed is the single WP-05 switch. While false, the test reports the
// measured numbers without failing (the red gate is visible but non-blocking).
// WP-05 sets it to true, at which point the assertions below enforce the
// acceptance criteria: recall 4/4 and zero false positives on the safe paths.
const gateArmed = false

// vulnGoRepo is the fixture: a small HTTP service with four attacker-controlled
// flows that reach dangerous sinks, plus two paths that must NOT be reported.
// The sources are syntactically valid Go so the production extractor sees the
// real symbol/edge shape (they are ingested from a temp dir, never compiled).
var vulnGoRepo = map[string]string{
	"go.mod": "module vulngo\n\ngo 1.21\n",

	"httphelper.go": `package app

import "net/http"

// GetQueryParam is a thin wrapper over the request query. Because it returns
// attacker-controlled input, its internal r.URL.Query() call makes it a taint
// SOURCE — the wrapper-source case that interprocedural propagation must carry
// (A2/WP-05).
func GetQueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}
`,

	"handlers.go": `package app

import (
	"database/sql"
	"net/http"
	"os"
	"os/exec"
	"strconv"
)

// vulnSQLiDirect — SQL injection: a tainted query parameter is concatenated
// straight into a SQL string. (flow 1: sql_injection)
func vulnSQLiDirect(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := db.Query("SELECT * FROM users WHERE id = " + id)
	_ = rows
	_ = w
}

// vulnSQLiWrapper — SQL injection through the GetQueryParam wrapper source.
// (flow 2: sql_injection via wrapper propagation)
func vulnSQLiWrapper(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	name := GetQueryParam(r, "name")
	_, _ = db.Exec("DELETE FROM users WHERE name = '" + name + "'")
	_ = w
}

// vulnRCE — command injection: tainted input reaches os/exec.Command.
// (flow 3: command_injection)
func vulnRCE(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	out, _ := exec.Command("sh", "-c", cmd).Output()
	_, _ = w.Write(out)
}

// vulnLFI — path traversal: tainted path read from disk.
// (flow 4: path_traversal)
func vulnLFI(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	data, _ := os.ReadFile("/var/data/" + name)
	_, _ = w.Write(data)
}

// safeSanitized — user input is fully sanitized via strconv.Atoi (a configured
// sanitizer) before it reaches the sink. MUST NOT be reported. (negative 1)
func safeSanitized(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("id")
	id, err := strconv.Atoi(raw)
	if err != nil {
		return
	}
	_, _ = db.Exec("UPDATE users SET seen = 1 WHERE id = ?", id)
	_ = w
}

// safeConstant — the sink argument is a hardcoded constant; no user input
// reaches it. MUST NOT be reported. (negative 2)
func safeConstant(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command("uptime").Output()
	_, _ = w.Write(out)
	_ = r
}
`,

	"register.go": `package app

import (
	"database/sql"
	"net/http"
)

// Register wires the handlers so every vulnerable and safe function is reachable
// from a real entry point (gives the linker concrete call edges to resolve).
func Register(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("/sqli", func(w http.ResponseWriter, r *http.Request) { vulnSQLiDirect(db, w, r) })
	mux.HandleFunc("/sqli2", func(w http.ResponseWriter, r *http.Request) { vulnSQLiWrapper(db, w, r) })
	mux.HandleFunc("/rce", vulnRCE)
	mux.HandleFunc("/lfi", vulnLFI)
	mux.HandleFunc("/safe1", func(w http.ResponseWriter, r *http.Request) { safeSanitized(db, w, r) })
	mux.HandleFunc("/safe2", safeConstant)
}
`,
}

// expectedFlow is one planted vulnerability that a correct taint analysis must
// report. sinkFunc is the vulnerable handler whose body contains the sink; a
// finding satisfies the flow when its category matches and its provenance path
// passes through that handler.
type expectedFlow struct {
	name     string
	category string
	sinkFunc string
}

var expectedFlows = []expectedFlow{
	{name: "sqli-direct", category: "sql_injection", sinkFunc: "vulnSQLiDirect"},
	{name: "sqli-wrapper", category: "sql_injection", sinkFunc: "vulnSQLiWrapper"},
	{name: "rce", category: "command_injection", sinkFunc: "vulnRCE"},
	{name: "lfi", category: "path_traversal", sinkFunc: "vulnLFI"},
}

// safeFuncs are handlers that must never appear in a reported flow.
var safeFuncs = []string{"safeSanitized", "safeConstant"}

func TestTaintE2E_VulnGoRecall(t *testing.T) {
	ctx := context.Background()

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	repo := writeRepo(t, vulnGoRepo)
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest vuln-go fixture: %v", err)
	}

	analyzer := taint.New(taint.DefaultConfig(), taint.DefaultCaps(), nil)
	res, err := analyzer.Run(ctx, store)
	if err != nil {
		t.Fatalf("run taint analyzer: %v", err)
	}

	recall := matchedFlows(res.Findings)
	fp := falsePositives(res.Findings)

	t.Logf("[RED GATE WP-05] vuln-go taint: recall=%d/%d, false_positives=%d, findings=%d, armed=%v",
		recall, len(expectedFlows), fp, len(res.Findings), gateArmed)

	if !gateArmed {
		if recall == len(expectedFlows) && fp == 0 {
			t.Logf("acceptance criteria now satisfiable — arm the gate: set gateArmed=true (WP-05)")
		}
		return
	}

	if recall != len(expectedFlows) {
		t.Errorf("taint recall = %d/%d, want %d/%d (missing flows are silent false-negatives)",
			recall, len(expectedFlows), len(expectedFlows), len(expectedFlows))
	}
	if fp != 0 {
		t.Errorf("taint false positives = %d on sanitized/safe paths, want 0 (precision floor 0.8)", fp)
	}
}

// matchedFlows counts how many planted vulnerabilities are covered by at least
// one finding of the right category whose provenance path passes through the
// vulnerable handler.
func matchedFlows(findings []taint.Finding) int {
	matched := 0
	for _, ef := range expectedFlows {
		for _, f := range findings {
			if f.SinkCategory == ef.category && pathTouches(f, ef.sinkFunc) {
				matched++
				break
			}
		}
	}
	return matched
}

// falsePositives counts findings whose provenance path touches a safe handler
// that must never be reported.
func falsePositives(findings []taint.Finding) int {
	fp := 0
	for _, f := range findings {
		for _, safe := range safeFuncs {
			if pathTouches(f, safe) {
				fp++
				break
			}
		}
	}
	return fp
}

// pathTouches reports whether any step in the finding's provenance path (or the
// sink/source name) references the given handler symbol.
func pathTouches(f taint.Finding, fn string) bool {
	if strings.Contains(f.SinkName, fn) || strings.Contains(f.SourceName, fn) {
		return true
	}
	for _, step := range f.Path {
		if strings.Contains(step.QualifiedName, fn) {
			return true
		}
	}
	return false
}
