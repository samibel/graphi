package main

// SW-128 (P0-C5): what the environment capture must never do.
//
// The probes themselves are host-dependent and are not asserted here — a test
// that demands a particular kernel string would fail on every machine but the
// author's. What IS asserted is the honesty contract around them: a probe that
// fails leaves a gap with a reason, a fact the run knows is carried rather than
// guessed, and the parsers turn fixed input into the value they claim to.

import (
	"runtime"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// The run's own facts are carried, not probed. Everything the harness resolved
// about WHAT it measured has to reach the environment record, or the numbers
// arrive without their provenance.
func TestCaptureEnvironment_CarriesTheRunsOwnFacts(t *testing.T) {
	env := captureEnvironment(environmentInput{
		workDir:         t.TempDir(),
		runnerClass:     "ubuntu-latest",
		runnerRole:      "reference",
		cacheState:      evalreport.PageCacheDropped,
		candidateSHA:    "deadbeef",
		candidateSource: "docs/rc/evidence-index.yaml",
		measuredSHA:     "deadbeef",
		candidateMatch:  true,
	})

	if env.RunnerClass != "ubuntu-latest" || env.RunnerRole != "reference" {
		t.Errorf("runner = %s/%s, want ubuntu-latest/reference", env.RunnerClass, env.RunnerRole)
	}
	if env.CacheState != evalreport.PageCacheDropped {
		t.Errorf("cache state = %q, want %q", env.CacheState, evalreport.PageCacheDropped)
	}
	if env.CandidateSHA != "deadbeef" || !env.CandidateMatch {
		t.Errorf("candidate = %q (match %v), want deadbeef/true", env.CandidateSHA, env.CandidateMatch)
	}
	// The versions are stamped by the capture, not by the caller: a run cannot
	// declare which methodology produced it.
	if env.HarnessVersion != evalreport.HarnessVersion || env.ScorerVersion != evalreport.ScorerVersion {
		t.Errorf("versions = %s/%s, want %s/%s", env.HarnessVersion, env.ScorerVersion,
			evalreport.HarnessVersion, evalreport.ScorerVersion)
	}
	// These three are always available from the Go runtime, so they may never
	// be missing on any host this test runs on.
	for _, field := range []string{evalreport.EnvOS, evalreport.EnvArch, evalreport.EnvGoVersion, evalreport.EnvCPUCount} {
		if _, known := env.Field(field); !known {
			t.Errorf("field %s is missing but comes from the Go runtime", field)
		}
	}
}

// The case the story calls out: a fact the run never resolved must read as
// MISSING with a stated reason, not as a captured blank.
func TestCaptureEnvironment_AnUnresolvedFactIsMissingWithAReason(t *testing.T) {
	env := captureEnvironment(environmentInput{workDir: t.TempDir(), runnerClass: "local"})

	missing := map[string]bool{}
	for _, name := range env.Missing() {
		missing[name] = true
	}
	for _, field := range []string{evalreport.EnvCacheState, evalreport.EnvCandidateSHA} {
		if !missing[field] {
			t.Errorf("field %s is not reported missing despite never being resolved", field)
		}
		if env.CaptureErrors[field] == "" {
			t.Errorf("field %s is missing with no recorded reason", field)
		}
		value, known := env.Field(field)
		if known || value != evalreport.EnvironmentUnknown {
			t.Errorf("field %s = (%q, %v), want (UNKNOWN, false)", field, value, known)
		}
	}
	if env.Complete() {
		t.Fatal("an environment with no cache state and no candidate reads complete")
	}
}

// A run with no working directory has no filesystem QUESTION, and the recorded
// reason must say that rather than blame an unreadable mount table.
func TestCaptureEnvironment_NoWorkDirIsNotAnUnreadableFilesystem(t *testing.T) {
	env := captureEnvironment(environmentInput{runnerClass: "local"})
	reason := env.CaptureErrors[evalreport.EnvFilesystem]
	if reason == "" {
		t.Fatal("no reason recorded for the missing filesystem")
	}
	if !strings.Contains(reason, "working directory") {
		t.Errorf("reason = %q, want it to name the absent working directory", reason)
	}
}

// On the two platforms with probes, a real directory must produce a real
// filesystem type. This is the one host-dependent assertion worth making: if it
// fails, the reference runner would publish an UNKNOWN filesystem.
func TestCaptureEnvironment_ProbesTheFilesystemWhereItCan(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("no filesystem probe for %s", runtime.GOOS)
	}
	env := captureEnvironment(environmentInput{workDir: t.TempDir(), runnerClass: "local"})
	if value, known := env.Field(evalreport.EnvFilesystem); !known {
		t.Fatalf("filesystem = %q on %s, want a probed type (reason: %s)",
			value, runtime.GOOS, env.CaptureErrors[evalreport.EnvFilesystem])
	}
}

func TestParseCPUInfoModel(t *testing.T) {
	const cpuinfo = `processor	: 0
vendor_id	: AuthenticAMD
cpu family	: 25
model		: 1
model name	: AMD EPYC 7763 64-Core Processor
stepping	: 1
`
	if got := parseCPUInfoModel(cpuinfo); got != "AMD EPYC 7763 64-Core Processor" {
		t.Fatalf("model = %q", got)
	}
	// The lowercase "model : 1" line above must NOT be mistaken for the model
	// NAME — it appears first and would otherwise win, publishing "1" as the
	// CPU. Only the capitalised arm64 field is a fallback.
	if got := parseCPUInfoModel("model\t\t: 1\n"); got != "" {
		t.Fatalf("lowercase numeric model = %q, want it ignored", got)
	}
	if got := parseCPUInfoModel("Model\t\t: Neoverse-N1\n"); got != "Neoverse-N1" {
		t.Fatalf("arm-style Model = %q, want Neoverse-N1", got)
	}
	if got := parseCPUInfoModel("nothing useful\n"); got != "" {
		t.Fatalf("model = %q for input with no model line, want empty", got)
	}
}

func TestParseMemTotalBytes(t *testing.T) {
	if got := parseMemTotalBytes("MemTotal:       16389184 kB\nMemFree: 100 kB\n"); got != 16389184*1024 {
		t.Fatalf("MemTotal = %d, want %d", got, 16389184*1024)
	}
	// No unit means the number is already bytes; assuming kB would inflate it
	// by 1024 and nobody would notice.
	if got := parseMemTotalBytes("MemTotal: 4096\n"); got != 4096 {
		t.Fatalf("unitless MemTotal = %d, want 4096", got)
	}
	for _, bad := range []string{"", "MemFree: 100 kB\n", "MemTotal: notanumber kB\n", "MemTotal:\n"} {
		if got := parseMemTotalBytes(bad); got != 0 {
			t.Errorf("parseMemTotalBytes(%q) = %d, want 0", bad, got)
		}
	}
}

func TestParseProcMounts(t *testing.T) {
	const mounts = `/dev/root / ext4 rw,relatime 0 0
tmpfs /dev/shm tmpfs rw,nosuid 0 0
/dev/sda1 /mnt ext4 rw 0 0
overlay /var/lib/docker/overlay2/x/merged overlay rw 0 0
`
	entries := parseProcMounts(mounts)
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	// Longest match wins, because mount points nest: a work directory under
	// /mnt must report /mnt's filesystem, not /'s.
	if got := matchMountTable(entries, "/mnt/work/graphi"); got != "ext4" {
		t.Errorf("fs for /mnt/work/graphi = %q, want ext4", got)
	}
	if got := matchMountTable(entries, "/dev/shm/thing"); got != "tmpfs" {
		t.Errorf("fs for /dev/shm/thing = %q, want tmpfs", got)
	}
	if got := matchMountTable(entries, "/home/runner"); got != "ext4" {
		t.Errorf("fs for /home/runner = %q, want the root ext4", got)
	}
}

func TestParseDarwinMounts(t *testing.T) {
	const mounts = `/dev/disk3s1s1 on / (apfs, sealed, local, read-only, journaled)
devfs on /dev (devfs, local, nobrowse)
/dev/disk3s5 on /System/Volumes/Data (apfs, local, journaled, nobrowse)
`
	entries := parseDarwinMounts(mounts)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if got := matchMountTable(entries, "/System/Volumes/Data/Users/x"); got != "apfs" {
		t.Errorf("fs = %q, want apfs", got)
	}
	if got := matchMountTable(entries, "/dev/null"); got != "devfs" {
		t.Errorf("fs = %q, want devfs", got)
	}
}

// A mount point must not swallow a sibling that merely shares a prefix. /var
// covering /variant would silently attribute a run to the wrong filesystem.
func TestMatchMountTable_ComparesOnSeparators(t *testing.T) {
	entries := []mountEntry{{point: "/", fs: "ext4"}, {point: "/var", fs: "xfs"}}
	if got := matchMountTable(entries, "/variant/work"); got != "ext4" {
		t.Fatalf("fs for /variant/work = %q, want the root ext4 — /var must not match /variant", got)
	}
	if got := matchMountTable(entries, "/var/work"); got != "xfs" {
		t.Fatalf("fs for /var/work = %q, want xfs", got)
	}
}
