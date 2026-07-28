package main

// SW-128 (P0-C5): capturing the machine a measurement ran on.
//
// FR-8 asks for CPU, RAM, OS, kernel, Go version, filesystem and cache state to
// be documented. The rule everything here follows is that a probe which fails
// leaves the field EMPTY and records why. It never substitutes a plausible
// value, and it never reports "unknown" as a string that could be mistaken for
// one — evalreport.RunEnvironment derives missing-ness from the value itself, so
// a failed probe surfaces as UNKNOWN whether or not this file remembers to say
// so.
//
// The probes are deliberately thin and per-OS. Linux is the reference runner
// class and reads /proc; darwin is the development sandbox and shells out to
// sysctl/uname. Anywhere else, the machine fields are simply not captured, the
// run reads incomplete, and that is the correct answer rather than a gap
// papered over with runtime.GOOS.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// environmentInput is what the RUN knows and the machine does not: which
// directory was measured, which class it declared, and what the run's own
// provenance resolved to. Everything else is probed.
type environmentInput struct {
	workDir         string
	runnerClass     string
	runnerRole      string
	cacheState      string
	candidateSHA    string
	candidateSource string
	measuredSHA     string
	candidateMatch  bool
	worktreeDirty   bool
}

// captureEnvironment probes the machine and folds in the run's own facts.
func captureEnvironment(in environmentInput) evalreport.RunEnvironment {
	env := evalreport.RunEnvironment{
		CPUCount:  runtime.NumCPU(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),

		CacheState:      in.cacheState,
		RunnerClass:     in.runnerClass,
		RunnerRole:      in.runnerRole,
		CandidateSHA:    in.candidateSHA,
		CandidateSource: in.candidateSource,
		MeasuredSHA:     in.measuredSHA,
		CandidateMatch:  in.candidateMatch,
		WorktreeDirty:   in.worktreeDirty,

		HarnessVersion: evalreport.HarnessVersion,
		ScorerVersion:  evalreport.ScorerVersion,
	}

	if model, err := probeCPUModel(); err != nil {
		env.NoteCaptureError(evalreport.EnvCPUModel, err.Error())
	} else {
		env.CPUModel = model
	}
	if ram, err := probeRAMBytes(); err != nil {
		env.NoteCaptureError(evalreport.EnvRAMBytes, err.Error())
	} else {
		env.RAMBytes = ram
	}
	if kernel, err := probeKernel(); err != nil {
		env.NoteCaptureError(evalreport.EnvKernel, err.Error())
	} else {
		env.Kernel = kernel
	}

	// The filesystem claim belongs to a DIRECTORY, not to "the machine", which
	// usually has several. An unnamed working directory is therefore not an
	// unreadable filesystem — it is a question nobody asked, and it says so.
	if strings.TrimSpace(in.workDir) == "" {
		env.NoteCaptureError(evalreport.EnvFilesystem, "no working directory was recorded for this run, so there is no path to read a filesystem type for")
	} else if fs, err := probeFilesystem(in.workDir); err != nil {
		env.FilesystemPath = in.workDir
		env.NoteCaptureError(evalreport.EnvFilesystem, err.Error())
	} else {
		env.Filesystem = fs
		env.FilesystemPath = in.workDir
	}

	// The cache state is not probed: it is the state the run's own cold
	// protocol REACHED, which only the run knows. An absent one says so rather
	// than guessing from the fact that a protocol was requested.
	if strings.TrimSpace(env.CacheState) == "" {
		env.NoteCaptureError(evalreport.EnvCacheState, "the run recorded no observed page-cache state")
	}
	if strings.TrimSpace(env.CandidateSHA) == "" {
		env.NoteCaptureError(evalreport.EnvCandidateSHA, "no frozen candidate was cited for this run (-candidate / -reference-scenario)")
	}
	return env
}

// probeCPUModel reads the processor's own name. The count comes from
// runtime.NumCPU, which is what the process could actually use — on a
// container-limited runner that is the honest figure and the socket count is
// not.
func probeCPUModel() (string, error) {
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/proc/cpuinfo")
		if err != nil {
			return "", fmt.Errorf("read /proc/cpuinfo: %w", err)
		}
		if model := parseCPUInfoModel(string(raw)); model != "" {
			return model, nil
		}
		return "", fmt.Errorf("/proc/cpuinfo carries no model name line")
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err != nil {
			return "", fmt.Errorf("sysctl machdep.cpu.brand_string: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("no CPU-model probe for %s", runtime.GOOS)
	}
}

// parseCPUInfoModel extracts the first "model name" from /proc/cpuinfo. Split
// out so the parsing is tested on fixed input rather than on whatever the host
// happens to be.
func parseCPUInfoModel(cpuinfo string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		// "model name" on x86, "Model" on some arm64 kernels; both are the
		// field a reader means by "which CPU was this".
		case "model name", "Model":
			if v := strings.TrimSpace(value); v != "" {
				return v
			}
		}
	}
	return ""
}

// probeRAMBytes reads total physical memory.
func probeRAMBytes() (int64, error) {
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, fmt.Errorf("read /proc/meminfo: %w", err)
		}
		bytes := parseMemTotalBytes(string(raw))
		if bytes <= 0 {
			return 0, fmt.Errorf("/proc/meminfo carries no readable MemTotal")
		}
		return bytes, nil
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0, fmt.Errorf("sysctl hw.memsize: %w", err)
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse hw.memsize: %w", err)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("no memory probe for %s", runtime.GOOS)
	}
}

// parseMemTotalBytes converts /proc/meminfo's "MemTotal: N kB" into bytes.
// Returns 0 when the line is absent or unparseable — the caller turns that into
// a recorded gap rather than a zero-byte machine.
func parseMemTotalBytes(meminfo string) int64 {
	for _, line := range strings.Split(meminfo, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "MemTotal" {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(value))
		if len(fields) == 0 {
			return 0
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		// The unit is part of the line and is honoured rather than assumed:
		// a kernel that ever reported bytes would otherwise be scaled by 1024.
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			return n * 1024
		}
		return n
	}
	return 0
}

// probeKernel reads the running kernel release.
func probeKernel() (string, error) {
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/proc/sys/kernel/osrelease")
		if err != nil {
			return "", fmt.Errorf("read /proc/sys/kernel/osrelease: %w", err)
		}
		if v := strings.TrimSpace(string(raw)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("/proc/sys/kernel/osrelease is empty")
	case "darwin":
		out, err := exec.Command("uname", "-r").Output()
		if err != nil {
			return "", fmt.Errorf("uname -r: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("no kernel probe for %s", runtime.GOOS)
	}
}

// probeFilesystem reports the filesystem TYPE the given directory sits on.
// It matters more than it looks: an ext4 run and a tmpfs run of the same index
// are not the same measurement, and overlayfs on a container runner is a
// different story again.
func probeFilesystem(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/proc/mounts")
		if err != nil {
			return "", fmt.Errorf("read /proc/mounts: %w", err)
		}
		if fs := matchMountTable(parseProcMounts(string(raw)), abs); fs != "" {
			return fs, nil
		}
		return "", fmt.Errorf("no mount in /proc/mounts covers %s", abs)
	case "darwin":
		out, err := exec.Command("/sbin/mount").Output()
		if err != nil {
			return "", fmt.Errorf("mount: %w", err)
		}
		if fs := matchMountTable(parseDarwinMounts(string(out)), abs); fs != "" {
			return fs, nil
		}
		return "", fmt.Errorf("no mount point covers %s", abs)
	default:
		return "", fmt.Errorf("no filesystem probe for %s", runtime.GOOS)
	}
}

// mountEntry is one mount point and its filesystem type.
type mountEntry struct {
	point string
	fs    string
}

// parseProcMounts reads the Linux mount table: "<device> <point> <type> ...".
func parseProcMounts(mounts string) []mountEntry {
	var out []mountEntry
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// /proc/mounts octal-escapes spaces in mount points; unescaping the one
		// escape that actually occurs keeps a path with a space matchable.
		out = append(out, mountEntry{point: strings.ReplaceAll(fields[1], `\040`, " "), fs: fields[2]})
	}
	return out
}

// parseDarwinMounts reads BSD `mount` output:
// "/dev/disk3s5 on /System/Volumes/Data (apfs, local, journaled)".
func parseDarwinMounts(mounts string) []mountEntry {
	var out []mountEntry
	for _, line := range strings.Split(mounts, "\n") {
		_, rest, ok := strings.Cut(line, " on ")
		if !ok {
			continue
		}
		point, opts, ok := strings.Cut(rest, " (")
		if !ok {
			continue
		}
		fs, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimSpace(opts), ")"), ",")
		if fs = strings.TrimSpace(fs); fs == "" {
			continue
		}
		out = append(out, mountEntry{point: strings.TrimSpace(point), fs: fs})
	}
	return out
}

// matchMountTable picks the LONGEST mount point that contains the path. Longest
// wins because mount points nest: on a runner where /mnt is its own filesystem,
// a work directory under /mnt must report /mnt's type and not /'s.
func matchMountTable(entries []mountEntry, path string) string {
	best := ""
	fs := ""
	for _, e := range entries {
		if !underPath(e.point, path) {
			continue
		}
		if len(e.point) >= len(best) {
			best, fs = e.point, e.fs
		}
	}
	return fs
}

// underPath is "is path inside point", compared on path SEPARATORS so /var
// does not swallow /variant.
func underPath(point, path string) bool {
	if point == "" {
		return false
	}
	if point == "/" || point == path {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(point, "/")+"/")
}
