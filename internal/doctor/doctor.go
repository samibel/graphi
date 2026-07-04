// Package doctor provides the read-only diagnostic framework for `graphi doctor`.
//
// Design contract: every check receives a read-only Env and returns a
// CheckResult. No check is given a writer, an ingest handle, or any other
// capability that could mutate the workspace or dial out. This invariant is
// enforced by the API, not just by convention.
package doctor

import (
	"context"
	"fmt"
	"sort"
)

// Status is the closed set of check result severities.
type Status string

const (
	StatusPass       Status = "pass"
	StatusWarn       Status = "warn"
	StatusFail       Status = "fail"
	StatusInfo       Status = "info"
	StatusUnverified Status = "unverified"
)

// Valid returns true for the five defined status literals.
func (s Status) Valid() bool {
	switch s {
	case StatusPass, StatusWarn, StatusFail, StatusInfo, StatusUnverified:
		return true
	}
	return false
}

// CheckResult is the uniform output of every doctor check.
// The JSON representation is a contract of record; field presence and value
// domain must remain stable across releases.
type CheckResult struct {
	// ID is a short, stable machine identifier for this check, e.g. "path-graphi".
	ID string `json:"id"`
	// Category groups related checks, e.g. "binary", "path", "mcp".
	Category string `json:"category"`
	// Status is one of pass, warn, fail, info, unverified.
	Status Status `json:"status"`
	// Message is a concise human-readable summary of the finding.
	Message string `json:"message"`
	// Action is the actionable remediation when Status is not pass/info.
	// It is an empty string (never omitted) when there is no action.
	Action string `json:"action"`
	// Detail carries optional structured or multi-line context.
	Detail string `json:"detail,omitempty"`
}

// Check is the interface implemented by every diagnostic check.
type Check interface {
	ID() string
	Category() string
	Run(ctx context.Context, env Env) CheckResult
}

// Env is the read-only environment exposed to checks. It intentionally contains
// no writer, no ingest capability, and no network dialer.
type Env interface {
	// RepoRoot returns the repository root if known; empty if not discovered.
	RepoRoot() string
	// DBPath returns the resolved durable store path, empty for in-memory.
	DBPath() string
	// MCPConfig returns the MCP client configuration reader.
	MCPConfig() MCPConfigReader
	// Release returns release metadata for the running binary.
	Release() ReleaseInfo
	// State returns the state reader.
	State() StateReader
}

// MCPConfigReader exposes read-only MCP client configuration.
type MCPConfigReader interface {
	// Clients returns the list of supported MCP clients.
	Clients() []MCPClient
	// Plan returns the desired graphi entry for the client and the action needed.
	Plan(client MCPClient, binary string) (MCPPlanAction, error)
}

// MCPClient is a minimal read-only description of an MCP client.
type MCPClient struct {
	ID         string
	Display    string
	ConfigPath string
}

// MCPPlanAction describes the action required to bring a client config current.
type MCPPlanAction string

const (
	MCPPlanNoOp   MCPPlanAction = "no-op"
	MCPPlanCreate MCPPlanAction = "create"
	MCPPlanUpdate MCPPlanAction = "update"
)

// ReleaseInfo exposes read-only release metadata.
type ReleaseInfo interface {
	Version() string
	Commit() string
	Date() string
	Arch() string
	IsRelease() bool
}

// StateReader exposes read-only state discovery.
type StateReader interface {
	DiscoverDB(repoRoot string) (string, error)
}

// Registry holds registered checks.
type Registry struct {
	checks []Check
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a check to the registry. Panics if the check is nil.
func (r *Registry) Register(c Check) {
	if c == nil {
		panic("doctor: cannot register nil check")
	}
	r.checks = append(r.checks, c)
}

// Checks returns the registered checks in registration order.
func (r *Registry) Checks() []Check {
	out := make([]Check, len(r.checks))
	copy(out, r.checks)
	return out
}

// Runner executes all registered checks and produces a Report.
type Runner struct {
	registry *Registry
}

// NewRunner creates a runner over the supplied registry.
func NewRunner(r *Registry) *Runner {
	return &Runner{registry: r}
}

// Run executes every registered check exactly once in registration order.
func (rn *Runner) Run(ctx context.Context, env Env) Report {
	checks := rn.registry.Checks()
	results := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		results = append(results, c.Run(ctx, env))
	}
	return Report{Results: results}
}

// Report is the aggregate output of a Runner.
type Report struct {
	Results []CheckResult
}

// WorstStatus returns the highest severity status in the report using the
// ordering: fail > warn > unverified > info > pass.
func (r Report) WorstStatus() Status {
	order := map[Status]int{
		StatusPass:       0,
		StatusInfo:       1,
		StatusUnverified: 2,
		StatusWarn:       3,
		StatusFail:       4,
	}
	worst := StatusPass
	for _, res := range r.Results {
		if order[res.Status] > order[worst] {
			worst = res.Status
		}
	}
	return worst
}

// ExitCode returns 0 unless at least one result has status fail, in which case
// it returns 1. This is a pure function over []CheckResult so it can be tested
// without any real checks.
func ExitCode(results []CheckResult) int {
	for _, r := range results {
		if r.Status == StatusFail {
			return 1
		}
	}
	return 0
}

// ExitCodeFromReport returns the exit code for a report.
func ExitCodeFromReport(r Report) int {
	return ExitCode(r.Results)
}

// SortKey returns a deterministic sort key for a CheckResult.
func SortKey(r CheckResult) string {
	return r.Category + "/" + r.ID
}

// SortedResults returns a copy of results sorted by category then id.
func SortedResults(results []CheckResult) []CheckResult {
	out := make([]CheckResult, len(results))
	copy(out, results)
	sort.Slice(out, func(i, j int) bool {
		return SortKey(out[i]) < SortKey(out[j])
	})
	return out
}

// StringResult is a helper to create a simple CheckResult.
func StringResult(id, category, message string, status Status) CheckResult {
	return CheckResult{ID: id, Category: category, Message: message, Status: status}
}

// ResultWithAction is a helper to create a CheckResult with a remediation action.
func ResultWithAction(id, category, message string, status Status, action string) CheckResult {
	return CheckResult{ID: id, Category: category, Message: message, Status: status, Action: action}
}

// FormatSummary returns a compact summary line for a report.
func FormatSummary(r Report) string {
	counts := map[Status]int{}
	for _, res := range r.Results {
		counts[res.Status]++
	}
	return fmt.Sprintf("status=%s pass=%d warn=%d fail=%d info=%d unverified=%d",
		r.WorstStatus(), counts[StatusPass], counts[StatusWarn], counts[StatusFail],
		counts[StatusInfo], counts[StatusUnverified])
}
