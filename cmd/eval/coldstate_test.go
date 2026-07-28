package main

// SW-124 (P0-C1, AC-1): coldness is verified per run, not assumed.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func TestPrepareCold_DetectsPreexistingState(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repo.db")
	metaDir := filepath.Join(dir, "repo-meta")

	// A genuinely fresh pair, on a class that imposes no cache protocol.
	fresh := prepareCold(context.Background(), coldProtocol{requiredProtocol: "uncontrolled"}, dbPath, metaDir)
	if !fresh.Verified {
		t.Fatalf("a fresh store and meta directory must verify as cold: %+v", fresh)
	}
	if fresh.StorePath != dbPath || fresh.MetaPath != metaDir {
		t.Error("the record must name the paths the coldness claim is about")
	}
	if fresh.RequiredProtocol != "uncontrolled" {
		t.Error("the class's declared cache protocol must travel with the observation")
	}

	// A store left behind by an earlier pass means the "cold" index would
	// resume from committed state.
	if err := os.WriteFile(dbPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	reused := prepareCold(context.Background(), coldProtocol{}, dbPath, metaDir)
	if reused.Verified {
		t.Fatal("a pre-existing store must not verify as cold")
	}
	if !reused.StorePreexisting || reused.Reason == "" {
		t.Errorf("the pre-existing store must be recorded with a reason: %+v", reused)
	}
	// Detected, not deleted: a silent cleanup would hide the harness bug that
	// produced the reuse.
	if _, err := os.Stat(dbPath); err != nil {
		t.Error("prepareCold must not delete pre-existing state")
	}

	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := prepareCold(context.Background(), coldProtocol{}, filepath.Join(dir, "other.db"), metaDir)
	if meta.Verified || !meta.MetaPreexisting {
		t.Errorf("a pre-existing ingest meta directory must not verify as cold: %+v", meta)
	}
}

// The reference class declares a drop-caches protocol. A run that did not
// achieve it is not cold BY ITS OWN DECLARATION, whatever the reason.
func TestPrepareCold_ReferenceClassRequiresTheDeclaredProtocol(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repo.db")
	metaDir := filepath.Join(dir, "repo-meta")

	// Not even requested: the declaration is unmet and the run says so.
	notRequested := prepareCold(context.Background(), coldProtocol{dropRequired: true, requiredProtocol: "drop_caches=3 before each timed index"}, dbPath, metaDir)
	if notRequested.Verified {
		t.Fatal("a reference-class run that never dropped the page cache must not verify as cold")
	}
	if notRequested.PageCache != evalreport.PageCacheNotDropped {
		t.Errorf("page_cache = %q, want %q", notRequested.PageCache, evalreport.PageCacheNotDropped)
	}

	// Requested: whether it succeeds is host-dependent (root/sudo on linux,
	// unsupported elsewhere), so the invariant asserted here is the one that
	// must hold everywhere — the verdict follows the OBSERVATION, and the
	// command actually executed is on the record.
	attempted := prepareCold(context.Background(), coldProtocol{drop: true, dropRequired: true, requiredProtocol: "drop_caches=3"}, dbPath, metaDir)
	if attempted.PageCache == evalreport.PageCacheUnsupported {
		// No protocol exists on this OS, so there is no argv to record — but
		// the state must say "unsupported" rather than "not dropped", so a
		// reader can tell "we could not" from "we did not".
		if attempted.PageCacheError == "" {
			t.Error("an unsupported protocol must record why it is unsupported")
		}
	} else if len(attempted.PageCacheCommand) == 0 {
		t.Error("the protocol's argv must be recorded, so what ran is auditable rather than described")
	}
	if want := attempted.PageCache == evalreport.PageCacheDropped; attempted.Verified != want {
		t.Errorf("verified = %v but page_cache = %q: the verdict must follow the observation", attempted.Verified, attempted.PageCache)
	}
	if attempted.PageCache != evalreport.PageCacheDropped && attempted.PageCacheError == "" {
		t.Error("a protocol that did not succeed must record why")
	}
}

func TestReadCgroupMemoryLimits(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "system.slice", "graphi.scope")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(scope, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.max", "8589934592\n")
	write("memory.swap.max", "0\n")
	write("memory.events", "low 0\nhigh 0\nmax 12\noom 3\noom_kill 2\n")

	procPath := filepath.Join(root, "cgroup")
	if err := os.WriteFile(procPath, []byte("0::/system.slice/graphi.scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	limits := readCgroupMemoryLimits(procPath, root)
	if !limits.Available {
		t.Fatalf("limits not available: %+v", limits)
	}
	// Verbatim: "8589934592" is compared against the contract's exact figure,
	// and normalising would erase the difference between a limit and "max".
	if limits.MemoryMax != "8589934592" || limits.MemorySwapMax != "0" {
		t.Errorf("limits = %+v, want the values verbatim", limits)
	}
	if !limits.OOMKillCollected || limits.OOMKill != 2 {
		t.Errorf("oom_kill = %d collected=%v, want 2 and true", limits.OOMKill, limits.OOMKillCollected)
	}

	// A v1 host cannot impose the SW-123 limit as specified, and says so
	// instead of reporting an empty limit.
	v1 := filepath.Join(root, "cgroup-v1")
	if err := os.WriteFile(v1, []byte("11:memory:/user.slice\n1:name=systemd:/user.slice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readCgroupMemoryLimits(v1, root); got.Available || got.Error == "" {
		t.Errorf("a cgroup v1 host must be reported as unavailable with a reason: %+v", got)
	}
	if got := readCgroupMemoryLimits(filepath.Join(root, "absent"), root); got.Available || got.Error == "" {
		t.Errorf("an unreadable /proc entry must be reported, not silently empty: %+v", got)
	}
}

// An absent oom_kill key is not a count of zero: reporting it as zero would let
// the OOM gate assert an absence it never observed.
func TestParseMemoryEventsOOMKill(t *testing.T) {
	if n, ok := parseMemoryEventsOOMKill("low 0\noom_kill 5\n"); !ok || n != 5 {
		t.Errorf("parsed %d, ok=%v, want 5 and true", n, ok)
	}
	if _, ok := parseMemoryEventsOOMKill("low 0\nhigh 0\n"); ok {
		t.Error("a file without oom_kill must report not-collected, not zero")
	}
	if _, ok := parseMemoryEventsOOMKill("oom_kill notanumber\n"); ok {
		t.Error("an unparseable counter must report not-collected")
	}
}
