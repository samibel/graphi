package intraproctaint

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/samibel/graphi/engine/analysis/taint"
)

// precisionFixture is a realistic multi-handler HTTP service used as the
// WP-05b-3 precision regression. It deliberately contains the exact false-
// positive SHAPES adversarial verification found (precision was 0.44 on these):
// an err from a multi-value call logged, a bool comparison logged, a len()
// logged, an fmt.Fprintf of user input, plus genuinely-safe sanitized /
// constant / allow-listed paths — alongside real vulnerabilities that MUST be
// reported. `db` is a PACKAGE-LEVEL var (not a parameter) so it also exercises
// the recall fix C (global-db sink resolution).
const precisionFixture = `package app

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)

var db *sql.DB

// ---- VULNERABLE: must be reported ----

// vGlobalSQLi: tainted id concatenated into a query on the PACKAGE-LEVEL db.
func vGlobalSQLi(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := db.Query("SELECT * FROM users WHERE id = " + id)
	_ = rows
	_ = w
}

// vFormSQLi: tainted form value concatenated into an Exec on the global db.
func vFormSQLi(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	_, _ = db.Exec("DELETE FROM users WHERE name = '" + name + "'")
	_ = w
}

// vRCE: tainted input reaches exec.Command.
func vRCE(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	out, _ := exec.Command("sh", "-c", cmd).Output()
	_, _ = w.Write(out)
}

// vLogInjection: raw user input written straight to the log (real log injection).
func vLogInjection(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	log.Printf("login attempt user=%s", user)
	_ = w
}

// ---- SAFE: must NOT be reported ----

// sErrLogged: err comes from a MULTI-VALUE call; it is NOT data (fix A3).
func sErrLogged(w http.ResponseWriter, r *http.Request) {
	_, err := time.Parse(time.RFC3339, r.FormValue("t"))
	if err != nil {
		log.Printf("bad time: %v", err)
	}
	_ = w
}

// sBoolLogged: ok is a BOOL from a comparison; not tainted (fix A1).
func sBoolLogged(w http.ResponseWriter, r *http.Request) {
	ok := r.FormValue("role") == "admin"
	log.Printf("is_admin=%v", ok)
	_ = w
}

// sLenLogged: n is an int from len(); not tainted (fix A2).
func sLenLogged(w http.ResponseWriter, r *http.Request) {
	n := len(r.URL.Query().Get("q"))
	log.Printf("query_len=%d", n)
	_ = w
}

// sSanitized: input is sanitized via strconv.Atoi and bound as a query param.
func sSanitized(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return
	}
	_, _ = db.Exec("UPDATE users SET seen = 1 WHERE id = ?", id)
	_ = w
}

// sConstant: the sink argument is a hardcoded constant; no user input reaches it.
func sConstant(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT 1")
	_ = rows
	_ = r
	_ = w
}

// sAllowList: the ORDER BY direction is chosen from a fixed allow-list of string
// literals; the raw form value only feeds a bool comparison, never the query.
func sAllowList(w http.ResponseWriter, r *http.Request) {
	dir := "asc"
	if r.FormValue("dir") == "desc" {
		dir = "desc"
	}
	rows, _ := db.Query("SELECT id FROM t ORDER BY id " + dir)
	_ = rows
	_ = w
}

// sFprintf: fmt.Fprintf is a general formatter, deliberately NOT a log sink
// after fix B — even with user input this must not be reported.
func sFprintf(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	fmt.Fprintf(w, "hello %s", name)
}
`

// precHandler describes an expected classification for one handler in the
// precision fixture.
type precHandler struct {
	fn    string
	vuln  bool   // true => at least one finding must touch it
	cat   string // expected category when vuln
	shape string // human note for the shape being exercised
}

var precHandlers = []precHandler{
	{fn: "vGlobalSQLi", vuln: true, cat: "sql_injection", shape: "global-db concat SQLi"},
	{fn: "vFormSQLi", vuln: true, cat: "sql_injection", shape: "form-value concat SQLi"},
	{fn: "vRCE", vuln: true, cat: "command_injection", shape: "exec.Command RCE"},
	{fn: "vLogInjection", vuln: true, cat: "log_injection", shape: "raw user input to log.Printf"},
	{fn: "sErrLogged", vuln: false, shape: "multi-value err logged (A3)"},
	{fn: "sBoolLogged", vuln: false, shape: "bool comparison logged (A1)"},
	{fn: "sLenLogged", vuln: false, shape: "len() logged (A2)"},
	{fn: "sSanitized", vuln: false, shape: "strconv.Atoi sanitized"},
	{fn: "sConstant", vuln: false, shape: "constant-arg query"},
	{fn: "sAllowList", vuln: false, shape: "allow-listed ORDER BY"},
	{fn: "sFprintf", vuln: false, shape: "fmt.Fprintf of user input (B)"},
}

func touchesFn(f taint.Finding, fn string) bool {
	// The sink/source names embed "<pkg>.<Fn>@..."; use a segment match so
	// handler names cannot collide as substrings of one another.
	if containsFn(f.SinkName, fn) || containsFn(f.SourceName, fn) {
		return true
	}
	for _, s := range f.Path {
		if containsFn(s.QualifiedName, fn) {
			return true
		}
	}
	return false
}

// containsFn matches the handler name as a qualified-name segment ("app.<fn>@"
// or "app.<fn> " / "app.<fn>-"), avoiding substring collisions between handlers.
func containsFn(hay, fn string) bool {
	seg := "." + fn
	for i := 0; i+len(seg) <= len(hay); i++ {
		if hay[i:i+len(seg)] != seg {
			continue
		}
		// require a non-identifier byte after the segment
		if i+len(seg) == len(hay) {
			return true
		}
		c := hay[i+len(seg)]
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return true
		}
	}
	return false
}

// TestPrecisionRegression measures precision/recall on the realistic multi-
// handler fixture. Before WP-05b-3 precision was 0.44 (the err/bool/len/Fprintf
// shapes all fired as false positives); this test asserts every vulnerable
// handler is still found and every safe handler is NOT, and reports the full
// confusion matrix.
func TestPrecisionRegression(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "app.go", precisionFixture, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	findings := Analyze(file, fset, taint.DefaultConfig())

	// tp: vulnerable handler correctly flagged (right category).
	// fp: safe handler flagged (a false positive) — counted per safe handler hit.
	// fn: vulnerable handler missed. tn: safe handler correctly clean.
	var tp, fp, fnCount, tn int
	for _, h := range precHandlers {
		hit := false
		for _, f := range findings {
			if !touchesFn(f, h.fn) {
				continue
			}
			if h.vuln && f.SinkCategory != h.cat {
				continue
			}
			hit = true
			break
		}
		switch {
		case h.vuln && hit:
			tp++
		case h.vuln && !hit:
			fnCount++
			t.Errorf("FALSE NEGATIVE: vulnerable handler %s (%s) was not reported", h.fn, h.shape)
		case !h.vuln && hit:
			fp++
			t.Errorf("FALSE POSITIVE: safe handler %s (%s) was reported", h.fn, h.shape)
		default:
			tn++
		}
	}

	precision := 1.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 1.0
	if tp+fnCount > 0 {
		recall = float64(tp) / float64(tp+fnCount)
	}

	t.Logf("confusion matrix: TP=%d FP=%d FN=%d TN=%d (of %d handlers, %d findings)",
		tp, fp, fnCount, tn, len(precHandlers), len(findings))
	t.Logf("precision=%.2f recall=%.2f (baseline before WP-05b-3 was precision=0.44)", precision, recall)
	for _, f := range findings {
		t.Logf("  finding: category=%s source=%q sink=%q", f.SinkCategory, f.SourceName, f.SinkName)
	}

	if precision < 0.8 {
		t.Fatalf("precision = %.2f, want >= 0.8", precision)
	}
	if recall < 1.0 {
		t.Fatalf("recall = %.2f, want 1.0 (all planted vulns must be found)", recall)
	}
}
