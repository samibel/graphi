package intraproctaint

import (
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/analysis/taint"
)

// vulnGoSources mirrors the ingest e2e fixture (engine/ingest/taint_vulngo_e2e_test.go
// vulnGoRepo): four planted injection flows plus two must-not-flag safe paths.
// Stage 1 proves the pure Analyze function in isolation before any integration.
var vulnGoSources = map[string]string{
	"httphelper.go": `package app

import "net/http"

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

func vulnSQLiDirect(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := db.Query("SELECT * FROM users WHERE id = " + id)
	_ = rows
	_ = w
}

func vulnSQLiWrapper(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	name := GetQueryParam(r, "name")
	_, _ = db.Exec("DELETE FROM users WHERE name = '" + name + "'")
	_ = w
}

func vulnRCE(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	out, _ := exec.Command("sh", "-c", cmd).Output()
	_, _ = w.Write(out)
}

func vulnLFI(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	data, _ := os.ReadFile("/var/data/" + name)
	_, _ = w.Write(data)
}

func safeSanitized(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("id")
	id, err := strconv.Atoi(raw)
	if err != nil {
		return
	}
	_, _ = db.Exec("UPDATE users SET seen = 1 WHERE id = ?", id)
	_ = w
}

func safeConstant(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command("uptime").Output()
	_, _ = w.Write(out)
	_ = r
}
`,
}

func analyzeVulnGo(t *testing.T) []taint.Finding {
	t.Helper()
	cfg := taint.DefaultConfig()
	var all []taint.Finding
	names := make([]string, 0, len(vulnGoSources))
	for name := range vulnGoSources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, vulnGoSources[name], parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		all = append(all, Analyze(file, fset, cfg)...)
	}
	return all
}

func TestAnalyze_VulnGoFlows(t *testing.T) {
	findings := analyzeVulnGo(t)

	type flow struct {
		category string
		sinkFunc string
	}
	want := []flow{
		{"sql_injection", "vulnSQLiDirect"},
		{"sql_injection", "vulnSQLiWrapper"},
		{"command_injection", "vulnRCE"},
		{"path_traversal", "vulnLFI"},
	}

	touches := func(f taint.Finding, fn string) bool {
		if strings.Contains(f.SinkName, fn) || strings.Contains(f.SourceName, fn) {
			return true
		}
		for _, s := range f.Path {
			if strings.Contains(s.QualifiedName, fn) {
				return true
			}
		}
		return false
	}

	matched := 0
	for _, wf := range want {
		found := false
		for _, f := range findings {
			if f.SinkCategory == wf.category && touches(f, wf.sinkFunc) {
				found = true
				break
			}
		}
		if found {
			matched++
		} else {
			t.Errorf("MISSING flow: %s in %s", wf.category, wf.sinkFunc)
		}
	}

	// No finding may touch a safe handler (sanitized / constant-arg paths).
	fp := 0
	for _, safe := range []string{"safeSanitized", "safeConstant"} {
		for _, f := range findings {
			if touches(f, safe) {
				fp++
				t.Errorf("FALSE POSITIVE: finding touches safe handler %s: sink=%q source=%q cat=%s",
					safe, f.SinkName, f.SourceName, f.SinkCategory)
			}
		}
	}

	t.Logf("intraproc taint: recall=%d/%d false_positives=%d total_findings=%d", matched, len(want), fp, len(findings))
	for _, f := range findings {
		t.Logf("  finding: category=%s source=%q sink=%q labels=%s", f.SinkCategory, f.SourceName, f.SinkName, f.Labels)
	}

	if matched != len(want) {
		t.Fatalf("recall = %d/%d, want %d/%d", matched, len(want), len(want), len(want))
	}
	if fp != 0 {
		t.Fatalf("false positives = %d, want 0", fp)
	}
}

// TestAnalyze_Deterministic proves identical input yields identical findings.
func TestAnalyze_Deterministic(t *testing.T) {
	a := analyzeVulnGo(t)
	b := analyzeVulnGo(t)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].SinkName != b[i].SinkName || a[i].SourceName != b[i].SourceName || a[i].SinkCategory != b[i].SinkCategory {
			t.Fatalf("nondeterministic finding at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}
