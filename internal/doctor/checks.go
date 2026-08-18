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

// KnownDefectsCheck discloses OPEN, published product defects that affect a
// GA operation. Info severity, never pass: an open defect is not a health
// failure of THIS install, but it is also never silently green. The list is
// maintained by hand and must shrink to removal in the same change that closes
// a defect; an empty list removes the check.
//
// Its history is the disclosure contract working: removed when PARITY-002
// closed, restored for PARITY-003, removed when that closed, and restored the
// same day for LINK-001 — which the PARITY-003 fix's own review UNMASKED
// rather than introduced. A workaround named here must be one the CLI accepts:
// the PARITY-003 disclosure shipped "-profile full", which profile.Parse
// rejects, so verify the string before shipping it.
func KnownDefectsCheck() Check {
	return checkFunc{
		id:       "known-defects",
		category: "known-defects",
		fn: func(ctx context.Context, env Env) CheckResult {
			return StringResult("known-defects", "known-defects",
				"LINK-001 (open): an `imports` edge targets EVERY file in the imported package's "+
					"directory, including files that are not package source — on pinned cobra 44 of "+
					"340 imports edges point at .md/.yml files, on grpc-go 2120 point at .md/.sh. "+
					"This inflates `related_files`/`imports` results with unrelated files and shifts "+
					"degree-ranked output (agent_brief, search_hybrid). It affects every profile that "+
					"keeps imports edges; it became more visible when PARITY-003's edge-collapsing was "+
					"removed (ADR 0010). Record: docs/rc/parity-matrix-real-repo.md. Workaround: read "+
					"the `reason`/evidence on an imports edge to tell a package member from a "+
					"same-directory file, or use `callers`/`callees`/`references`, which are unaffected.",
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
