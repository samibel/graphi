// Package canary — static zero-telemetry gate (SW-008 Slice 3).
//
// The gate scans the DEFAULT CGo-free build graph (CGO_ENABLED=0, default build
// tags, module root) for two classes of trust-breaking code:
//
//  1. Telemetry/analytics SDK imports — a curated denylist of import paths whose
//     presence in the default graph fails CI with the offending import.
//  2. Non-allowlisted outbound dial constructors — source-level AST scan for
//     net.Dial / net.DialUDP / http.Client that would reach non-loopback
//     destinations, against an explicit allowlist. The loopback-only surfaces
//     (the daemon Unix socket, local HTTP/SSE) are allowlisted.
//
// It invokes the Go toolchain via `go list`/`go vet`-style inspection (no
// golang.org/x/tools dependency, keeping the build graph lean) and is itself
// CGo-free. The graphi-broad CGo flavor is excluded: the gate only scans the
// default build graph, which is the artifact the local-first contract applies
// to.
package canary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/buildattest"
)

// StaticPrivacyGateID versions the source inputs and decision logic whose
// digest is emitted by RunGate and embedded into canonical release binaries.
const StaticPrivacyGateID = buildattest.PrivacyGateID

// TelemetryFinding names an import path that the gate has rejected.
type TelemetryFinding struct {
	Kind   string `json:"kind"`   // "telemetry-import" | "outbound-dial"
	Import string `json:"import"` // offending import path (telemetry) or package containing the call
	Symbol string `json:"symbol"` // offending symbol/call (outbound-dial) or ""
	File   string `json:"file"`   // source file, when known
	Reason string `json:"reason"`
}

// telemetryImportDenylist is the curated set of telemetry/analytics SDK import
// path prefixes. A default-graph dependency matching any prefix fails the gate.
// This is necessary-but-not-sufficient (refinement S1): the outbound-dial scan
// catches http.Client-based telemetry without an SDK import.
var telemetryImportDenylist = []string{
	"go.opentelemetry.io/otel", // tracing/metrics SDK family
	"go.opentelemetry.io/contrib",
	"contrib.go.opencensus.io",
	"go.opencensus.io",
	"github.com/amplitude",
	"github.com/segmentio/analytics-go",
	"github.com/mixpanel",
	"github.com/getsentry/sentry-go",
	"github.com/posthog",
	"gopkg.in/segmentio",
	"github.com/DataDog/datadog-go", // agent telemetry (the datadog *metrics* story is SW-014's explicit, allowlisted path)
}

// outboundDialAllowlist is the explicit, reviewed set of package paths permitted
// to construct outbound network dials in the default graph. Everything else
// introducing net.Dial / net.DialUDP / http.Client{(Timeout:,Transport:...)} /
// http.Get etc. fails the gate with the offending symbol + file.
//
// graphi is local-first by contract: the ONLY legitimate network use is
// loopback (daemon Unix socket, local HTTP/SSE). Those live in surfaces/daemon
// and surfaces/* and are allowlisted. The opt-in graphi-broad CGo flavor is on a
// separate track and not in the default graph.
var outboundDialAllowlist = []string{
	"github.com/samibel/graphi/surfaces/daemon", // Unix-socket IPC (local)
	"github.com/samibel/graphi/surfaces/client", // in-process + daemon socket client (local)
	"github.com/samibel/graphi/surfaces/http",   // loopback-only HTTP/SSE surface; ListenLoopback binds only after AssertLoopback (SW-044)
	// surfaces/guard is the SW-099 zero-egress enforcement chokepoint itself: its
	// ListenLoopback refuses any non-loopback bind before opening a socket, and its
	// NoEgressDialer is a DEFAULT-DENY dialer that rejects every non-loopback
	// outbound dial. The net.Listen/net.Dialer it contains ARE the egress-control
	// mechanism, loopback-by-construction; its own guard_test asserts non-loopback
	// binds and external dials are refused. Allowlisting the guard is what lets
	// every other transport route through one audited policy.
	"github.com/samibel/graphi/surfaces/guard",
	"github.com/samibel/graphi/internal/canary", // the canary itself records/observes dials by design
	// engine/review is the GitHub PR-review surface (SW-043/EP-007). Its egress is
	// confined to githubhost.go — the single, documented, intentional outbound
	// boundary (user-invoked PR comments, not telemetry). engine/review's own
	// TestGitHubHostIsSoleNetworkUser guards that githubhost.go stays the ONLY
	// net/http importer in the package, so allowlisting the package cannot mask a
	// new, unintended egress sneaking in elsewhere.
	"github.com/samibel/graphi/engine/review",
	// engine/embed/ollama is the OPT-IN, LOOPBACK-ONLY embedder (SW-059). It dials
	// only the local Ollama endpoint and validates the host fail-closed at
	// construction (assertLoopbackEndpoint rejects any non-loopback host), so its
	// egress is loopback-by-construction and never reached on the default path
	// (the GRAPHI_EMBEDDER selector is empty by default, so the embedder is never
	// constructed). Allowlisting the package is the registration-layer analog of
	// the surfaces/http loopback-only entry: its own test asserts loopback-only.
	"github.com/samibel/graphi/engine/embed/ollama",
	// surfaces/forge is the SW-105/EP-018 read-only PR-enumeration boundary — the
	// multi-PR triage suite's single, documented, intentional outbound path
	// (user-invoked PR discovery/metadata over the GitHub REST API, not telemetry).
	// It is a SURFACE-layer ingestion client: it lists open PRs and fetches their
	// metadata via GET only; it posts/mutates nothing and performs no scoring.
	// Confining the enumeration egress here is what keeps the engine `triage-prs`
	// analyzer it feeds strictly zero-outbound. The Enumerator seam is injectable so
	// tests drive an in-memory MockForge and do zero network I/O; the real
	// GitHubForge is the only dialer (mirrors engine/review's single egress).
	"github.com/samibel/graphi/surfaces/forge",
	// cmd/graphi/staticfetch is the static-embedder supply-chain surface
	// (SW-262). Its ONLY outbound network call is the
	// `graphi setup-embedder static:<model>@<revision>` command, which
	// downloads the pinned static-embedder artifact over HTTPS and
	// validates its SHA-256 against the in-tree pin table. The
	// download is user-invoked, fail-closed, and structurally isolated
	// from the rest of the default graph: engine/embed/static is
	// net-free (its own test fails if net/http ever re-appears there),
	// surfaces/cli is net-free, and the rest of cmd/graphi is also
	// net-free. TestStaticfetch_IsTheOnlyNetworkCallerInCmdGraphi walks
	// the whole repository and enforces that setup.go is the sole caller
	// of Download/InstallLocal, including through an import alias or
	// function value. The narrower package boundary lets this canary
	// catch a direct dial added to `index`, `search`, MCP, or HTTP; the
	// call-site guard catches an indirect reach into this exemption.
	"github.com/samibel/graphi/cmd/graphi/staticfetch",
}

// outboundDialCallDenylist names the dial constructors the AST scan flags when
// found outside the allowlist. It targets the ACTUAL egress mechanisms, not
// non-I/O constructors:
//   - net.* Dial/Listen (raw socket egress / bind surface)
//   - http.{Get,Post,PostForm,Head} (package-level convenience calls that dial
//     via http.DefaultClient)
//
// The primary HTTP egress path is http.Client.Do / Send (and the package-level
// DefaultClient.* aliases), which are handled by scanOutboundDials via
// selector-on-Client detection below — NOT listed here, because their receiver
// must be type-resolved, not just name-matched. http.NewRequest is intentionally
// absent: it constructs a request and performs no I/O, so flagging it would be a
// false positive.
var outboundDialCallDenylist = []string{
	"net.Dial",
	"net.DialTCP",
	"net.DialUDP",
	"net.DialIP",
	"net.Listen", // a non-loopback Listen is also an egress surface; allowed only if bound to loopback
	"http.Get",
	"http.Post",
	"http.PostForm",
	"http.Head",
}

// httpClientEgressMethods are method names that, when called on a value of type
// *http.Client (or the http.DefaultClient package-level identifier), constitute
// the real HTTP egress mechanism. scanOutboundDials flags any such call outside
// the allowlist. (Review F1/F2 fix: the previous version flagged the
// non-I/O http.NewRequest constructor and missed Do/Send entirely — the
// dangerous false-negative direction for a security gate.)
var httpClientEgressMethods = map[string]bool{
	"Do":   true, // (*http.Client).Do — the canonical request egress
	"Send": true, // (*http.Transport).Send / custom client senders
	"Get":  true, // (*http.Client).Get
	"Post": true, // (*http.Client).Post
	"Head": true, // (*http.Client).Head
}

// GateConfig configures the static gate. ModuleDir defaults to the graphi
// module root (detected via `go env GOMOD` when empty).
type GateConfig struct {
	ModuleDir string
	// BuildTags selects the exact dependency graph linked into the candidate.
	BuildTags []string
	GOOS      string
	GOARCH    string
	// GraphCommand returns the default-graph dependency list. Defaults to
	// `go list -deps -test=false ./...` under CGO_ENABLED=0; injectable for
	// hermetic tests.
	GraphCommand func(dir string) ([]string, error)
}

// GateResult is the static-gate verdict.
type GateResult struct {
	Verdict        string             `json:"verdict"` // "pass" | "fail"
	Findings       []TelemetryFinding `json:"findings"`
	EvidenceDigest string             `json:"evidence_digest"`
	BuildTags      []string           `json:"build_tags,omitempty"`
	GOOS           string             `json:"goos"`
	GOARCH         string             `json:"goarch"`
}

// RunGate executes the static zero-telemetry gate over the default graph.
func RunGate(cfg GateConfig) (GateResult, error) {
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.GOARCH == "" {
		cfg.GOARCH = runtime.GOARCH
	}
	defaultCommand := cfg.GraphCommand == nil
	if cfg.GraphCommand == nil {
		cfg.GraphCommand = func(dir string) ([]string, error) {
			return graphDeps(dir, cfg.BuildTags, cfg.GOOS, cfg.GOARCH)
		}
	}
	if defaultCommand {
		root, err := ResolveModuleDir(cfg.ModuleDir)
		if err != nil {
			return GateResult{Verdict: "pass"}, err
		}
		cfg.ModuleDir = root
	} else if cfg.ModuleDir == "" {
		cfg.ModuleDir = "."
	}

	tags := append([]string(nil), cfg.BuildTags...)
	sort.Strings(tags)
	res := GateResult{Verdict: "pass", BuildTags: tags, GOOS: cfg.GOOS, GOARCH: cfg.GOARCH}

	deps, err := cfg.GraphCommand(cfg.ModuleDir)
	if err != nil {
		return res, fmt.Errorf("canary gate: resolve default graph: %w", err)
	}
	res.EvidenceDigest, err = staticEvidenceDigest(cfg.ModuleDir, deps, tags, cfg.GOOS, cfg.GOARCH)
	if err != nil {
		return res, fmt.Errorf("canary gate: digest static evidence: %w", err)
	}

	// (1) Telemetry-import scan.
	for _, dep := range deps {
		if f := matchTelemetryImport(dep); f != nil {
			res.Findings = append(res.Findings, *f)
		}
	}

	// (2) Outbound-dial AST scan over the module's own source (deps are stdlib +
	// vendored libs; only graphi's own code should be introducing dials). We
	// walk .go files under ModuleDir excluding _test.go and the canary itself
	// unless allowlisted.
	ownPkgDials, err := scanOutboundDials(cfg.ModuleDir)
	if err != nil {
		return res, fmt.Errorf("canary gate: scan outbound dials: %w", err)
	}
	res.Findings = append(res.Findings, ownPkgDials...)

	if len(res.Findings) > 0 {
		res.Verdict = "fail"
	}
	sortFindings(res.Findings)
	return res, nil
}

// ResolveModuleDir returns the graphi source root required by the static gate.
// It rejects arbitrary Go modules: a PASS about a different checkout would not
// describe graphi and is more dangerous than an explicit unavailable result.
func ResolveModuleDir(dir string) (string, error) {
	if dir == "" {
		out, err := exec.Command("go", "env", "GOMOD").Output()
		if err != nil {
			return "", fmt.Errorf("source scan unavailable: the Go toolchain and the graphi source module are required")
		}
		gomod := strings.TrimSpace(string(out))
		if gomod == "" || gomod == os.DevNull || gomod == "/dev/null" {
			return "", fmt.Errorf("source scan unavailable: run from the graphi source module or select an official build attestation")
		}
		dir = filepath.Dir(gomod)
	}
	gomodPath := filepath.Join(dir, "go.mod")
	raw, err := os.ReadFile(gomodPath)
	if err != nil {
		return "", fmt.Errorf("source scan unavailable: %s is not the graphi source module", dir)
	}
	const want = "module github.com/samibel/graphi"
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			found = line == want
			break
		}
	}
	if !found {
		return "", fmt.Errorf("source scan unavailable: %s is not module github.com/samibel/graphi", dir)
	}
	return filepath.Clean(dir), nil
}

// matchTelemetryImport returns a finding if dep matches the denylist.
func matchTelemetryImport(dep string) *TelemetryFinding {
	for _, prefix := range telemetryImportDenylist {
		if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
			return &TelemetryFinding{
				Kind:   "telemetry-import",
				Import: dep,
				Reason: "telemetry/analytics SDK import in the default build graph violates the zero-telemetry local-first contract",
			}
		}
	}
	return nil
}

// defaultGraphDeps returns the default-graph dependency list under
// CGO_ENABLED=0.
func defaultGraphDeps(dir string) ([]string, error) {
	return graphDeps(dir, nil, runtime.GOOS, runtime.GOARCH)
}

func graphDeps(dir string, buildTags []string, goos, goarch string) ([]string, error) {
	args := []string{"list", "-deps", "-test=false"}
	if len(buildTags) > 0 {
		args = append(args, "-tags", strings.Join(buildTags, ","))
	}
	args = append(args, "./...")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps: %w", err)
	}
	var deps []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			deps = append(deps, line)
		}
	}
	return deps, nil
}

// staticEvidenceDigest binds the result to the dependency names, module files,
// and non-test Go source the gate inspected. It is deterministic across
// checkouts and gives a sceptical user a value they can reproduce from the
// attested source revision without trusting the running binary's prose.
func staticEvidenceDigest(root string, deps, buildTags []string, goos, goarch string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "gate\x00%s\n", StaticPrivacyGateID)
	fmt.Fprintf(h, "target\x00%s/%s\n", goos, goarch)
	sortedTags := append([]string(nil), buildTags...)
	sort.Strings(sortedTags)
	for _, tag := range sortedTags {
		fmt.Fprintf(h, "tag\x00%s\n", tag)
	}
	sortedDeps := append([]string(nil), deps...)
	sort.Strings(sortedDeps)
	for _, dep := range sortedDeps {
		fmt.Fprintf(h, "dep\x00%s\n", dep)
	}

	files, err := graphiSourceFiles(root)
	if err != nil {
		return "", err
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(root, name)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", fmt.Errorf("relative path for %s: %w", path, err)
		}
		sum := sha256.Sum256(raw)
		fmt.Fprintf(h, "file\x00%s\x00%s\n", filepath.ToSlash(rel), hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// scanOutboundDials walks .go files (non-test) under root, type-checks each
// graphi package with the stdlib source importer, and flags egress calls
// outside the allowlist. Type resolution (go/types) is what makes this a real
// security gate rather than a name-guessing heuristic: (*http.Client).Do is
// caught regardless of the receiver variable's name. (Review F1/F2 fix.)
//
// Type-check failures fall back to a conservative AST name-based pass so a
// package that does not type-check cleanly is still inspected.
func scanOutboundDials(root string) ([]TelemetryFinding, error) {
	var findings []TelemetryFinding
	pkgDirs, err := graphiPackageDirs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range pkgDirs {
		fs := token.NewFileSet()
		pkgs, perr := parser.ParseDir(fs, dir, nonTestInfoFilter, parser.ParseComments)
		if perr != nil {
			continue
		}
		for pkgName, pkg := range pkgs {
			if strings.HasSuffix(pkgName, "_test") {
				continue
			}
			fileList := make([]*ast.File, 0, len(pkg.Files))
			for _, f := range pkg.Files {
				fileList = append(fileList, f)
			}
			pkgPath := dirToPkgPath(root, dir)
			allowed := isAllowlistedPkg(pkgPath)
			if allowed {
				continue
			}
			if !importsHTTP(fileList) {
				// Fast path: a package that does not import net/http cannot be
				// doing HTTP egress. net.* dial constructors are still checked.
				findings = append(findings, scanPackageAST(fs, fileList, pkgPath, nil)...) //nolint:gocritic // intentional append
				continue
			}
			info := &types.Info{
				Types:      make(map[ast.Expr]types.TypeAndValue),
				Defs:       make(map[*ast.Ident]types.Object),
				Uses:       make(map[*ast.Ident]types.Object),
				Selections: make(map[*ast.SelectorExpr]*types.Selection),
			}
			conf := types.Config{Importer: importer.ForCompiler(fs, "source", nil), Error: func(error) {}}
			_, _ = conf.Check(pkgPath, fs, fileList, info) // best-effort; partial info still useful
			findings = append(findings, scanPackageAST(fs, fileList, pkgPath, info)...)
		}
	}
	return findings, nil
}

// importsHTTP reports whether any file in the package imports "net/http".
func importsHTTP(files []*ast.File) bool {
	for _, f := range files {
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, "\"") == "net/http" {
				return true
			}
		}
	}
	return false
}

// nonTestInfoFilter is the parser.ParseDir filter: parse non-test files only.
func nonTestInfoFilter(fi os.FileInfo) bool {
	return !strings.HasSuffix(fi.Name(), "_test.go")
}

// scanPackageAST inspects one package's files and produces findings. When info
// is non-nil it uses type resolution for precise http.Client detection; the
// net.* and http.* package-level checks are always name-based (they are
// unambiguous).
func scanPackageAST(fs *token.FileSet, files []*ast.File, pkgPath string, info *types.Info) []TelemetryFinding {
	var findings []TelemetryFinding
	for _, file := range files {
		filename := fs.Position(file.Pos()).Filename
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// (1) Package-level qualified calls: net.Dial, http.Get, etc.
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				qualified := pkgIdent.Name + "." + sel.Sel.Name
				for _, deny := range outboundDialCallDenylist {
					if qualified == deny {
						findings = append(findings, egressFinding(pkgPath, filename, qualified))
					}
				}
			}
			// (2) http.Client / http.DefaultClient egress (Do/Get/Post/Head/Send).
			if httpClientEgressMethods[sel.Sel.Name] && isHTTPClientCall(sel, info) {
				findings = append(findings, egressFinding(pkgPath, filename, "http.Client."+sel.Sel.Name))
			}
			return true
		})
	}
	return findings
}

func egressFinding(pkgPath, filename, symbol string) TelemetryFinding {
	return TelemetryFinding{
		Kind:   "outbound-dial",
		Import: pkgPath,
		Symbol: symbol,
		File:   filename,
		Reason: "non-allowlisted outbound dial/HTTP egress in default graph; only loopback surfaces (surfaces/daemon, surfaces/client) may dial, and only to loopback",
	}
}

// isHTTPClientCall reports whether sel is a method call on an *http.Client
// (or http.Client) value, or the http.DefaultClient package identifier. When
// type info is available it resolves the receiver type precisely; otherwise it
// falls back to conservative name heuristics.
func isHTTPClientCall(sel *ast.SelectorExpr, info *types.Info) bool {
	if info != nil {
		if selection, ok := info.Selections[sel]; ok {
			recv := selection.Recv()
			if isHTTPClientType(recv) {
				return true
			}
		}
		// Also handle the http.DefaultClient.Get(...) shape: callee is a
		// package-level *types.Func (a Var) in net/http named DefaultClient.
		if id, ok := sel.X.(*ast.SelectorExpr); ok {
			if obj, ok := info.Uses[id.Sel]; ok {
				if v, ok := obj.(*types.Var); ok && v.Pkg() != nil {
					// fall through to name heuristic for DefaultClient
				}
			}
		}
	}
	return isHTTPClientReceiver(sel.X)
}

// isHTTPClientType reports whether t is *http.Client or http.Client.
func isHTTPClientType(t types.Type) bool {
	if t == nil {
		return false
	}
	ptr, ok := t.(*types.Pointer)
	if ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "net/http" && obj.Name() == "Client"
}

// graphiPackageDirs returns the directories under root that contain non-test
// .go files of graphi's own packages (excluding vendor/.git/node_modules and
// the canary's own build output).
func graphiPackageDirs(root string) ([]string, error) {
	files, err := graphiSourceFiles(root)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, path := range files {
		d := filepath.Dir(path)
		if !contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}

func graphiSourceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if fi.IsDir() {
			name := fi.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files, err
}

// dirToPkgPath returns the module-relative package path for a directory.
func dirToPkgPath(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return ""
	}
	return "github.com/samibel/graphi/" + filepath.ToSlash(rel)
}

// isAllowlistedPkg reports whether pkgPath is permitted to dial (loopback
// surfaces + the canary itself).
func isAllowlistedPkg(pkgPath string) bool {
	for _, a := range outboundDialAllowlist {
		if pkgPath == a || strings.HasPrefix(pkgPath, a+"/") {
			return true
		}
	}
	return false
}

// isHTTPClientReceiver reports whether expr is plausibly an *http.Client value
// (or the http.DefaultClient package identifier). This is the FALLBACK used when
// type info is unavailable; the type-resolved path (isHTTPClientType) is
// preferred. (Review F1/F2.)
func isHTTPClientReceiver(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok && pkg.Name == "http" && e.Sel.Name == "DefaultClient" {
			return true
		}
		return httpClientishName(e.Sel.Name)
	case *ast.Ident:
		return httpClientishName(e.Name)
	}
	return false
}

// httpClientishName reports whether a name conventionally refers to an
// *http.Client value (fallback heuristic only).
func httpClientishName(name string) bool {
	switch name {
	case "client", "Client", "httpClient", "HttpClient", "http", "DefaultClient":
		return true
	}
	return false
}

func sortFindings(f []TelemetryFinding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].Kind != f[j].Kind {
			return f[i].Kind < f[j].Kind
		}
		return f[i].Import < f[j].Import
	})
}
