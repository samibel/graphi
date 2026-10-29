package validate

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/state"
)

// hermeticTools are the external programs the gates may call. The behavioural
// tests run the scripts with a PATH that holds symlinks to exactly these, so
// "jq missing" and "graphi missing" are reproducible and nothing leaks in from
// the developer's machine.
var hermeticTools = []string{
	"awk", "basename", "cat", "cut", "date", "dirname", "git", "grep", "mv",
	"rm", "sed", "sha256sum", "shasum", "tail", "tr",
}

type env struct {
	t         *testing.T
	sh        string
	scriptDir string
	root      string // project dir the hook believes it runs in
	state     string // XDG_STATE_HOME
	tmp       string // TMPDIR (retry ledger)
	bin       string // hermetic PATH
}

type envOpts struct {
	graphi   bool // put a graphi stub on PATH
	jq       bool // put jq on PATH
	index    bool // create the db.sqlite the gate looks for
	trailing bool // pass CLAUDE_PROJECT_DIR with a trailing slash
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts are POSIX sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; behavioural gate tests need it (static tests still run)")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			t.Skip("no sha256 tool installed")
		}
	}

	base := t.TempDir()
	e := &env{
		t:         t,
		sh:        sh,
		scriptDir: filepath.Join(pluginDir(t), "hooks", "scripts"),
		root:      filepath.Join(base, "repo"),
		state:     filepath.Join(base, "state"),
		tmp:       filepath.Join(base, "tmp"),
		bin:       filepath.Join(base, "bin"),
	}
	for _, d := range []string{e.root, filepath.Join(e.root, "src"), filepath.Join(e.root, "docs"), e.state, e.tmp, e.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tool := range hermeticTools {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(e.bin, tool))
		}
	}
	if o.jq {
		p, _ := exec.LookPath("jq")
		if err := os.Symlink(p, filepath.Join(e.bin, "jq")); err != nil {
			t.Fatal(err)
		}
	}
	if o.graphi {
		if err := os.WriteFile(filepath.Join(e.bin, "graphi"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if o.index {
		dir := filepath.Join(e.state, "graphi", state.Fingerprint(e.root))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "db.sqlite"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if o.trailing {
		e.root += "/"
	}
	return e
}

func (e *env) run(script, payload string, extra ...string) (stdout string, exit int) {
	e.t.Helper()
	cmd := exec.Command(e.sh, filepath.Join(e.scriptDir, script))
	cmd.Dir = e.t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + e.bin,
		"HOME=" + e.t.TempDir(),
		"CLAUDE_PROJECT_DIR=" + e.root,
		"XDG_STATE_HOME=" + e.state,
		"TMPDIR=" + e.tmp,
	}, extra...)
	cmd.Stdin = strings.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	}
	if err != nil {
		e.t.Fatalf("run %s: %v", script, err)
	}
	return out.String(), 0
}

func grepPayload(session, pattern, path, glob, typ string) string {
	in := map[string]string{"pattern": pattern}
	if path != "" {
		in["path"] = path
	}
	if glob != "" {
		in["glob"] = glob
	}
	if typ != "" {
		in["type"] = typ
	}
	b, _ := json.Marshal(map[string]any{"session_id": session, "tool_name": "Grep", "tool_input": in})
	return string(b)
}

func bashPayload(session, command string) string {
	b, _ := json.Marshal(map[string]any{
		"session_id": session, "tool_name": "Bash",
		"tool_input": map[string]string{"command": command},
	})
	return string(b)
}

type decision struct {
	Out struct {
		EventName string `json:"hookEventName"`
		Decision  string `json:"permissionDecision"`
		Reason    string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// assertDecision checks allow (empty stdout, exit 0) or deny (well-formed deny
// JSON naming the pattern and a graphi alternative, exit 0).
func assertDecision(t *testing.T, name, stdout string, exit int, wantDeny bool, pattern string) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("%s: exit = %d, a gate must always exit 0", name, exit)
	}
	if !wantDeny {
		if strings.TrimSpace(stdout) != "" {
			t.Fatalf("%s: expected allow (empty stdout), got %q", name, stdout)
		}
		return
	}
	var d decision
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("%s: expected deny JSON, got %q (%v)", name, stdout, err)
	}
	if d.Out.EventName != "PreToolUse" || d.Out.Decision != "deny" {
		t.Fatalf("%s: unexpected decision %+v", name, d.Out)
	}
	for _, want := range []string{"graphi-shunt", pattern, "callers", "references", "GRAPHI_SHUNT_DISABLE"} {
		if !strings.Contains(d.Out.Reason, want) {
			t.Fatalf("%s: deny reason is missing %q: %s", name, want, d.Out.Reason)
		}
	}
}

func TestGrepGateDecisionTable(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	file := filepath.Join(e.root, "src", "a.go")
	cases := []struct {
		name                    string
		pattern, path, glob, ty string
		deny                    bool
	}{
		{"camelCase identifier", "IngestAll", "", "", "", true},
		{"snake_case identifier", "parse_config_file", "", "", "", true},
		{"qualified name", "auth.Middleware", "", "", "", true},
		{"cpp qualified name", "ingest::IngestAll", "", "", "", true},
		{"definition probe", "func HandleRequest", "", "", "", true},
		{"call probe", `ParseConfig\(`, "", "", "", true},
		{"word-bounded identifier", `\bIngestAll\b`, "", "", "", true},
		{"long lowercase identifier", "scheduler", "", "", "", true},
		{"identifier in code dir", "IngestAll", "src", "", "", true},
		{"identifier with code glob", "IngestAll", "", "*.go", "", true},
		{"short lowercase word", "main", "", "", "", false},
		{"marker word", "TODO", "", "", "", false},
		{"multi-word text", "connection refused", "", "", "", false},
		{"regex", `foo.*bar`, "", "", "", false},
		{"file name", "main.go", "", "", "", false},
		{"targeted single file", "IngestAll", file, "", "", false},
		{"docs directory", "IngestAll", "docs", "", "", false},
		{"markdown glob", "IngestAll", "", "*.md", "", false},
		{"brace non-code glob", "IngestAll", "", "*.{json,yaml}", "", false},
		{"non-code type", "IngestAll", "", "", "json", false},
	}
	for i, c := range cases {
		session := "grep-" + string(rune('a'+i))
		out, exit := e.run("gate-grep.sh", grepPayload(session, c.pattern, c.path, c.glob, c.ty))
		assertDecision(t, c.name, out, exit, c.deny, c.pattern)
	}
}

func TestBashGateDecisionTable(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	cases := []struct {
		name    string
		command string
		deny    bool
		pattern string
	}{
		{"rg symbol", "rg IngestAll", true, "IngestAll"},
		{"rg with flags", "rg -n --type go IngestAll", true, "IngestAll"},
		{"rg -e", "rg -n -e QueryEngine", true, "QueryEngine"},
		{"recursive grep", "grep -rn --include=*.go HandleRequest .", true, "HandleRequest"},
		{"git grep", "git grep -n ParseConfig", true, "ParseConfig"},
		{"ag symbol", "ag WriteBatch", true, "WriteBatch"},
		{"after cd", "cd src && rg WriteBatch", true, "WriteBatch"},
		{"first of a pipeline", "rg IngestAll | head -20", true, "IngestAll"},
		{"quoted symbol", `rg "IngestAll"`, true, "IngestAll"},
		{"no search tool", "ls -la && go test ./...", false, ""},
		{"merge is not a search", "git merge main", false, ""},
		{"piped filter", "git log | grep ParseConfig", false, ""},
		{"non-recursive grep", "grep ParseConfig src/a.go", false, ""},
		{"search of one file", "rg ParseConfig src/a.go", false, ""},
		{"search of docs", "rg ParseConfig docs/", false, ""},
		{"multi-word phrase", `rg "connection refused"`, false, ""},
		{"short word", "rg main", false, ""},
		{"command substitution", `rg $(cat names.txt)`, false, ""},
		{"heredoc", "grep -r Foo <<EOF\nx\nEOF", false, ""},
	}
	for i, c := range cases {
		session := "bash-" + string(rune('a'+i))
		out, exit := e.run("gate-bash.sh", bashPayload(session, c.command))
		assertDecision(t, c.name, out, exit, c.deny, c.pattern)
	}
}

func TestSecondIdenticalAttemptIsAllowed(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	payload := grepPayload("retry", "IngestAll", "", "", "")
	out, exit := e.run("gate-grep.sh", payload)
	assertDecision(t, "first attempt", out, exit, true, "IngestAll")
	out, exit = e.run("gate-grep.sh", payload)
	assertDecision(t, "identical retry", out, exit, false, "")

	// A different search in the same session is still gated.
	out, exit = e.run("gate-grep.sh", grepPayload("retry", "WriteBatch", "", "", ""))
	assertDecision(t, "different pattern", out, exit, true, "WriteBatch")

	// A different session starts with a clean ledger.
	out, exit = e.run("gate-grep.sh", grepPayload("other-session", "IngestAll", "", "", ""))
	assertDecision(t, "other session", out, exit, true, "IngestAll")

	bash := bashPayload("retry", "rg IngestAll")
	out, exit = e.run("gate-bash.sh", bash)
	assertDecision(t, "bash first", out, exit, true, "IngestAll")
	out, exit = e.run("gate-bash.sh", bash)
	assertDecision(t, "bash retry", out, exit, false, "")
}

func TestUnwritableLedgerFailsOpen(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	missing := filepath.Join(e.tmp, "does", "not", "exist")
	out, exit := e.run("gate-grep.sh", grepPayload("x", "IngestAll", "", "", ""), "TMPDIR="+missing)
	assertDecision(t, "unwritable ledger", out, exit, false, "")
}

func TestGatesFailOpen(t *testing.T) {
	payloads := map[string]string{
		"gate-grep.sh": grepPayload("fo", "IngestAll", "", "", ""),
		"gate-bash.sh": bashPayload("fo", "rg IngestAll"),
	}
	cases := []struct {
		name string
		opts envOpts
		env  []string
	}{
		{"no index", envOpts{graphi: true, jq: true, index: false}, nil},
		{"graphi not installed", envOpts{graphi: false, jq: true, index: true}, nil},
		{"jq not installed", envOpts{graphi: true, jq: false, index: true}, nil},
		{"escape hatch", envOpts{graphi: true, jq: true, index: true}, []string{"GRAPHI_SHUNT_DISABLE=1"}},
	}
	for _, c := range cases {
		e := newEnv(t, c.opts)
		for script, payload := range payloads {
			out, exit := e.run(script, payload, c.env...)
			assertDecision(t, c.name+"/"+script, out, exit, false, "")
		}
	}

	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	for _, script := range []string{"gate-grep.sh", "gate-bash.sh"} {
		for name, payload := range map[string]string{"empty stdin": "", "malformed json": "{not json rg grep", "no tool_input": `{"session_id":"x"}`} {
			out, exit := e.run(script, payload)
			assertDecision(t, name+"/"+script, out, exit, false, "")
		}
	}
}

// TestShellFingerprintMatchesGo pins the shell copy of the store-path derivation
// to internal/state: the gate must find an index the Go side created, including
// when the project dir is passed with a trailing slash (filepath.Clean).
func TestShellFingerprintMatchesGo(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		e := newEnv(t, envOpts{graphi: true, jq: true, index: true, trailing: trailing})
		out, exit := e.run("gate-grep.sh", grepPayload("fp", "IngestAll", "", "", ""))
		assertDecision(t, "fingerprint parity (trailing slash="+map[bool]string{false: "no", true: "yes"}[trailing]+")", out, exit, true, "IngestAll")
	}
}
