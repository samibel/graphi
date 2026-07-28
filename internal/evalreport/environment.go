package evalreport

// SW-128 (P0-C5): the run environment, recorded rather than assumed.
//
// FR-8's acceptance criteria name the facts a performance number is only
// interpretable against: CPU, RAM, OS, kernel, Go version, filesystem and cache
// state — plus the provenance that says WHAT was measured (runner class,
// candidate SHA) and BY WHAT (harness version, scorer version).
//
// The load-bearing design choice is that "missing" is DERIVED FROM THE VALUE,
// never declared by the capturer. A capture that could not read the kernel
// leaves the field empty and Missing() reports it; there is no flag a forgetful
// capturer can leave unset, and no path by which an empty string reaches a
// reader as a documented value. Rows() renders every required field, in a fixed
// order, with UNKNOWN standing in for what was not read — so the table is
// always the full requirement and a gap is visible at the position it belongs.

import (
	"sort"
	"strconv"
	"strings"
)

// EnvironmentUnknown is what an uncaptured field reads as. It is the same
// vocabulary the evidence index and the gate statuses use: UNKNOWN is a
// first-class value, and it is not a measurement.
const EnvironmentUnknown = StatusUnknown

// Environment field names. They are constants because the aggregator addresses
// them by name and a divergence between the capturer's spelling and the
// reader's would silently produce a permanently-missing field.
const (
	EnvCPUModel       = "cpu_model"
	EnvCPUCount       = "cpu_count"
	EnvRAMBytes       = "ram_bytes"
	EnvOS             = "os"
	EnvArch           = "arch"
	EnvKernel         = "kernel"
	EnvGoVersion      = "go_version"
	EnvFilesystem     = "filesystem"
	EnvCacheState     = "cache_state"
	EnvRunnerClass    = "runner_class"
	EnvCandidateSHA   = "candidate_sha"
	EnvHarnessVersion = "harness_version"
	EnvScorerVersion  = "scorer_version"
)

// RequiredEnvironmentFields is AC-3's list, in the order it is reported. It is
// the ONLY definition of "was the environment captured": Complete() is
// "no entry here is missing", so extending the requirement is one edit and
// cannot be met by a capture that quietly stops filling a field.
var RequiredEnvironmentFields = []string{
	EnvCPUModel,
	EnvCPUCount,
	EnvRAMBytes,
	EnvOS,
	EnvArch,
	EnvKernel,
	EnvGoVersion,
	EnvFilesystem,
	EnvCacheState,
	EnvRunnerClass,
	EnvCandidateSHA,
	EnvHarnessVersion,
	EnvScorerVersion,
}

// RunEnvironment is the machine, the software and the provenance one
// measurement run was produced under.
type RunEnvironment struct {
	// The machine.
	CPUModel string `json:"cpu_model,omitempty"`
	CPUCount int    `json:"cpu_count,omitempty"`
	RAMBytes int64  `json:"ram_bytes,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Kernel   string `json:"kernel,omitempty"`

	// The software.
	GoVersion string `json:"go_version,omitempty"`

	// Filesystem is the filesystem TYPE the measurement's working directory sat
	// on (ext4, apfs, overlay, tmpfs); FilesystemPath is the path it was read
	// for, so the claim is attributable to a directory rather than to "the
	// machine", which usually has several.
	Filesystem     string `json:"filesystem,omitempty"`
	FilesystemPath string `json:"filesystem_path,omitempty"`

	// CacheState is the page-cache state the run actually reached (the
	// PageCache* constants), taken from the run's own ColdState rather than
	// from the protocol that was requested.
	CacheState string `json:"cache_state,omitempty"`

	// Provenance: what was measured, and is it the thing anyone cares about.
	RunnerClass     string `json:"runner_class,omitempty"`
	RunnerRole      string `json:"runner_role,omitempty"`
	CandidateSHA    string `json:"candidate_sha,omitempty"`
	CandidateSource string `json:"candidate_source,omitempty"`
	MeasuredSHA     string `json:"measured_sha,omitempty"`
	CandidateMatch  bool   `json:"candidate_match"`
	WorktreeDirty   bool   `json:"worktree_dirty"`

	// The measuring apparatus itself (PRD risk 9). Two runs whose harness
	// versions differ did not measure the same question, and averaging them is
	// the drift this field exists to make impossible.
	HarnessVersion string `json:"harness_version,omitempty"`
	ScorerVersion  string `json:"scorer_version,omitempty"`

	// CaptureErrors records WHY a field could not be read, keyed by field name.
	// A gap with a reason is actionable; a gap without one is a shrug.
	CaptureErrors map[string]string `json:"capture_errors,omitempty"`
}

// EnvironmentRow is one required field as a reader sees it.
type EnvironmentRow struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Known bool   `json:"known"`
	Error string `json:"error,omitempty"`
}

// NoteCaptureError records why a field is missing. It is a method rather than a
// map literal at the call site so the map is allocated once and a capturer
// cannot forget to initialise it.
func (e *RunEnvironment) NoteCaptureError(field, reason string) {
	if field == "" || reason == "" {
		return
	}
	if e.CaptureErrors == nil {
		e.CaptureErrors = map[string]string{}
	}
	e.CaptureErrors[field] = reason
}

// Field returns one required field's rendered value and whether it was
// captured. An uncaptured field — and an unrecognised NAME — reads
// (EnvironmentUnknown, false), never a blank that a caller might print as if it
// were the truth.
func (e RunEnvironment) Field(name string) (string, bool) {
	raw, ok := e.rawField(name)
	if !ok {
		return EnvironmentUnknown, false
	}
	if strings.TrimSpace(raw) == "" {
		return EnvironmentUnknown, false
	}
	return raw, true
}

// rawField is the name → value mapping. The numeric fields render their zero as
// the empty string on purpose: a machine with zero cores or zero bytes of RAM
// is not a measurement, and Field turns that into UNKNOWN like any other gap.
func (e RunEnvironment) rawField(name string) (string, bool) {
	switch name {
	case EnvCPUModel:
		return e.CPUModel, true
	case EnvCPUCount:
		return positiveInt(int64(e.CPUCount)), true
	case EnvRAMBytes:
		return positiveInt(e.RAMBytes), true
	case EnvOS:
		return e.OS, true
	case EnvArch:
		return e.Arch, true
	case EnvKernel:
		return e.Kernel, true
	case EnvGoVersion:
		return e.GoVersion, true
	case EnvFilesystem:
		return e.Filesystem, true
	case EnvCacheState:
		return e.CacheState, true
	case EnvRunnerClass:
		return e.RunnerClass, true
	case EnvCandidateSHA:
		return e.CandidateSHA, true
	case EnvHarnessVersion:
		return e.HarnessVersion, true
	case EnvScorerVersion:
		return e.ScorerVersion, true
	default:
		return "", false
	}
}

func positiveInt(v int64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

// Missing lists the required fields that were not captured, in
// RequiredEnvironmentFields order. Fixed order, not map order: two runs of the
// same incomplete capture must produce byte-identical records or the artifacts
// are not comparable.
func (e RunEnvironment) Missing() []string {
	var out []string
	for _, name := range RequiredEnvironmentFields {
		if _, known := e.Field(name); !known {
			out = append(out, name)
		}
	}
	return out
}

// Complete is "every required field was captured". It is the environment half
// of whether a run may be published at all (AC-3 + AC-5).
func (e RunEnvironment) Complete() bool { return len(e.Missing()) == 0 }

// Rows renders every required field, in order, with UNKNOWN for what was not
// read. The table is always the full requirement — a gap shows up at the
// position it belongs rather than as a shorter list nobody counts.
func (e RunEnvironment) Rows() []EnvironmentRow {
	out := make([]EnvironmentRow, 0, len(RequiredEnvironmentFields))
	for _, name := range RequiredEnvironmentFields {
		value, known := e.Field(name)
		out = append(out, EnvironmentRow{
			Field: name,
			Value: value,
			Known: known,
			Error: e.CaptureErrors[name],
		})
	}
	return out
}

// CaptureErrorFields lists the fields that recorded a capture error, sorted, so
// a summary can name them deterministically.
func (e RunEnvironment) CaptureErrorFields() []string {
	out := make([]string, 0, len(e.CaptureErrors))
	for name := range e.CaptureErrors {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
