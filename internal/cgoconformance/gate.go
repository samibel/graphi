// Package cgoconformance implements the CGo-free build conformance gate for the
// default graphi binary (story SW-009). It is a CI concern that lives outside the
// surfaces/engine layers: it builds and inspects the default graph
// (CGO_ENABLED=0, default build tags, ./cmd/graphi) and asserts:
//
//   - the default binary compiles with CGO_ENABLED=0;
//   - the default test suite passes under CGO_ENABLED=0;
//   - CGO_ENABLED=0 is actually in effect for the build (not leaked from host);
//   - the produced binary has zero cgo-introduced dynamic C dependencies
//     (statically linked on linux; carried by the CGO_ENABLED=0 build setting on
//     darwin/other platforms where a fully-static binary is unattainable);
//   - the default import graph contains no cgo-requiring packages (a regression
//     detector that names any offending package);
//   - the opt-in `graphi-broad` CGO flavor is explicitly excluded via a named,
//     documented condition on its separate track and never affects this gate.
//
// The gate emits a single named check ("cgo-free-conformance") consumed by CI.
//
// Layering note: this package imports only the Go standard library. It never
// imports surfaces/engine/core, so it cannot pull a cgo dependency into the
// default graph itself.
package cgoconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CheckName is the distinct, named CI check emitted by this gate. It appears as
// a dedicated, visible check in the build summary (AC: named check).
const CheckName = "cgo-free-conformance"

// ExcludedBroadFlavor is the named, documented opt-in CGO flavor that this gate
// deliberately excludes. It has its own separate conformance track; its presence
// must never affect this gate's pass/fail result (AC: graphi-broad exclusion).
//
// This is the human-facing flavor NAME ("graphi-broad"). Note that the Go BUILD
// TAG is the underscore form ExcludedBroadBuildTag ("graphi_broad") because `-` is
// illegal in a Go build constraint (SW-056 DN-2). Import paths that vendor the
// flavor name use the hyphen form; the build tag uses the underscore form. Both
// are recognized by IsBroadFlavor / SanitizeGoFlags so the default-graph gate's
// broad-strip never silently no-ops on the real `-tags graphi_broad` flag.
const ExcludedBroadFlavor = "graphi-broad"

// ExcludedBroadBuildTag is the Go BUILD TAG identifier for the opt-in graphi-broad
// CGO flavor (SW-056). It is the underscore spelling of ExcludedBroadFlavor because
// `-` is illegal in a Go build constraint. The graphi-broad parser bundle is gated
// `//go:build graphi_broad` and built with `-tags graphi_broad`; SanitizeGoFlags
// strips exactly this tag from any GOFLAGS the default-graph gate inherits, and
// IsBroadFlavor recognizes it in an import-path/tag string.
const ExcludedBroadBuildTag = "graphi_broad"

// ForestModulePath is the CGO tree-sitter grammar bundle (go-sitter-forest) that
// belongs ONLY to the opt-in graphi-broad flavor (SW-056). It is wholly CGO
// (import "C" + parser.c) and MUST NEVER appear in the default build's import
// graph. The static "forest unreachable" assertion (SW-055 AC#2/AC#4) is the
// import-graph complement to core/parse's registration-level no-CGO guard:
// defense-in-depth across the build layer and the runtime-registration layer.
const ForestModulePath = "go-sitter-forest"

// DefaultBuildTarget is the canonical default-graph build target, shared with
// SW-008 (static gate) and SW-013 (packaging). It is the single definition of
// "default graph" and MUST NOT be redefined elsewhere.
const DefaultBuildTarget = "./cmd/graphi/"

// Status is the outcome of a named check.
type Status string

const (
	// StatusPass means the conformance gate passed.
	StatusPass Status = "PASS"
	// StatusFail means the conformance gate failed; Reason carries the cause.
	StatusFail Status = "FAIL"
)

// Result is the named-check record emitted by Run. It is machine-readable JSON
// so CI can consume it deterministically (AC: distinct named check).
type Result struct {
	Name           string    `json:"name"`             // always CheckName
	Status         Status    `json:"status"`           // PASS | FAIL
	CheckTime      time.Time `json:"check_time"`       // UTC
	CGOEnabled     string    `json:"cgo_enabled"`      // enforced value
	Target         string    `json:"target"`           // build target
	Binary         string    `json:"binary,omitempty"` // inspected binary path
	BuildOK        bool      `json:"build_ok"`         // build under CGO_ENABLED=0 succeeded
	TestOK         bool      `json:"test_ok"`          // default suite passed under CGO_ENABLED=0
	StaticLinked   bool      `json:"static_linked"`    // zero cgo-introduced dynamic C deps
	CgoPackages    []string  `json:"cgo_packages"`     // offenders (should be empty)
	ForestPackages []string  `json:"forest_packages"`  // go-sitter-forest offenders (should be empty)
	ExcludedFlavor string    `json:"excluded_flavor"`  // named excluded flavor
	Reason         string    `json:"reason,omitempty"` // failure cause (if any)
}

// GateConfig parameterizes a conformance run.
type GateConfig struct {
	// Target is the default-graph build target (default: DefaultBuildTarget).
	Target string
	// TestTarget is the test selector run under CGO_ENABLED=0 (default: "./...").
	TestTarget string
	// CGOEnabled is the value enforced for build+test+inspect (default: "0").
	CGOEnabled string
	// BinaryOut is where the default binary is built for linkage inspection.
	// If empty, a temp file is used and removed after the run.
	BinaryOut string
	// Stdout receives human-readable progress lines (default: discarded).
	Stdout io.Writer
}

func (c *GateConfig) defaults() {
	if c.Target == "" {
		c.Target = DefaultBuildTarget
	}
	if c.TestTarget == "" {
		c.TestTarget = "./..."
	}
	if c.CGOEnabled == "" {
		c.CGOEnabled = "0"
	}
	if c.Stdout == nil {
		c.Stdout = io.Discard
	}
}

// IsBroadFlavor reports whether pkg (an import path or build-tag string) belongs
// to the opt-in graphi-broad CGO flavor that this gate explicitly excludes. This
// is the named, documented exclusion condition required by the AC. It recognizes
// BOTH the flavor name ("graphi-broad", as it may appear in an import path) AND the
// underscore build-tag form ("graphi_broad", SW-056 DN-2) so the exclusion never
// silently misses the real `-tags graphi_broad` flag.
func IsBroadFlavor(pkg string) bool {
	if pkg == "" {
		return false
	}
	return strings.Contains(pkg, ExcludedBroadFlavor) || strings.Contains(pkg, ExcludedBroadBuildTag)
}

// EffectiveCgoEnabled reports the value of CGO_ENABLED that `go env` resolves
// under the given environment. Used to assert the flag is actually in effect and
// not silently leaked from the host environment (AC: assert CGO_ENABLED in
// effect, not leaked).
func EffectiveCgoEnabled(ctx context.Context, cgoEnabled string) (string, error) {
	out, err := goOutput(ctx, []string{"env", "CGO_ENABLED"}, cgoEnabled)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CgoUsingPackages returns the import paths of packages reachable from target
// that use cgo (non-empty CgoFiles), EXCLUDING any package that belongs to the
// opt-in graphi-broad flavor. A non-empty result is a regression: a
// cgo-dependent import entered the default build graph. The offending package
// paths are returned verbatim so CI can name them (AC: regression names the
// offending package).
func CgoUsingPackages(ctx context.Context, target, cgoEnabled string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json", target)
	cmd.Env = withCgoEnv(cgoEnabled)
	if dir, err := moduleRoot(); err == nil {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("go list: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	// `go list -deps -json` emits a stream of concatenated JSON objects (one per
	// package), not a JSON array; decode in a loop.
	dec := json.NewDecoder(stdout)
	var offenders []string
	for {
		var p struct {
			ImportPath string
			CgoFiles   []string
		}
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			_ = cmd.Wait()
			return nil, fmt.Errorf("decode go list json: %w", err)
		}
		if len(p.CgoFiles) == 0 {
			continue
		}
		if IsBroadFlavor(p.ImportPath) {
			continue // excluded named flavor, separate track
		}
		offenders = append(offenders, p.ImportPath)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("go list failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return offenders, nil
}

// ForestReachablePackages returns the import paths reachable from target whose
// path contains the go-sitter-forest module (the CGO grammar bundle). For the
// default build this MUST be empty: go-sitter-forest is wholly CGO and belongs
// only to the opt-in graphi-broad flavor (SW-056). A non-empty result is a
// regression that names the offending package(s).
//
// This is the static, import-graph half of "go-sitter-forest is never reachable
// from the default build" (SW-055 AC#2/AC#4); it complements the
// registration-level no-CGO guard in core/parse (AssertPureGoDefaults). It scans
// EVERY reachable package, not only cgo-using ones, so it catches a forest import
// even before its cgo files would surface in the CgoUsingPackages scan.
func ForestReachablePackages(ctx context.Context, target, cgoEnabled string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", target)
	cmd.Env = withCgoEnv(cgoEnabled)
	if dir, err := moduleRoot(); err == nil {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var offenders []string
	for _, line := range strings.Split(string(out), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" {
			continue
		}
		if strings.Contains(pkg, ForestModulePath) {
			offenders = append(offenders, pkg)
		}
	}
	return offenders, nil
}

// FormatForestReachableFailure renders a clear failure message naming the
// go-sitter-forest packages that wrongly entered the default build graph.
func FormatForestReachableFailure(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"[%s] go-sitter-forest (CGO grammar bundle) reachable from the default build graph: %s — go-sitter-forest belongs only to the opt-in %s flavor and must never reach %s",
		CheckName, strings.Join(pkgs, ", "), ExcludedBroadFlavor, DefaultBuildTarget,
	)
}

// FormatCgoImportFailure renders a clear, machine-and-human-readable message
// naming the offending cgo-dependent packages for a failing gate (AC).
func FormatCgoImportFailure(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"[%s] cgo-dependent import(s) entered the default build graph: %s — these packages use cgo and must not be reachable from %s",
		CheckName, strings.Join(pkgs, ", "), DefaultBuildTarget,
	)
}

// BuildBinary builds target under CGO_ENABLED=cgoEnabled into outPath and
// returns the combined build output.
func BuildBinary(ctx context.Context, target, cgoEnabled, outPath string) ([]byte, error) {
	return goOutput(ctx, []string{"build", "-o", outPath, target}, cgoEnabled)
}

// RunTestSuite runs `go test target` under CGO_ENABLED=cgoEnabled and returns
// the combined output.
func RunTestSuite(ctx context.Context, target, cgoEnabled string) ([]byte, error) {
	return goOutput(ctx, []string{"test", target}, cgoEnabled)
}

// AssertStaticLinkage inspects bin for cgo-introduced dynamic C dependencies.
//
// On linux it additionally requires `file` to report "statically linked". On
// darwin and other platforms a fully-static binary is not attainable (the
// platform mandates a dynamic link to its system library), so the guarantee is
// carried by the CGO_ENABLED=0 build setting instead. The semantics are
// identical across platforms: zero cgo-introduced dynamic C dependencies.
func AssertStaticLinkage(ctx context.Context, bin, cgoEnabled string) (ok bool, detail string, err error) {
	ver, err := goOutput(ctx, []string{"version", "-m", bin}, cgoEnabled)
	if err != nil {
		return false, "", fmt.Errorf("go version -m: %w", err)
	}
	vers := string(ver)
	want := "CGO_ENABLED=" + cgoEnabled
	if !strings.Contains(vers, want) {
		return false, fmt.Sprintf("build settings missing %q in `go version -m`", want), nil
	}
	if runtime.GOOS == "linux" {
		if file, err := exec.LookPath("file"); err == nil {
			out, _ := exec.CommandContext(ctx, file, bin).Output()
			if !strings.Contains(string(out), "statically linked") {
				return false, fmt.Sprintf("`file` reports non-static: %s", strings.TrimSpace(string(out))), nil
			}
		}
	}
	return true, "cgo-free linkage confirmed: build setting " + want, nil
}

// Run executes the full conformance gate and returns the named-check Result. The
// gate stops at the first failing assertion and records the cause in Reason.
func Run(ctx context.Context, cfg GateConfig) Result {
	cfg.defaults()
	res := Result{
		Name:           CheckName,
		Status:         StatusFail,
		CheckTime:      time.Now().UTC(),
		CGOEnabled:     cfg.CGOEnabled,
		Target:         cfg.Target,
		ExcludedFlavor: ExcludedBroadFlavor,
	}
	fmt.Fprintf(cfg.Stdout, "[%s] target=%s cgo_enabled=%s excluded_flavor=%s\n",
		CheckName, cfg.Target, cfg.CGOEnabled, ExcludedBroadFlavor)

	// (1) Assert the flag is actually in effect (not leaked from host env).
	eff, err := EffectiveCgoEnabled(ctx, cfg.CGOEnabled)
	if err != nil {
		res.Reason = "effective cgo check failed: " + err.Error()
		return res
	}
	if eff != cfg.CGOEnabled {
		res.Reason = fmt.Sprintf("CGO_ENABLED leaked from host: effective=%s want=%s", eff, cfg.CGOEnabled)
		return res
	}

	// (2) Build the default binary under CGO_ENABLED=0.
	bin := cfg.BinaryOut
	cleanup := false
	if bin == "" {
		f, ferr := os.CreateTemp("", "graphi-cgoconf-*")
		if ferr != nil {
			res.Reason = "temp binary: " + ferr.Error()
			return res
		}
		bin = f.Name()
		_ = f.Close()
		cleanup = true
		defer func() {
			if cleanup {
				_ = os.Remove(bin)
			}
		}()
	}
	if out, err := BuildBinary(ctx, cfg.Target, cfg.CGOEnabled, bin); err != nil {
		res.Reason = "build failed under CGO_ENABLED=" + cfg.CGOEnabled + ": " + strings.TrimSpace(string(out))
		return res
	}
	res.Binary = bin
	res.BuildOK = true
	fmt.Fprintf(cfg.Stdout, "[%s] build OK -> %s\n", CheckName, bin)

	// (3) Static linkage on the produced binary.
	ok, detail, err := AssertStaticLinkage(ctx, bin, cfg.CGOEnabled)
	if err != nil {
		res.Reason = "linkage inspect failed: " + err.Error()
		return res
	}
	res.StaticLinked = ok
	if !ok {
		res.Reason = "static linkage assertion failed: " + detail
		return res
	}
	fmt.Fprintf(cfg.Stdout, "[%s] linkage OK: %s\n", CheckName, detail)

	// (4) cgo-import regression scan over the default graph.
	offenders, err := CgoUsingPackages(ctx, cfg.Target, cfg.CGOEnabled)
	if err != nil {
		res.Reason = "cgo scan failed: " + err.Error()
		return res
	}
	res.CgoPackages = offenders
	if len(offenders) > 0 {
		res.Reason = FormatCgoImportFailure(offenders)
		return res
	}
	fmt.Fprintf(cfg.Stdout, "[%s] cgo-import scan OK (no offenders; %s excluded)\n", CheckName, ExcludedBroadFlavor)

	// (4b) Static "go-sitter-forest unreachable" scan over the default graph
	// (SW-055 AC#2/AC#4). The CGO grammar bundle must not appear in the default
	// import graph; this is the build-layer complement to core/parse's
	// registration-level no-CGO guard. Release-blocking.
	forest, err := ForestReachablePackages(ctx, cfg.Target, cfg.CGOEnabled)
	if err != nil {
		res.Reason = "forest scan failed: " + err.Error()
		return res
	}
	res.ForestPackages = forest
	if len(forest) > 0 {
		res.Reason = FormatForestReachableFailure(forest)
		return res
	}
	fmt.Fprintf(cfg.Stdout, "[%s] forest-unreachable scan OK (%s not in default graph)\n", CheckName, ForestModulePath)

	// (5) Default test suite under CGO_ENABLED=0.
	if out, err := RunTestSuite(ctx, cfg.TestTarget, cfg.CGOEnabled); err != nil {
		res.Reason = "default test suite failed under CGO_ENABLED=" + cfg.CGOEnabled + ": " + strings.TrimSpace(string(out))
		return res
	}
	res.TestOK = true
	fmt.Fprintf(cfg.Stdout, "[%s] default test suite OK under CGO_ENABLED=%s\n", CheckName, cfg.CGOEnabled)

	res.Status = StatusPass
	fmt.Fprintf(cfg.Stdout, "[%s] %s\n", CheckName, res.Status)
	return res
}

// goOutput runs a `go <args...>` command under a CGO_ENABLED=cgoEnabled env,
// returning combined output. The command runs from the module root so relative
// targets (e.g. ./cmd/graphi/) resolve regardless of the caller's CWD. The env
// is sanitized so the opt-in graphi-broad flavor can never leak into the
// default-graph gate (named exclusion).
func goOutput(ctx context.Context, args []string, cgoEnabled string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = withCgoEnv(cgoEnabled)
	if dir, err := moduleRoot(); err == nil {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

// moduleRoot resolves the module root directory from `go env GOMOD` once and
// caches it. All gate subprocesses run from here so relative default-graph
// targets are stable whether the gate is driven by `go test`, `go run`, or CI.
var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})

// withCgoEnv returns os.Environ with CGO_ENABLED forced to cgoEnabled and any
// graphi-broad build tag stripped from GOFLAGS. This is the mechanical half of
// the named graphi-broad exclusion; the gate also never sets the tag itself.
func withCgoEnv(cgoEnabled string) []string {
	env := os.Environ()
	env = overrideEnv(env, "CGO_ENABLED", cgoEnabled)
	for i, e := range env {
		if strings.HasPrefix(e, "GOFLAGS=") {
			env[i] = "GOFLAGS=" + SanitizeGoFlags(strings.TrimPrefix(e, "GOFLAGS="))
			break
		}
	}
	return env
}

// overrideEnv returns env with key=val (replacing an existing key or appending).
func overrideEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			out = append(out, prefix+val)
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, prefix+val)
	}
	return out
}

// SanitizeGoFlags removes any opt-in graphi-broad build tag from a GOFLAGS-style
// string so the default-graph gate never sees the broad flavor (AC: graphi-broad
// exclusion). Recognized forms: `-tags=...graphi_broad...` / `...graphi-broad...`,
// the two-token `-tags <value>`, and a bare `graphi_broad`/`graphi-broad`. Other
// tags sharing a `-tags` with the broad tag are preserved. It strips BOTH the
// underscore build-tag form (the real `-tags graphi_broad`, SW-056 DN-2) and the
// hyphen flavor name so a default-graph gate that inherits the broad GOFLAGS does
// not silently no-op.
func SanitizeGoFlags(goFlags string) string {
	tokens := strings.Fields(goFlags)
	var kept []string
	for i := 0; i < len(tokens); i++ {
		f := tokens[i]
		bare := strings.TrimLeft(f, "-")
		switch {
		case bare == ExcludedBroadFlavor, bare == ExcludedBroadBuildTag:
			continue
		case strings.HasPrefix(bare, "tags="):
			if cleaned := filterBroadTag(strings.TrimPrefix(bare, "tags=")); cleaned != "" {
				kept = append(kept, "-tags="+cleaned)
			}
		case bare == "tags" && i+1 < len(tokens):
			// two-token form: -tags <value>
			if cleaned := filterBroadTag(tokens[i+1]); cleaned != "" {
				kept = append(kept, "-tags="+cleaned)
			}
			i++ // consume the value token
		default:
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
}

// filterBroadTag strips the graphi-broad tag from a comma-separated tag list. It
// removes both the underscore build-tag form (graphi_broad) and the hyphen flavor
// name (graphi-broad) (SW-056 DN-2).
func filterBroadTag(val string) string {
	var out []string
	for _, t := range strings.Split(val, ",") {
		if t == ExcludedBroadFlavor || t == ExcludedBroadBuildTag {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, ",")
}
