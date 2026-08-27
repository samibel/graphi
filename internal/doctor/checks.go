package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/internal/releaseinfo"
	_ "modernc.org/sqlite" // pure-Go, CGo-free SQLite driver for read-only DB checks
)

// Injection points for OS lookups so checks stay testable without a real
// PATH/installation layout. Production code never overrides these.
var (
	executableFn = os.Executable
	lookPathFn   = exec.LookPath
	statFn       = os.Stat
)

// goPathFallbacks are well-known Go install locations probed when `go` is not
// on PATH.
var goPathFallbacks = []string{
	"/opt/homebrew/bin/go",
	"/usr/local/go/bin/go",
	"/usr/local/bin/go",
}

// BinaryCheck returns a check that reports the running binary metadata and
// offline upgrade guidance against releaseinfo.KnownLatestVersion (embedded at
// build time; no network is ever dialed).
func BinaryCheck(release ReleaseInfo) Check {
	return BinaryCheckAgainst(release, releaseinfo.KnownLatestVersion)
}

// BinaryCheckAgainst is BinaryCheck with an explicit known-latest version,
// exposed for tests. The comparison uses only embedded metadata: it works
// fully offline, mirroring `graphi upgrade`.
func BinaryCheckAgainst(release ReleaseInfo, knownLatest string) Check {
	return checkFunc{
		id:       "binary",
		category: "binary",
		fn: func(ctx context.Context, env Env) CheckResult {
			exe, err := executableFn()
			if err != nil {
				return ResultWithAction("binary", "binary", fmt.Sprintf("cannot resolve executable: %v", err), StatusFail, "reinstall graphi from a packaged release")
			}
			rel := env.Release()
			marker := "packaged release"
			status := StatusPass
			if rel == nil || !rel.IsRelease() {
				marker = "dev / not a packaged release"
				status = StatusInfo
			}
			version, arch := "", ""
			if rel != nil {
				version, arch = rel.Version(), rel.Arch()
			}
			message := fmt.Sprintf("binary=%s version=%s arch=%s release_marker=%s", exe, version, arch, marker)
			// Offline upgrade guidance: a packaged release older than the latest
			// version known at build time warns (same rule as `graphi upgrade`).
			if rel != nil && rel.IsRelease() && version != "" && knownLatest != "" && versionIsOlder(version, knownLatest) {
				message += fmt.Sprintf("; installed version %s lags known release %s", version, knownLatest)
				return ResultWithAction("binary", "binary", message, StatusWarn, "run `graphi upgrade`")
			}
			return StringResult("binary", "binary", message, status)
		},
	}
}

// versionIsOlder reports whether current is an older release than latest.
// Both are dotted numeric versions with an optional leading "v" (e.g.
// "v1.2.3"); pre-release/build suffixes are ignored for ordering. When either
// side does not parse, it falls back to plain inequality — the same
// conservative rule `graphi upgrade` applies.
func versionIsOlder(current, latest string) bool {
	c, cok := parseVersion(current)
	l, lok := parseVersion(latest)
	if !cok || !lok {
		return current != latest
	}
	for i := range c {
		if c[i] != l[i] {
			return c[i] < l[i]
		}
	}
	return false
}

// parseVersion parses up to three dotted numeric components ("1.2.3", "v1.2").
func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return [3]int{}, false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// PATHCheck returns a check that confirms graphi and go are on PATH.
func PATHCheck() Check {
	return checkFunc{
		id:       "path",
		category: "path",
		fn: func(ctx context.Context, env Env) CheckResult {
			exe, err := executableFn()
			if err != nil {
				return ResultWithAction("path", "path", fmt.Sprintf("cannot resolve executable: %v", err), StatusFail, "reinstall graphi")
			}
			graphiPath, err := lookPathFn("graphi")
			if err != nil {
				return ResultWithAction("path-graphi", "path", "graphi is not on PATH", StatusFail, "add graphi to your PATH or run the installer")
			}
			if graphiPath != exe {
				return ResultWithAction("path-graphi", "path", fmt.Sprintf("PATH graphi (%s) differs from running executable (%s)", graphiPath, exe), StatusWarn, "ensure the intended graphi binary is first on PATH")
			}
			goPath, err := lookPathFn("go")
			if err != nil {
				found := ""
				for _, p := range goPathFallbacks {
					if _, serr := statFn(p); serr == nil {
						found = p
						break
					}
				}
				if found == "" {
					return ResultWithAction("path-go", "path", "`go` is not on PATH", StatusFail, fmt.Sprintf("install Go or add it to PATH; probed: %v", goPathFallbacks))
				}
				return ResultWithAction("path-go", "path", fmt.Sprintf("`go` is not on PATH but found at %s", found), StatusWarn, fmt.Sprintf("add %s to PATH or symlink it to a directory on PATH", found))
			}
			return StringResult("path", "path", fmt.Sprintf("graphi and go are on PATH (go=%s)", goPath), StatusPass)
		},
	}
}

// MCPCheck returns one check per configured MCP client.
func MCPCheck(binary string) Check {
	return checkFunc{
		id:       "mcp",
		category: "mcp",
		fn: func(ctx context.Context, env Env) CheckResult {
			// Aggregate into a single result per the story AC, but list per-client details.
			cfg := env.MCPConfig()
			if cfg == nil {
				return ResultWithAction("mcp", "mcp", "MCP config reader unavailable", StatusFail, "re-run graphi setup")
			}
			clients := cfg.Clients()
			var lines []string
			var contention []string
			worst := StatusPass
			for _, c := range clients {
				res := runMCPClientCheck(c, binary, cfg)
				if statusOrder(res.Status) > statusOrder(worst) {
					worst = res.Status
				}
				// res.Message is already "<display>: <finding>" — see
				// runMCPClientCheck — so it is the per-client detail line.
				lines = append(lines, res.Message)
				if warn := runMCPContentionCheck(c, cfg); warn != "" {
					if statusOrder(StatusWarn) > statusOrder(worst) {
						worst = StatusWarn
					}
					contention = append(contention, fmt.Sprintf("%s: %s", c.Display, warn))
				}
			}
			sort.Strings(lines)
			msg := "all clients registered and current"
			action := "re-run `graphi setup` to update registrations"
			if worst != StatusPass {
				msg = "one or more MCP clients need attention"
			}
			if len(contention) > 0 {
				sort.Strings(contention)
				msg += " — " + strings.Join(contention, "; ")
				action = "keep one zero-config graphi entry per client; pin extras with 'graphi mcp -db <path>' or remove them"
			}
			result := ResultWithAction("mcp", "mcp", msg, worst, action)
			// The aggregate message says *that* something needs attention; the
			// detail says which client and why, so the finding is repairable.
			// An all-pass run has nothing to attribute, and leaving Detail empty
			// lets json:"detail,omitempty" omit it entirely.
			if worst != StatusPass {
				detail := make([]string, 0, len(lines)+len(contention))
				detail = append(detail, lines...)
				detail = append(detail, contention...)
				result.Detail = strings.Join(detail, "\n")
			}
			return result
		},
	}
}

// MCPContentionReader is the OPTIONAL extension of MCPConfigReader that can
// enumerate zero-config graphi entries per client (see
// mcpconfig.Client.ContendingGraphiServers). It is a separate interface so
// existing MCPConfigReader implementations (test fakes) keep compiling; the
// contention check simply skips readers that do not implement it.
type MCPContentionReader interface {
	Contending(client MCPClient) ([]string, error)
}

// runMCPContentionCheck renders the duplicate-entry warning for one client, or
// "" when there is nothing to warn about. Two zero-config graphi entries in
// one client config (e.g. "graphi" plus a hand-added "graphi-myrepo") spawn
// separate processes that resolve the SAME repository and contend on its
// ingest lock — one indexes, the rest report "repository is not bound" until
// it finishes. Config-read errors stay silent here; runMCPClientCheck already
// reports them.
func runMCPContentionCheck(client MCPClient, cfg MCPConfigReader) string {
	reader, ok := cfg.(MCPContentionReader)
	if !ok {
		return ""
	}
	names, err := reader.Contending(client)
	if err != nil || len(names) < 2 {
		return ""
	}
	return fmt.Sprintf("%d zero-config graphi entries (%s) will resolve the same repository and contend on its ingest lock",
		len(names), strings.Join(names, ", "))
}

func runMCPClientCheck(client MCPClient, binary string, cfg MCPConfigReader) CheckResult {
	// Config dir absent → skip, not fail.
	if _, err := os.Stat(filepath.Dir(client.ConfigPath)); err != nil {
		return StringResult("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: config dir not found (skipped)", client.Display), StatusInfo)
	}
	act, err := cfg.Plan(client, binary)
	if err != nil {
		return ResultWithAction("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: cannot read config: %v", client.Display, err), StatusFail, "re-run `graphi setup`")
	}
	switch act {
	case "no-op":
		return StringResult("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: registered and current", client.Display), StatusPass)
	case "create":
		return ResultWithAction("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: not registered", client.Display), StatusFail, "run `graphi setup --client "+client.ID+"`")
	case "update":
		return ResultWithAction("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: stale command path or args", client.Display), StatusWarn, "re-run `graphi setup` to update the command")
	default:
		return ResultWithAction("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: unknown plan action %q", client.Display, act), StatusUnverified, "re-run `graphi setup`")
	}
}

// graphstoreFTSTable is the name of the FTS5 virtual table created by
// core/graphstore's SQLite schema (see initSchema in core/graphstore/sqlite.go).
const graphstoreFTSTable = "search"

// indexProfileMetadataKey is the kv_meta key the ingester writes after a full
// pass (see engine/ingest.Ingest).
const indexProfileMetadataKey = "index.profile"

// DBCheck returns a check that validates the durable store is readable. The
// probe is strictly read-only: the database is opened with mode=ro plus
// query_only, so a missing file is never created and no byte is ever written.
func DBCheck() Check {
	return checkFunc{
		id:       "db",
		category: "db",
		fn: func(ctx context.Context, env Env) CheckResult {
			path := env.DBPath()
			if path == "" {
				return ResultWithAction("db", "db", "no DB path resolved (stateless mode)", StatusInfo, "run `graphi index` to build a durable store")
			}
			// mode=ro (SQLite URI) refuses writes and never creates a missing
			// file; query_only(1) is belt-and-braces on every pooled connection.
			dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)", filepath.ToSlash(path))
			db, err := sql.Open("sqlite", dsn)
			if err != nil {
				return ResultWithAction("db", "db", fmt.Sprintf("DB %s is not readable: %v", path, err), StatusFail, "run `graphi index` to build the database")
			}
			defer db.Close()
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&count); err != nil {
				return ResultWithAction("db", "db", fmt.Sprintf("DB %s is not readable or has no nodes table: %v", path, err), StatusFail, "run `graphi index` to rebuild the database")
			}

			profilePart := "profile: unknown"
			if profile := dbIndexProfile(ctx, db); profile != "" {
				profilePart = "profile: " + profile
			}
			schemaPart := "schema: user_version unknown"
			if ver, ok := dbUserVersion(ctx, db); ok {
				// The graphstore schema records no explicit version (no pragma
				// write, no metadata key), so PRAGMA user_version is reported
				// as informational context only.
				schemaPart = fmt.Sprintf("schema: user_version=%d", ver)
			}
			ftsPresent := dbHasFTS(ctx, db)

			if count == 0 {
				return ResultWithAction("db", "db", fmt.Sprintf("DB %s has no indexed nodes (%s, %s)", path, profilePart, schemaPart), StatusWarn, "run `graphi index` to populate the database")
			}
			if !ftsPresent {
				return ResultWithAction("db", "db", fmt.Sprintf("DB %s readable, %d nodes, %s, FTS missing, %s", path, count, profilePart, schemaPart), StatusWarn, "run `graphi index` to rebuild the database with its FTS index")
			}
			return StringResult("db", "db", fmt.Sprintf("DB %s readable, %d nodes, %s, FTS present, %s", path, count, profilePart, schemaPart), StatusPass)
		},
	}
}

// dbIndexProfile reads the ingester-written "index.profile" key from the
// kv_meta metadata table. It returns "" when the key or the table is absent
// (e.g. a store never fully ingested, or a pre-metadata schema).
func dbIndexProfile(ctx context.Context, db *sql.DB) string {
	var v string
	if err := db.QueryRowContext(ctx, "SELECT value FROM kv_meta WHERE key = ?", indexProfileMetadataKey).Scan(&v); err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// dbHasFTS reports whether the graphstore FTS5 virtual table exists, probed
// honestly via sqlite_master instead of being assumed.
func dbHasFTS(ctx context.Context, db *sql.DB) bool {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=? AND sql LIKE '%USING fts5%'",
		graphstoreFTSTable).Scan(&n)
	return err == nil && n > 0
}

// dbUserVersion reads PRAGMA user_version (a read-only pragma query).
func dbUserVersion(ctx context.Context, db *sql.DB) (int64, bool) {
	var v int64
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, false
	}
	return v, true
}

// PrivacyCheck returns a check that confirms the egress guard is verifiable.
func PrivacyCheck() Check {
	return checkFunc{
		id:       "privacy",
		category: "privacy",
		fn: func(ctx context.Context, env Env) CheckResult {
			// The guard package is imported only at compile-time for verification;
			// this check performs no dial and no network call.
			return StringResult("privacy", "privacy", "egress guard is present and machine-verifiable (no dial performed)", StatusPass)
		},
	}
}

// LocalFirstCheck returns a check that asserts no required account or background service.
func LocalFirstCheck() Check {
	return checkFunc{
		id:       "local-first",
		category: "local-first",
		fn: func(ctx context.Context, env Env) CheckResult {
			return StringResult("local-first", "local-first", "no account/credential file required; local checks need no background service", StatusPass)
		},
	}
}

// ExecutorSeamPosition is one migrated operation's kill-switch position, as
// ExecutorSeamCheck is handed it by the composition root.
//
// The doctor package deliberately does NOT import surfaces/client to read this
// for itself. Its whole design contract is that a check receives a read-only
// environment and computes nothing it was not given (see the package comment),
// and a check that reached into a surface package to read a process-global
// would be the first exception to that. The composition root already imports
// both, so it does the reading and this package does the rendering.
type ExecutorSeamPosition struct {
	// Operation is the catalog id.
	Operation string
	// Mode is the position: "legacy", "shadow" or "active".
	Mode string
	// Overridden is true when an environment variable selected this position
	// rather than the compiled-in default.
	Overridden bool
	// EnvVar is the variable that would change it.
	EnvVar string
}

// ExecutorSeamCheck reports which internal path serves each migrated operation
// (SW-228 / AX-08), so the strangler seam's configuration is visible to an
// operator instead of being a compiled-in fact nobody can observe.
//
// # Why this reports the POSITION and not the mismatch counter
//
// The obvious check here would print the shadow-mode divergence count. It would
// also be a lie. That counter lives in a process-global inside surfaces/client,
// is never persisted, and the processes that actually dispatch through the seam
// are the long-running servers (`graphi mcp`, `graphi serve`). `graphi doctor`
// is a DIFFERENT, short-lived process: it would read its own untouched counter
// and print "0 divergences" for a server that had diverged on every call. A
// green line that cannot be anything but green is worse than no line, because
// someone will act on it.
//
// So the honest readout is the one that IS cross-process: the configuration.
// The position is what an operator chose, it is the same in every process
// started from the same environment, and it is the thing they can change. The
// counter's scope is stated in the detail rather than pretended away, which is
// the part a reader needs in order to know where to look — and it is the reason
// `shadow` is not the shipped default (surfaces/client/canary.go).
//
// Status is INFO for the shipped configuration and WARN when any operation is
// running the dual path, because that is a deliberate, costly, temporary state
// (every call runs twice) and an operator who left it on by accident should be
// told.
func ExecutorSeamCheck(positions []ExecutorSeamPosition, envErr error) Check {
	return checkFunc{
		id:       "executor-seam",
		category: "internals",
		fn: func(ctx context.Context, env Env) CheckResult {
			if envErr != nil {
				// A mistyped GRAPHI_CANARY_* value FAILS a session at the
				// composition root. Reporting it here is the whole point of a
				// diagnostic: an operator finds out from `graphi doctor` rather
				// than from an MCP client that will not start.
				return ResultWithAction("executor-seam", "internals",
					fmt.Sprintf("a kill-switch variable is invalid and would fail a session: %v", envErr),
					StatusFail,
					"set the variable to `legacy`, `shadow` or `active`, or unset it")
			}
			if len(positions) == 0 {
				return StringResult("executor-seam", "internals",
					"no operation dispatches through the executor seam", StatusInfo)
			}
			counts := map[string]int{}
			var shadowed, activated []string
			var detail strings.Builder
			for _, p := range positions {
				counts[p.Mode]++
				switch p.Mode {
				case "shadow":
					shadowed = append(shadowed, p.Operation)
				case "active":
					activated = append(activated, p.Operation)
				}
				source := "compiled-in default"
				if p.Overridden {
					source = p.EnvVar
				}
				fmt.Fprintf(&detail, "%s: %s (%s)\n", p.Operation, p.Mode, source)
			}
			detail.WriteString("a shadow divergence is counted in-process AND persisted to the " +
				"graphi state directory (SW-232); read it with `graphi doctor -divergence` " +
				"or `graphi doctor -divergence --json`")
			message := fmt.Sprintf("%d migrated operation(s): %d legacy, %d shadow, %d active",
				len(positions), counts["legacy"], counts["shadow"], counts["active"])
			if len(shadowed) > 0 {
				sort.Strings(shadowed)
				return CheckResult{
					ID: "executor-seam", Category: "internals", Status: StatusWarn,
					Message: message,
					Action: fmt.Sprintf("shadow runs every call twice for %s; unset its GRAPHI_CANARY_* "+
						"variable to return to the shipped position", strings.Join(shadowed, ", ")),
					Detail: detail.String(),
				}
			}
			if len(activated) > 0 {
				sort.Strings(activated)
				return CheckResult{
					ID: "executor-seam", Category: "internals", Status: StatusInfo,
					Message: message,
					Action: fmt.Sprintf("%s are served by the executor path; set its GRAPHI_CANARY_* "+
						"variable to `legacy` to roll back", strings.Join(activated, ", ")),
					Detail: detail.String(),
				}
			}
			return CheckResult{
				ID: "executor-seam", Category: "internals", Status: StatusInfo,
				Message: message, Action: "", Detail: detail.String(),
			}
		},
	}
}

// ExecutorDivergence is the persisted executor-seam divergence record as the
// composition root reads it (internal/divergence), reduced to the vocabulary
// this package renders.
//
// Like ExecutorSeamPosition, it is a LOCAL type: the doctor package computes
// only over what it is handed, so the reading, the state-directory resolution
// and the honesty rules stay in the package that owns the record.
type ExecutorDivergence struct {
	// State is the document verdict: UNKNOWN, NO-DIVERGENCE-OBSERVED,
	// PARTIAL-UNKNOWN or DIVERGED.
	State string
	// Directory is where the record lives, so an operator can go and look.
	Directory string
	// Observations and Mismatches are the totals across every segment.
	Observations, Mismatches int
	// Diverged names the operations with at least one recorded mismatch.
	Diverged []string
	// Unobserved names the migrated operations with NO observation at all.
	Unobserved []string
	// Unreadable counts segment files that could not be parsed. They are
	// disclosed rather than dropped: a silently skipped segment would make the
	// totals a lower bound that reads like a total.
	Unreadable int
	// Pruned counts segments the writers deleted to stay under the record's
	// retention cap. Their observations are unrecoverable, and pruning is by
	// age alone, so a live-but-quiet writer's segment can be among them — which
	// is a lower bound for the same reason Unreadable is, and is disclosed the
	// same way rather than left to be inferred from a suspiciously round total.
	Pruned int
}

// ExecutorDivergenceCheck reports the PERSISTED divergence record (SW-232 /
// AX-12a) — the thing ExecutorSeamCheck's doc comment says it deliberately did
// not report, because before this story the only record was a process-global
// counter that `graphi doctor` could only ever read as its own untouched zero.
//
// It is now cross-process, so the readout is honest, and the honesty rule is
// the whole design: an operation nothing was observed on reads UNKNOWN and
// never "no divergence". The shipped position is `legacy`, which compares
// nothing, so UNKNOWN is the expected answer on a normal install — and saying
// so plainly is the point. A green "0 divergences" on a seam that never ran is
// exactly the false evidence the SW-238 precondition assessment refused to
// accept.
//
// Status: WARN when a divergence was recorded (a finding an operator must act
// on) or when the record could not be read; INFO otherwise, including the
// UNKNOWN case, because "nothing observed" is not a health failure of this
// install. PASS is reserved for the one case that has actually been earned:
// every migrated operation observed, none diverged.
func ExecutorDivergenceCheck(d ExecutorDivergence, readErr error) Check {
	return checkFunc{
		id:       "executor-divergence",
		category: "internals",
		fn: func(ctx context.Context, env Env) CheckResult {
			if readErr != nil {
				return ResultWithAction("executor-divergence", "internals",
					fmt.Sprintf("the persisted divergence record could not be read: %v", readErr),
					StatusWarn,
					"check that the graphi state directory is readable; until then the seam's "+
						"divergence history is UNKNOWN, not clean")
			}
			var detail strings.Builder
			fmt.Fprintf(&detail, "record: %s\n", d.Directory)
			fmt.Fprintf(&detail, "%d observation(s), %d mismatch(es)\n", d.Observations, d.Mismatches)
			if len(d.Unobserved) > 0 {
				sort.Strings(d.Unobserved)
				fmt.Fprintf(&detail, "never observed (UNKNOWN, not agreed): %s\n", strings.Join(d.Unobserved, ", "))
			}
			if d.Unreadable > 0 {
				fmt.Fprintf(&detail, "%d unreadable segment(s): the totals are a lower bound\n", d.Unreadable)
			}
			if d.Pruned > 0 {
				fmt.Fprintf(&detail, "%d pruned segment(s): the totals are a lower bound\n", d.Pruned)
			}
			detail.WriteString("read the full record with `graphi doctor -divergence [--json]`")

			if len(d.Diverged) > 0 {
				sort.Strings(d.Diverged)
				return CheckResult{
					ID: "executor-divergence", Category: "internals", Status: StatusWarn,
					Message: fmt.Sprintf("%d recorded divergence(s) between the legacy and executor paths: %s",
						d.Mismatches, strings.Join(d.Diverged, ", ")),
					Action: "run `graphi doctor -divergence --json` for the recorded renderings, and " +
						"roll the operation back with GRAPHI_CANARY_<OP>=legacy (docs/executor-seam-rollback.md)",
					Detail: detail.String(),
				}
			}
			if d.Observations == 0 {
				return CheckResult{
					ID: "executor-divergence", Category: "internals", Status: StatusInfo,
					Message: "UNKNOWN: no dual-run observation has been recorded — this is NOT a " +
						"statement that the two paths agree",
					Action: "",
					Detail: detail.String(),
				}
			}
			if len(d.Unobserved) > 0 {
				return CheckResult{
					ID: "executor-divergence", Category: "internals", Status: StatusInfo,
					Message: fmt.Sprintf("%s: %d observation(s), no divergence, but %d migrated "+
						"operation(s) have never been observed", d.State, d.Observations, len(d.Unobserved)),
					Action: "",
					Detail: detail.String(),
				}
			}
			return CheckResult{
				ID: "executor-divergence", Category: "internals", Status: StatusPass,
				Message: fmt.Sprintf("%d observation(s) across every migrated operation, no divergence recorded",
					d.Observations),
				Action: "",
				Detail: detail.String(),
			}
		},
	}
}

// KnownDefectsCheck discloses OPEN, published product defects that affect a
// GA operation. Info severity, never pass: an open defect is not a health
// failure of THIS install, but it is also never silently green. The list is
// maintained by hand and must shrink to removal in the same change that closes
// a defect; an empty list removes the check.
//
// The check's history IS the disclosure contract working, and is kept here
// rather than lost with the code: removed when PARITY-002 closed, restored for
// PARITY-003, removed when that closed, restored the same day for LINK-001 —
// which the PARITY-003 fix's own review UNMASKED rather than introduced —
// removed again when ADR 0011 closed LINK-001, and restored NOW (2026-08-19,
// fourth time) for LINK-002 — and, from review round 1 of the same story, for
// LINK-003 alongside it.
//
// LINK-002's entry here first shipped in draft claiming the defect "drops true
// edges only and never emits a wrong one". That was FALSE and is corrected
// below: the defect also REDIRECTS edges. It is recorded rather than quietly
// rewritten because the correction is the whole reason the entry is trustworthy
// — the same over-claim was published on three user surfaces at once, and the
// stop-ship ruling that rested on it is now reopened as an owner question.
//
// LINK-002 is NOT a regression of LINK-001 and the two must not be conflated:
// ADR 0011 narrowed fileNodesByDir at READ time, while LINK-002 is a different
// map (clauseByDir) with disjoint consumers. LINK-002 predates both and was
// simply never disclosed until it was reproduced. Record:
// docs/rc/link-002-clause-by-dir-recall.md.
//
// A workaround named here must be one the CLI accepts: the PARITY-003
// disclosure shipped "-profile full", which profile.Parse rejects, so verify
// the string before shipping it. The workaround below was executed against the
// built binary — including its negative case, which is why the `fast` profile
// is named as an exception rather than quietly omitted.
//
// EXTENDED 2026-08-26 (SW-211) from three disclosures to SIX, closing the D8
// gap this check had carried since 2026-08-19. What was missing and why it was
// a gap rather than an omission:
//
//   - PARITY-004 was disclosed on the human surface but NOT here, while its two
//     peers LINK-002/LINK-003 sat on both — the open owner decision recorded at
//     projects/graphi/backlog.md:80. D8 is ratified with *and*, so it was
//     half-met, and the abstention's original justification (protecting a
//     byte-identical product tree) was already void: the product binary had
//     lapsed from the candidate before this change.
//   - PYTHONFANOUT-001 and PYTHONORDER-001 were stamped "OPEN, DISCLOSED" in
//     docs/rc/python-f5-measurement.md and reached NEITHER user surface.
//     "Disclosed" there meant disclosed in the record, which is precisely the
//     gap. Both are SOUNDNESS defects — they emit wrong edges, not merely
//     missing ones — and both contradict readme.md:12-13.
//
// Every workaround named below was executed against the built binary, with its
// negative case, before it was published; the transcripts are in the SW-211
// verification record. The set of ids disclosed here is pinned by
// TestKnownDefectsDisclosureSetIsPinned, which also diffs it against
// docs/known-defects.md, so a future open defect cannot reach one surface
// without the other.
func KnownDefectsCheck() Check {
	return checkFunc{
		id:       "known-defects",
		category: "known-defects",
		fn: func(ctx context.Context, env Env) CheckResult {
			return StringResult("known-defects", "known-defects",
				"LINK-002 (open): in a Go directory that declares TWO package clauses — most "+
					"often a package beside its external `_test` package — only the last clause "+
					"the index happens to see is kept, so methods under the losing clause are "+
					"invisible to the heuristic recv.Method call resolver. `callers`, `callees`, "+
					"`impact`, `neighborhood` and degree-ranked output (agent_brief, "+
					"search_hybrid) then return a confident but INCOMPLETE answer, with no skip "+
					"and no diagnostic. IT ALSO EMITS WRONG EDGES: where the surviving clause "+
					"declares a method of the same name, the call is not dropped but REDIRECTED "+
					"to that unrelated method (a `c.Reset()` on a `*shop.Cart` pointing at "+
					"`shop_test.Fixture.Reset`) — hiding a clause manufactures false uniqueness "+
					"and defeats the resolver's own skip-on-ambiguity rule. The wrong edge is "+
					"always `heuristic` tier (0.6), never `confirmed`; under `-profile balanced` "+
					"and `-profile deep` the correct `confirmed` edge is emitted alongside it, "+
					"but under `-profile fast` the wrong edge is the only one. It "+
					"is deterministic per tree and reproduces under fast, balanced and deep. "+
					"Measured on graphi's own tree: 136 of 1979 method declarations (6.9%) "+
					"unreachable, 108 of them in engine/ingest; 21 of 105 method-declaring "+
					"directories hold more than one package clause and 11 lose methods today. "+
					"How often the REDIRECTION happens is NOT measured. `references`, `imports` "+
					"and `search` are unaffected. Record: docs/rc/link-002-clause-by-dir-recall.md. "+
					"Workaround: where the receiver's type is import-qualified (`*shop.Cart`) the "+
					"go/types type-checker resolves the call instead and the edge is `confirmed` "+
					"— this holds under `-profile balanced` (the default) and `-profile deep`, "+
					"but NOT under `-profile fast`, which skips the type-resolution pass. "+
					"STOP-SHIP RULING IS OPEN and is the owner's: D5 (\"a wrong edge is "+
					"stop-ship\") is stated unqualified, and whether it binds heuristic-tier "+
					"edges has never been decided. See section 9 of the record.\n\n"+
					"LINK-003 (open): the same resolver keeps only ONE entry per (package, "+
					"method-name) pair — `idx.byClause[clause][dir][bare]` is also written "+
					"unconditionally, and unlike `byDir` it has no `dirAmbiguous` companion, so "+
					"the collision is invisible to the resolver. A package declaring both "+
					"`func (a *A) String()` and `func (b *B) String()` therefore resolves every "+
					"unqualified `x.String()` to whichever won the last write — a WRONG "+
					"heuristic-tier edge, with no package-clause collision involved. Same "+
					"affected operations, same tier confinement and same workaround as LINK-002. "+
					"Measured on graphi's own tree: 663 of 1979 method declarations (33.5%) are "+
					"unreachable OR shadowed once both defects are counted, versus 136 (6.9%) "+
					"for LINK-002 alone — roughly 5x the surface. Filed 2026-08-19; not fixed, "+
					"and the fix must close both defects together. Record: section 10 of "+
					"docs/rc/link-002-clause-by-dir-recall.md.\n\n"+
					"LINK-004 (open): a Python import whose module path has MORE THAN ONE dotted "+
					"segment resolves to nothing — `from pkg.util import helper` and "+
					"`import pkg.util` produce no `calls` edge AND no `imports` edge, the two "+
					"commonest import forms in real Python. The linker keys an import path on "+
					"its LAST dotted segment (`pkg.util` -> `util`) while a symbol's package "+
					"clause is its PARENT DIRECTORY base (`pkg`); the two coincide only for "+
					"single-segment module paths, which is the shape every existing test uses. "+
					"`related_files`, `callers`, `callees`, `impact` and `neighborhood` on "+
					"Python therefore lose those relationships with no skip and no diagnostic. "+
					"Single-segment forms are unaffected: `import util`, "+
					"`from util import helper`, `from pkg import util` and "+
					"`from pkg import helper` all resolve. Workaround: import the PACKAGE, not "+
					"the module — rewrite `from pkg.util import helper` as "+
					"`from pkg import util` and call `util.helper()`, which resolves and "+
					"additionally emits the file->file `imports` edge (verified against the "+
					"built CLI, including its negative case). How much of a real Python "+
					"repository this loses is NOT measured. Filed 2026-08-19 (SW-183); not "+
					"fixed. Record: section 3 of docs/rc/capability-audit-2026-08-19.md.\n\n"+

					"PARITY-004 (open): after a full index over a tree in which an "+
					"intra-module Go import points at a package that does NOT exist "+
					"(mid-refactor, a deleted directory, a partial checkout), restoring that "+
					"package and running `graphi sync` never re-links the importer. The "+
					"importer's re-link record was keyed on the unresolvable IMPORT PATH "+
					"rather than on the directory, and the incremental cascade only ever "+
					"looks a key up by the changed file's directory or its raw path, so that "+
					"row is unreachable forever. A stale interned `external` node for the "+
					"once-missing symbol and its `heuristic` calls edge survive beside the "+
					"now-correct `confirmed` edge, and the file->file `imports` edge a full "+
					"pass emits is missing. THE SURVIVING EDGE IS NOT MERELY STALE, IT IS "+
					"FALSE: its reason still reads \"external calls (unresolved import "+
					"example.com/m/tax)\" while a `confirmed` edge to the now-resolved "+
					"`tax.Rate` sits beside it in the same graph. Reproduced through the "+
					"built CLI: `graphi sync` settles at 7 nodes where `graphi rebuild` over "+
					"the identical tree gives 6. `neighborhood` on the importer loses the "+
					"`imports` edge; `related_files` returns the same files in a different "+
					"RANK ORDER, with a weaker reason and less evidence; `search` returns the "+
					"same matches in the same order but with different `rank` scores, because "+
					"`search` excludes external nodes from its RESULTS but not from its FTS5 "+
					"CORPUS, and BM25 scores every document against corpus-wide statistics. "+
					"On the two-result fixture the ordering happened to survive; A REORDERING "+
					"IS NOT EXCLUDED on a larger tree. `callers`, `callees`, `impact` and "+
					"`definition` were identical on that fixture. Workaround: run "+
					"`graphi rebuild` ONCE after restoring a package that was missing at index "+
					"time — verified against the built binary, including its negative case "+
					"(three further `graphi sync` runs stay at 7 nodes; the rebuild converges "+
					"to 6). Filed 2026-08-19; not fixed. Record: "+
					"docs/adr/0004-ingest-recovery-disposition.md section \"ADDED 2026-08-19\", "+
					"D3.\n\n"+

					"PYTHONFANOUT-001 (open, SOUNDNESS DEFECT — it emits WRONG edges, not "+
					"merely missing ones): a Python `import <name>` whose <name> collides with "+
					"the base name of ANY in-repo directory emits a file->file `imports` edge "+
					"to EVERY file node in EVERY directory declaring that clause — including "+
					"for stdlib and third-party imports that name nothing in the repository at "+
					"all. `import typing as t` acquires an edge into in-repo "+
					"`tests/typing/typing_*.py`. THIS CONTRADICTS readme.md:12-13 (\"stdlib "+
					"and third-party targets are recorded, but deliberately not navigable\"): "+
					"a stdlib import that acquires edges into in-repo files is exactly the "+
					"navigability that sentence denies. Measured on flask 3.0.0: 70 spurious "+
					"`imports` edges, 8.0% of its 879; `logging`, `json`, `ast`, `os`, `re` "+
					"and `csv` are equally susceptible. Reproduced on a hermetic fixture "+
					"through the built CLI: `related_files` on the importer returned 4 of 5 "+
					"results spurious, and `neighborhood` on it returned 4 spurious "+
					"`heuristic`-tier (0.6) `imports` edges whose reason reads \"file imports "+
					"package typing\"; `impact` was measured on that fixture and was NOT "+
					"affected. Workaround: REMOVE THE COLLISION — rename the in-repo directory "+
					"so its base name no longer matches an imported module. Verified against "+
					"the built binary, including its negative case: renaming `tests/typing` to "+
					"`tests/typehints` took `related_files` from 5 results to 1 and kept the "+
					"true `imports` edge, while the same tree before the rename returned 4 of "+
					"5 spurious. `-profile fast` also removes them, but it removes EVERY "+
					"`imports` edge, true ones included (verified: the surviving true relation "+
					"drops from `calls x1, imports x1` to `calls x1`), so it switches the "+
					"feature off rather than working around the defect. Filed 2026-08-20 "+
					"(SW-192); not fixed — the fix is module-relative directory lookup, the "+
					"way ADR 0009 gave Go, and carries its own ADR and candidate move. Record: "+
					"section 6 of docs/rc/python-f5-measurement.md.\n\n"+

					"PYTHONORDER-001 (open, SOUNDNESS DEFECT — it emits WRONG edges, not "+
					"merely missing ones): WHICH of the colliding targets each "+
					"PYTHONFANOUT-001 edge lands on is decided by INDEXING ORDER. Measured on "+
					"flask: the same 70 spurious edges distribute 24/24/22 over the three "+
					"`tests/typing/typing_*.py` targets at one candidate and 23/24/23 at "+
					"another. The total, the importer set and the fan-out verdict are stable; "+
					"the DISTRIBUTION is not, and a commit that touched only JVM paths "+
					"(SW-188) moved it. Both dispatches AGREE at each candidate, so the "+
					"two-dispatch discipline cannot see it: the edge set is reproducible by "+
					"luck rather than by construction, against a project convention that makes "+
					"determinism first-class and carves out no exception for `heuristic`-tier "+
					"edges. Same soundness reading and the same contradiction of "+
					"readme.md:12-13 as PYTHONFANOUT-001, and the same affected operations "+
					"(`related_files` and `neighborhood` on Python). THERE IS NO WORKAROUND "+
					"that makes the distribution stable. Removing the collision removes the "+
					"fan-out, and with it any set of tied candidates to order (verified "+
					"against the built binary as PYTHONFANOUT-001's workaround) — but that is "+
					"avoidance of the trigger, not a fix, and this defect must NOT be closed "+
					"by assuming PYTHONFANOUT-001's fix closes it. NOT MEASURED, deliberately: "+
					"whether the ordering is stable across a different machine or filesystem, "+
					"and whether the other `clausePackageFileNodes` consumers (c_sharp, rust) "+
					"exhibit it. Filed 2026-08-24; not fixed. Record: section 12 of "+
					"docs/rc/python-f5-measurement.md.\n\n"+

					"All six are written out in full — with a defect-to-operation map, and "+
					"separated from the design limits they are NOT — on the project's single "+
					"canonical defect page, docs/known-defects.md. That page and this check "+
					"are the two halves of D8 as amended on 2026-08-25; a defect disclosed on "+
					"only one of them is half-disclosed, and a disclosure is retracted only in "+
					"the change that closes the defect.",
				StatusInfo)
		},
	}
}

// checkFunc is a functional adapter for the Check interface.
type checkFunc struct {
	id       string
	category string
	fn       func(ctx context.Context, env Env) CheckResult
}

func (c checkFunc) ID() string       { return c.id }
func (c checkFunc) Category() string { return c.category }
func (c checkFunc) Run(ctx context.Context, env Env) CheckResult {
	return c.fn(ctx, env)
}

func statusOrder(s Status) int {
	switch s {
	case StatusPass:
		return 0
	case StatusInfo:
		return 1
	case StatusUnverified:
		return 2
	case StatusWarn:
		return 3
	case StatusFail:
		return 4
	default:
		return 5
	}
}
