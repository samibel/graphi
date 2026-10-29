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
	"awk", "basename", "cat", "cut", "date", "dirname", "grep", "mv",
	"rm", "sed", "sha256sum", "shasum", "tail", "tr",
}

type env struct {
	t         *testing.T
	sh        string
	scriptDir string
	root      string // repository root the index was built for
	project   string // CLAUDE_PROJECT_DIR handed to the hook
	state     string // XDG_STATE_HOME
	tmp       string // TMPDIR (retry ledger)
	bin       string // hermetic PATH
	jqMarker  string // written by the jq spy stub on every invocation
}

type envOpts struct {
	graphi   bool   // put a graphi stub on PATH
	jq       bool   // put jq on PATH
	jqSpy    bool   // wrap jq so each call leaves a marker file
	index    bool   // create the db.sqlite the gate looks for
	trailing bool   // pass CLAUDE_PROJECT_DIR with a trailing slash
	marker   string // repo-root marker to create in root (".git" or "go.mod")
	subdir   string // run the hook from this subdirectory of root
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
	jqPath, err := exec.LookPath("jq")
	if err != nil {
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
		jqMarker:  filepath.Join(base, "jq-was-called"),
	}
	e.project = e.root
	for _, d := range []string{e.root, filepath.Join(e.root, "src"), filepath.Join(e.root, "docs"), e.state, e.tmp, e.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	switch o.marker {
	case "":
	case ".git":
		if err := os.MkdirAll(filepath.Join(e.root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	default:
		if err := os.WriteFile(filepath.Join(e.root, o.marker), []byte("module x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if o.subdir != "" {
		e.project = filepath.Join(e.root, o.subdir)
		if err := os.MkdirAll(e.project, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range hermeticTools {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(e.bin, tool))
		}
	}
	if o.jq {
		if o.jqSpy {
			stub := "#!/bin/sh\n: >\"" + e.jqMarker + "\"\nexec \"" + jqPath + "\" \"$@\"\n"
			if err := os.WriteFile(filepath.Join(e.bin, "jq"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink(jqPath, filepath.Join(e.bin, "jq")); err != nil {
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
		e.project += "/"
	}
	return e
}

// run executes a gate script the way Claude Code does: from an unrelated
// working directory, with the session directory only in the environment and
// the payload. The hook must not depend on its own cwd.
func (e *env) run(script, payload string, extra ...string) (stdout string, exit int) {
	e.t.Helper()
	cmd := exec.Command(e.sh, filepath.Join(e.scriptDir, script))
	cmd.Dir = e.t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + e.bin,
		"HOME=" + e.t.TempDir(),
		"CLAUDE_PROJECT_DIR=" + e.project,
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

type grepInput struct {
	pattern, path, glob, typ string
}

func grepPayload(session, cwd string, in grepInput) string {
	ti := map[string]string{"pattern": in.pattern}
	if in.path != "" {
		ti["path"] = in.path
	}
	if in.glob != "" {
		ti["glob"] = in.glob
	}
	if in.typ != "" {
		ti["type"] = in.typ
	}
	p := map[string]any{"session_id": session, "tool_name": "Grep", "tool_input": ti}
	if cwd != "" {
		p["cwd"] = cwd
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func bashPayload(session, cwd, command string) string {
	p := map[string]any{
		"session_id": session, "tool_name": "Bash",
		"tool_input": map[string]string{"command": command, "description": "run a command"},
	}
	if cwd != "" {
		p["cwd"] = cwd
	}
	b, _ := json.Marshal(p)
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
	for _, want := range []string{"graphi-shunt", `"` + pattern + `"`, "callers", "references", "GRAPHI_SHUNT_DISABLE"} {
		if !strings.Contains(d.Out.Reason, want) {
			t.Fatalf("%s: deny reason is missing %q: %s", name, want, d.Out.Reason)
		}
	}
}

func TestGrepGateDecisionTable(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	absFile := filepath.Join(e.root, "src", "a.go")
	outside := t.TempDir()
	cases := []struct {
		name string
		in   grepInput
		cwd  string
		deny bool
	}{
		{"camelCase identifier", grepInput{pattern: "IngestAll"}, "", true},
		{"snake_case identifier", grepInput{pattern: "parse_config_file"}, "", true},
		{"qualified name", grepInput{pattern: "auth.Middleware"}, "", true},
		{"cpp qualified name", grepInput{pattern: "ingest::IngestAll"}, "", true},
		{"definition probe", grepInput{pattern: "func HandleRequest"}, "", true},
		{"call probe", grepInput{pattern: `ParseConfig\(`}, "", true},
		{"word-bounded identifier", grepInput{pattern: `\bIngestAll\b`}, "", true},
		{"long lowercase identifier", grepInput{pattern: "scheduler"}, "", true},
		{"identifier in code dir", grepInput{pattern: "IngestAll", path: "src"}, e.root, true},
		{"identifier in abs code dir", grepInput{pattern: "IngestAll", path: filepath.Join(e.root, "src")}, "", true},
		{"identifier with code glob", grepInput{pattern: "IngestAll", glob: "*.go"}, "", true},
		{"short lowercase word", grepInput{pattern: "main"}, "", false},
		{"marker word", grepInput{pattern: "TODO"}, "", false},
		{"english stop word", grepInput{pattern: "version"}, "", false},
		{"english stop word 2", grepInput{pattern: "license"}, "", false},
		{"language keyword", grepInput{pattern: "package"}, "", false},
		{"multi-word text", grepInput{pattern: "connection refused"}, "", false},
		{"regex", grepInput{pattern: `foo.*bar`}, "", false},
		{"file name", grepInput{pattern: "main.go"}, "", false},
		{"targeted single file (absolute)", grepInput{pattern: "IngestAll", path: absFile}, "", false},
		{"targeted single file (relative to cwd)", grepInput{pattern: "IngestAll", path: "src/a.go"}, e.root, false},
		{"docs directory", grepInput{pattern: "IngestAll", path: "docs"}, e.root, false},
		{"path outside the repository", grepInput{pattern: "IngestAll", path: outside}, "", false},
		{"markdown glob", grepInput{pattern: "IngestAll", glob: "*.md"}, "", false},
		{"brace non-code glob", grepInput{pattern: "IngestAll", glob: "*.{json,yaml}"}, "", false},
		{"non-code type", grepInput{pattern: "IngestAll", typ: "json"}, "", false},
	}
	for i, c := range cases {
		session := "grep-" + string(rune('a'+i))
		out, exit := e.run("gate-grep.sh", grepPayload(session, c.cwd, c.in))
		assertDecision(t, c.name, out, exit, c.deny, c.in.pattern)
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
		{"rg --sort takes a value", "rg --sort modified IngestAll", true, "IngestAll"},
		{"rg -r is replace, not recursive", "rg -r Replacement IngestAll", true, "IngestAll"},
		{"rg --color takes a value", "rg --color always IngestAll", true, "IngestAll"},
		{"rg --iglob takes a value", `rg --iglob "*.go" IngestAll`, true, "IngestAll"},
		{"recursive grep", "grep -rn --include=*.go HandleRequest .", true, "HandleRequest"},
		{"git grep", "git grep -n ParseConfig", true, "ParseConfig"},
		{"ag symbol", "ag WriteBatch", true, "WriteBatch"},
		{"after cd into a code dir", "cd src && rg WriteBatch", true, "WriteBatch"},
		{"first of a pipeline", "rg IngestAll | head -20", true, "IngestAll"},
		{"quoted symbol", `rg "IngestAll"`, true, "IngestAll"},
		{"quoted definition probe", `grep -r "func HandleRequest" .`, true, "func HandleRequest"},
		{"explicit repo dir operand", "rg IngestAll .", true, "IngestAll"},
		{"no search tool", "ls -la && go test ./...", false, ""},
		{"merge is not a search", "git merge main", false, ""},
		{"piped filter", "git log | grep ParseConfig", false, ""},
		{"non-recursive grep", "grep ParseConfig src/a.go", false, ""},
		{"search of one file", "rg ParseConfig src/a.go", false, ""},
		{"search of one file after cd", "cd src && rg WriteBatch a.go", false, ""},
		{"search of docs", "rg ParseConfig docs/", false, ""},
		{"glob operand", "rg IngestAll src/*.go", false, ""},
		{"nonexistent operand", "rg IngestAll no-such-dir", false, ""},
		{"operand outside the repository", "rg NewClient /usr/include", false, ""},
		{"multi-word phrase", `rg "connection refused"`, false, ""},
		{"short word", "rg main", false, ""},
		{"english stop word", "rg -n version", false, ""},
		{"command substitution", `rg $(cat names.txt)`, false, ""},
		{"heredoc", "grep -r Foo <<EOF\nx\nEOF", false, ""},
	}
	for i, c := range cases {
		session := "bash-" + string(rune('a'+i))
		out, exit := e.run("gate-bash.sh", bashPayload(session, e.root, c.command))
		assertDecision(t, c.name, out, exit, c.deny, c.pattern)
	}
}

func TestSecondIdenticalAttemptIsAllowed(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	payload := grepPayload("retry", "", grepInput{pattern: "IngestAll"})
	out, exit := e.run("gate-grep.sh", payload)
	assertDecision(t, "first attempt", out, exit, true, "IngestAll")
	out, exit = e.run("gate-grep.sh", payload)
	assertDecision(t, "identical retry", out, exit, false, "")

	// A different search in the same session is still gated.
	out, exit = e.run("gate-grep.sh", grepPayload("retry", "", grepInput{pattern: "WriteBatch"}))
	assertDecision(t, "different pattern", out, exit, true, "WriteBatch")

	// A different session starts with a clean ledger.
	out, exit = e.run("gate-grep.sh", grepPayload("other-session", "", grepInput{pattern: "IngestAll"}))
	assertDecision(t, "other session", out, exit, true, "IngestAll")

	bash := bashPayload("retry", e.root, "rg IngestAll")
	out, exit = e.run("gate-bash.sh", bash)
	assertDecision(t, "bash first", out, exit, true, "IngestAll")
	out, exit = e.run("gate-bash.sh", bash)
	assertDecision(t, "bash retry", out, exit, false, "")
}

// TestRetryHasNoTimeWindow pins the documented promise: the identical second
// attempt is allowed for the life of the session, however long the agent spent
// on graphi in between.
func TestRetryHasNoTimeWindow(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	payload := grepPayload("slow", "", grepInput{pattern: "IngestAll"})
	out, exit := e.run("gate-grep.sh", payload)
	assertDecision(t, "first attempt", out, exit, true, "IngestAll")

	ledger := filepath.Join(e.tmp, "graphi-shunt-slow.log")
	b, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("ledger not written: %v", err)
	}
	var aged []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("unexpected ledger line %q", line)
		}
		aged = append(aged, "0 "+f[1])
	}
	if err := os.WriteFile(ledger, []byte(strings.Join(aged, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, exit = e.run("gate-grep.sh", payload)
	assertDecision(t, "retry hours later", out, exit, false, "")
}

func TestUnwritableLedgerFailsOpen(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, index: true})
	missing := filepath.Join(e.tmp, "does", "not", "exist")
	out, exit := e.run("gate-grep.sh", grepPayload("x", "", grepInput{pattern: "IngestAll"}), "TMPDIR="+missing)
	assertDecision(t, "unwritable ledger", out, exit, false, "")
}

func TestGatesFailOpen(t *testing.T) {
	payloads := map[string]string{
		"gate-grep.sh": grepPayload("fo", "", grepInput{pattern: "IngestAll"}),
		"gate-bash.sh": bashPayload("fo", "", "rg IngestAll"),
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

// TestBashPrefilterSpawnsNothingForPlainCommands pins the latency contract of
// the Bash gate: a command without a search tool must be dismissed by the
// pure-shell prefilter before jq (or anything else) is started, even when
// other payload fields contain substrings such as "package" or "merge".
func TestBashPrefilterSpawnsNothingForPlainCommands(t *testing.T) {
	e := newEnv(t, envOpts{graphi: true, jq: true, jqSpy: true, index: true})
	noisy := func(command string) string {
		b, _ := json.Marshal(map[string]any{
			"session_id":      "spy",
			"tool_name":       "Bash",
			"cwd":             filepath.Join(e.root, "storage", "merge-target"),
			"transcript_path": "/home/agent/.claude/projects/package-manager/transcript.jsonl",
			"tool_input":      map[string]string{"command": command, "description": "Stage the package and merge the image"},
		})
		return string(b)
	}
	out, exit := e.run("gate-bash.sh", noisy("ls -la"))
	assertDecision(t, "plain command", out, exit, false, "")
	if _, err := os.Stat(e.jqMarker); err == nil {
		t.Fatal("jq was spawned for a command without a search tool; the prefilter must look at the command only")
	}
	out, exit = e.run("gate-bash.sh", noisy("rg IngestAll"))
	assertDecision(t, "search command", out, exit, true, "IngestAll")
	if _, err := os.Stat(e.jqMarker); err != nil {
		t.Fatal("control: jq was expected to run for a search command")
	}
}

// TestShellFingerprintMatchesGo pins the shell copy of the store-path derivation
// to internal/state: the gate must find an index the Go side created for the
// repository root, whether the hook is handed that root with a trailing slash
// or a subdirectory of it (state.RepoRoot walks up to .git, go.work, go.mod).
func TestShellFingerprintMatchesGo(t *testing.T) {
	cases := []struct {
		name string
		opts envOpts
	}{
		{"root", envOpts{}},
		{"root with trailing slash", envOpts{trailing: true}},
		{"subdirectory of a git repo", envOpts{marker: ".git", subdir: filepath.Join("services", "api")}},
		{"subdirectory of a go module", envOpts{marker: "go.mod", subdir: "internal"}},
		{"subdirectory of a go workspace", envOpts{marker: "go.work", subdir: "cmd"}},
	}
	for _, c := range cases {
		c.opts.graphi, c.opts.jq, c.opts.index = true, true, true
		e := newEnv(t, c.opts)
		out, exit := e.run("gate-grep.sh", grepPayload("fp", "", grepInput{pattern: "IngestAll"}))
		assertDecision(t, "fingerprint parity ("+c.name+")", out, exit, true, "IngestAll")
	}
}
