package evalreport

// SW-128 (P0-C5): the environment record's honesty rules.
//
// These tests own one property above all others: a field that was not captured
// must READ as not captured. An empty `kernel` rendering as a documented kernel
// is exactly the quiet dishonesty this slice removes, so the missing-field
// derivation is taken from the VALUE rather than from a flag the capturer sets
// — a capturer that forgets to declare a gap cannot hide one.

import (
	"encoding/json"
	"strings"
	"testing"
)

// completeEnvironment is a fully captured environment, used as the baseline the
// negative cases below remove exactly one field from.
func completeEnvironment() RunEnvironment {
	return RunEnvironment{
		CPUModel:       "AMD EPYC 7763 64-Core Processor",
		CPUCount:       4,
		RAMBytes:       16 * 1024 * 1024 * 1024,
		OS:             "linux",
		Arch:           "amd64",
		Kernel:         "6.11.0-1015-azure",
		GoVersion:      "go1.26.5",
		Filesystem:     "ext4",
		FilesystemPath: "/mnt/work",
		CacheState:     PageCacheDropped,
		RunnerClass:    "ubuntu-latest",
		RunnerRole:     "reference",
		CandidateSHA:   "abc123",
		HarnessVersion: HarnessVersion,
		ScorerVersion:  ScorerVersion,
	}
}

// AC-3: every field the story names is part of the record, and a complete
// capture reports itself complete with nothing missing.
func TestRunEnvironment_CompleteCaptureHasNoMissingFields(t *testing.T) {
	env := completeEnvironment()
	if got := env.Missing(); len(got) != 0 {
		t.Fatalf("missing = %v, want none", got)
	}
	if !env.Complete() {
		t.Fatal("Complete() = false for a fully captured environment")
	}
	for _, name := range RequiredEnvironmentFields {
		value, known := env.Field(name)
		if !known {
			t.Errorf("field %q reads unknown in a complete capture", name)
		}
		if strings.TrimSpace(value) == "" {
			t.Errorf("field %q is known but empty", name)
		}
	}
}

// The test note's case, stated exactly: an empty kernel must surface as MISSING
// rather than slip through as a documented empty string.
func TestRunEnvironment_AnEmptyKernelIsMissingNotDocumented(t *testing.T) {
	env := completeEnvironment()
	env.Kernel = ""

	if env.Complete() {
		t.Fatal("Complete() = true with an empty kernel")
	}
	missing := env.Missing()
	if len(missing) != 1 || missing[0] != EnvKernel {
		t.Fatalf("missing = %v, want exactly [%s]", missing, EnvKernel)
	}
	value, known := env.Field(EnvKernel)
	if known {
		t.Error("Field(kernel) reports known for an empty kernel")
	}
	if value != EnvironmentUnknown {
		t.Errorf("Field(kernel) = %q, want %q — an unread field renders as UNKNOWN, never as blank", value, EnvironmentUnknown)
	}
}

// Whitespace is not a capture. A field padded to look present must read the
// same as an absent one, or a capturer could satisfy the check with a space.
func TestRunEnvironment_WhitespaceIsNotACapture(t *testing.T) {
	env := completeEnvironment()
	env.Filesystem = "   "
	missing := env.Missing()
	if len(missing) != 1 || missing[0] != EnvFilesystem {
		t.Fatalf("missing = %v, want exactly [%s]", missing, EnvFilesystem)
	}
}

// The numeric fields have their own zero problem: a CPU count of 0 and a RAM
// size of 0 are not measurements, and treating them as documented values would
// publish a machine with no memory.
func TestRunEnvironment_ZeroNumericsAreMissing(t *testing.T) {
	env := completeEnvironment()
	env.CPUCount = 0
	env.RAMBytes = 0

	missing := env.Missing()
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want cpu_count and ram_bytes", missing)
	}
	for _, name := range []string{EnvCPUCount, EnvRAMBytes} {
		value, known := env.Field(name)
		if known || value != EnvironmentUnknown {
			t.Errorf("field %q = (%q, %v), want (UNKNOWN, false)", name, value, known)
		}
	}
}

// Missing() is reported in RequiredEnvironmentFields order, not in map order:
// two runs of the same incomplete capture must produce byte-identical records
// or the artifact is not comparable.
func TestRunEnvironment_MissingIsDeterministicallyOrdered(t *testing.T) {
	env := completeEnvironment()
	env.Kernel = ""
	env.CPUModel = ""
	env.RunnerClass = ""

	want := []string{EnvCPUModel, EnvKernel, EnvRunnerClass}
	for i := 0; i < 20; i++ {
		got := env.Missing()
		if len(got) != len(want) {
			t.Fatalf("missing = %v, want %v", got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("missing = %v, want %v (RequiredEnvironmentFields order)", got, want)
			}
		}
	}
}

// A capture error is retained beside the gap. Knowing WHY the filesystem could
// not be read is what turns an UNKNOWN into something actionable rather than a
// shrug.
func TestRunEnvironment_CaptureErrorsAreRetained(t *testing.T) {
	env := completeEnvironment()
	env.Filesystem = ""
	env.NoteCaptureError(EnvFilesystem, "no /proc/mounts on darwin")

	if got := env.CaptureErrors[EnvFilesystem]; got != "no /proc/mounts on darwin" {
		t.Fatalf("capture error = %q, want the recorded reason", got)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "no /proc/mounts on darwin") {
		t.Error("the capture error is not in the serialized record")
	}
}

// Field() must reject a name that is not a field at all rather than returning a
// plausible blank — a typo in the aggregator has to fail loudly.
func TestRunEnvironment_UnknownFieldNameIsNotAValue(t *testing.T) {
	value, known := completeEnvironment().Field("cpu_temperature")
	if known {
		t.Fatal("Field() claims to know a field that does not exist")
	}
	if value != EnvironmentUnknown {
		t.Fatalf("Field() = %q for an unknown name, want UNKNOWN", value)
	}
}

// EnvironmentRows is what a reader sees. Every required field appears, in
// order, with UNKNOWN standing in for what was not captured — the table can
// never be shorter than the requirement.
func TestRunEnvironment_RowsCoverEveryRequiredField(t *testing.T) {
	env := completeEnvironment()
	env.Kernel = ""

	rows := env.Rows()
	if len(rows) != len(RequiredEnvironmentFields) {
		t.Fatalf("rows = %d, want %d", len(rows), len(RequiredEnvironmentFields))
	}
	for i, row := range rows {
		if row.Field != RequiredEnvironmentFields[i] {
			t.Fatalf("row %d = %q, want %q", i, row.Field, RequiredEnvironmentFields[i])
		}
	}
	for _, row := range rows {
		if row.Field != EnvKernel {
			continue
		}
		if row.Known || row.Value != EnvironmentUnknown {
			t.Fatalf("kernel row = %+v, want UNKNOWN and not known", row)
		}
	}
}
