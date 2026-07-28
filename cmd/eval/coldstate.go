package main

// SW-124 (P0-C1): coldness is produced and verified, never assumed.
//
// "Ten cold runs" is only a measurement if each run was actually cold. Two
// independent things have to hold, and each is recorded as an OBSERVATION
// rather than as the intention that produced it:
//
//  1. no pre-existing state — the SQLite store and the ingest meta directory
//     must not exist before the timed pass, or the run is measuring a warm
//     start;
//  2. a page cache in the state the runner class DECLARED — the reference class
//     declares `sync && sysctl -w vm.drop_caches=3` before every timed index,
//     and a reference-class run that did not manage it is not cold by its own
//     contract and says so.
//
// The drop happens between the clone and the index, not before the clone: a
// shallow clone writes exactly the files about to be indexed into the page
// cache, so dropping before it would produce a warm index wearing a cold label.
//
// This file also reads back the cgroup v2 memory limit the measured process is
// running under. That belongs here rather than in the OOM check because the
// limit has to be observed from INSIDE the constrained process — a launcher
// reading the limit it just asked for verifies nothing.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// coldProtocol is what the caller asks for; evalreport.ColdState is what was
// achieved. Keeping the two apart is the whole point — the request is not
// evidence.
type coldProtocol struct {
	// drop asks for the page cache to be dropped before the timed index.
	drop bool
	// requiredProtocol is the runner class's declared cache_state, copied into
	// the record so the observation can be read against the declaration.
	requiredProtocol string
	// dropRequired says the declaration OBLIGES this run to drop the cache
	// (true for the reference class). A run that fails to drop it is then
	// recorded as unverified rather than published as cold.
	dropRequired bool
}

// cgroupRoot is the cgroup v2 mount point on the reference runner class.
const cgroupRoot = "/sys/fs/cgroup"

// prepareCold verifies the no-pre-existing-state half, runs the page-cache
// protocol, and returns the evidence for both. It never fails the run: an
// un-droppable cache is a fact about the measurement, and hiding it behind an
// error would remove the very signal AC-1 asks for.
func prepareCold(ctx context.Context, cp coldProtocol, dbPath, metaDir string) evalreport.ColdState {
	state := evalreport.ColdState{
		StorePath:        dbPath,
		MetaPath:         metaDir,
		RequiredProtocol: cp.requiredProtocol,
		DropRequired:     cp.dropRequired,
		PageCache:        evalreport.PageCacheNotDropped,
	}
	// A store or meta directory that already exists means the "cold" index
	// would resume from committed state. Detect it; do not delete it — a
	// silent cleanup would hide a harness bug that produced the reuse.
	if _, err := os.Stat(dbPath); err == nil {
		state.StorePreexisting = true
	}
	if _, err := os.Stat(metaDir); err == nil {
		state.MetaPreexisting = true
	}

	if cp.drop {
		argv, err := dropPageCache(ctx)
		state.PageCacheCommand = argv
		switch {
		case err == nil:
			state.PageCache = evalreport.PageCacheDropped
		case runtime.GOOS != "linux":
			state.PageCache = evalreport.PageCacheUnsupported
			state.PageCacheError = err.Error()
		default:
			state.PageCache = evalreport.PageCacheDropFailed
			state.PageCacheError = err.Error()
		}
	}

	reasons := []string{}
	if state.StorePreexisting {
		reasons = append(reasons, "the SQLite store already existed, so the index would not have started from nothing")
	}
	if state.MetaPreexisting {
		reasons = append(reasons, "the ingest meta directory already existed, so a previous pass' state was reachable")
	}
	if cp.dropRequired && state.PageCache != evalreport.PageCacheDropped {
		reasons = append(reasons, "the runner class declares a drop-caches protocol and the page cache was "+state.PageCache)
	}
	state.Verified = len(reasons) == 0
	state.Reason = strings.Join(reasons, "; ")
	return state
}

// dropPageCache runs the reference class's declared protocol and returns the
// exact argv it used, so the record shows what was executed rather than what
// was meant. sudo is tried non-interactively (`-n`): the hosted runners have
// passwordless sudo, and a prompt would hang a scheduled job forever.
func dropPageCache(ctx context.Context) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, errUnsupportedDropCaches
	}
	script := "sync && sysctl -w vm.drop_caches=3"
	argv := []string{"sh", "-c", script}
	if os.Geteuid() != 0 {
		argv = []string{"sudo", "-n", "sh", "-c", script}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return argv, &dropCachesError{argv: argv, err: err, output: strings.TrimSpace(string(out))}
	}
	return argv, nil
}

type dropCachesError struct {
	argv   []string
	err    error
	output string
}

func (e *dropCachesError) Error() string {
	msg := strings.Join(e.argv, " ") + ": " + e.err.Error()
	if e.output != "" {
		msg += ": " + e.output
	}
	return msg
}

// errUnsupportedDropCaches is a sentinel rather than a formatted error so the
// caller can distinguish "this OS has no such protocol" (recorded as
// unsupported) from "the protocol ran and failed" (recorded as drop_failed).
var errUnsupportedDropCaches = &unsupportedDropCachesError{}

type unsupportedDropCachesError struct{}

func (*unsupportedDropCachesError) Error() string {
	return "dropping the page cache is only implemented for linux (the reference runner class); this run's cache state is uncontrolled"
}

// observedCgroupLimits reads the memory limit this process is running under.
// Non-Linux hosts have no cgroup v2 and record that fact rather than a zero
// that could read as "no limit observed, therefore fine".
func observedCgroupLimits() *evalreport.CgroupLimits {
	if runtime.GOOS != "linux" {
		return &evalreport.CgroupLimits{
			Available: false,
			Error:     "cgroup v2 memory limits are readable on linux only; this host is " + runtime.GOOS,
		}
	}
	return readCgroupMemoryLimits("/proc/self/cgroup", cgroupRoot)
}

// readCgroupMemoryLimits resolves the process's own cgroup v2 path and reads
// memory.max and memory.swap.max verbatim. Values are NOT normalized: the
// literal "max" means "no limit", and turning it into a number would erase the
// difference between an unlimited process and a generously limited one.
//
// The paths are parameters so the parsing is testable without a cgroup mount.
func readCgroupMemoryLimits(procCgroupPath, root string) *evalreport.CgroupLimits {
	limits := &evalreport.CgroupLimits{}
	raw, err := os.ReadFile(procCgroupPath)
	if err != nil {
		limits.Error = "read " + procCgroupPath + ": " + err.Error()
		return limits
	}
	rel := ""
	for _, line := range strings.Split(string(raw), "\n") {
		// cgroup v2 is the unified hierarchy, always the "0::" line.
		if suffix, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok {
			rel = suffix
			break
		}
	}
	if rel == "" {
		limits.Error = "no cgroup v2 (unified, '0::') entry in " + procCgroupPath + "; a v1 host cannot impose the SW-123 limit as specified"
		return limits
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	limits.Path = dir

	memoryMax, err := readCgroupFile(filepath.Join(dir, "memory.max"))
	if err != nil {
		limits.Error = err.Error()
		return limits
	}
	swapMax, err := readCgroupFile(filepath.Join(dir, "memory.swap.max"))
	if err != nil {
		limits.Error = err.Error()
		return limits
	}
	limits.Available = true
	limits.MemoryMax = memoryMax
	limits.MemorySwapMax = swapMax
	// memory.events is read from INSIDE the cgroup, at the end of the measured
	// work: a kill of any process in the scope (including a helper such as git)
	// bumps oom_kill even when this process survived to write the report. When
	// this process is itself killed there is no report and the wait status
	// carries the signal instead.
	if events, err := readCgroupFile(filepath.Join(dir, "memory.events")); err == nil {
		if kills, ok := parseMemoryEventsOOMKill(events); ok {
			limits.OOMKill = kills
			limits.OOMKillCollected = true
		}
	}
	return limits
}

// parseMemoryEventsOOMKill reads the `oom_kill <n>` line of a cgroup v2
// memory.events file. A file without that key returns ok=false rather than 0 —
// an absent counter is not a count of zero.
func parseMemoryEventsOOMKill(contents string) (int, bool) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "oom_kill" {
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

func readCgroupFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
