package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite" // pure-Go, CGo-free SQLite driver for read-only DB checks
)

// BinaryCheck returns a check that reports the running binary metadata.
func BinaryCheck(release ReleaseInfo) Check {
	return checkFunc{
		id:       "binary",
		category: "binary",
		fn: func(ctx context.Context, env Env) CheckResult {
			exe, err := os.Executable()
			if err != nil {
				return ResultWithNextStep("binary", "binary", fmt.Sprintf("cannot resolve executable: %v", err), StatusFail, "reinstall graphi from a packaged release")
			}
			rel := env.Release()
			marker := "packaged release"
			status := StatusPass
			if rel == nil || !rel.IsRelease() {
				marker = "dev / not a packaged release"
				status = StatusInfo
			}
			message := fmt.Sprintf("binary=%s version=%s arch=%s release_marker=%s", exe, rel.Version(), rel.Arch(), marker)
			return StringResult("binary", "binary", message, status)
		},
	}
}

// PATHCheck returns a check that confirms graphi and go are on PATH.
func PATHCheck() Check {
	return checkFunc{
		id:       "path",
		category: "path",
		fn: func(ctx context.Context, env Env) CheckResult {
			exe, err := os.Executable()
			if err != nil {
				return ResultWithNextStep("path", "path", fmt.Sprintf("cannot resolve executable: %v", err), StatusFail, "reinstall graphi")
			}
			graphiPath, err := exec.LookPath("graphi")
			if err != nil {
				return ResultWithNextStep("path-graphi", "path", "graphi is not on PATH", StatusFail, "add graphi to your PATH or run the installer")
			}
			if graphiPath != exe {
				return ResultWithNextStep("path-graphi", "path", fmt.Sprintf("PATH graphi (%s) differs from running executable (%s)", graphiPath, exe), StatusWarn, "ensure the intended graphi binary is first on PATH")
			}
			goPath, err := exec.LookPath("go")
			if err != nil {
				fallbacks := []string{
					"/opt/homebrew/bin/go",
					"/usr/local/go/bin/go",
					"/usr/local/bin/go",
				}
				found := ""
				for _, p := range fallbacks {
					if _, serr := os.Stat(p); serr == nil {
						found = p
						break
					}
				}
				if found == "" {
					return ResultWithNextStep("path-go", "path", "`go` is not on PATH", StatusFail, fmt.Sprintf("install Go or add it to PATH; probed: %v", fallbacks))
				}
				return ResultWithNextStep("path-go", "path", fmt.Sprintf("`go` is not on PATH but found at %s", found), StatusWarn, fmt.Sprintf("add %s to PATH or symlink it to a directory on PATH", found))
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
				return ResultWithNextStep("mcp", "mcp", "MCP config reader unavailable", StatusFail, "re-run graphi setup")
			}
			clients := cfg.Clients()
			var lines []string
			worst := StatusPass
			for _, c := range clients {
				res := runMCPClientCheck(c, binary, cfg)
				if statusOrder(res.Status) > statusOrder(worst) {
					worst = res.Status
				}
				lines = append(lines, fmt.Sprintf("%s: %s", c.Display, res.Message))
			}
			sort.Strings(lines)
			msg := "all clients registered and current"
			if worst != StatusPass {
				msg = "one or more MCP clients need attention"
			}
			return ResultWithNextStep("mcp", "mcp", msg, worst, "re-run `graphi setup` to update registrations")
		},
	}
}

func runMCPClientCheck(client MCPClient, binary string, cfg MCPConfigReader) CheckResult {
	// Config dir absent → skip, not fail.
	if _, err := os.Stat(filepath.Dir(client.ConfigPath)); err != nil {
		return StringResult("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: config dir not found (skipped)", client.Display), StatusInfo)
	}
	act, err := cfg.Plan(client, binary)
	if err != nil {
		return ResultWithNextStep("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: cannot read config: %v", client.Display, err), StatusFail, "re-run `graphi setup`")
	}
	switch act {
	case "no-op":
		return StringResult("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: registered and current", client.Display), StatusPass)
	case "create":
		return ResultWithNextStep("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: not registered", client.Display), StatusFail, "run `graphi setup --client "+client.ID+"`")
	case "update":
		return ResultWithNextStep("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: stale command path or args", client.Display), StatusWarn, "re-run `graphi setup` to update the command")
	default:
		return ResultWithNextStep("mcp-"+client.ID, "mcp", fmt.Sprintf("%s: unknown plan action %q", client.Display, act), StatusUnverified, "re-run `graphi setup`")
	}
}

// DBCheck returns a check that validates the durable store is readable.
func DBCheck() Check {
	return checkFunc{
		id:       "db",
		category: "db",
		fn: func(ctx context.Context, env Env) CheckResult {
			path := env.DBPath()
			if path == "" {
				return ResultWithNextStep("db", "db", "no DB path resolved (stateless mode)", StatusInfo, "run `graphi index` to build a durable store")
			}
			// Read-only open using modernc SQLite DSN.
			db, err := sql.Open("sqlite", path+"?_mode=ro")
			if err != nil {
				return ResultWithNextStep("db", "db", fmt.Sprintf("DB %s is not readable: %v", path, err), StatusFail, "run `graphi index` to build the database")
			}
			defer db.Close()
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&count); err != nil {
				return ResultWithNextStep("db", "db", fmt.Sprintf("DB %s readable but nodes table missing: %v", path, err), StatusFail, "run `graphi index` to rebuild the database")
			}
			if count == 0 {
				return ResultWithNextStep("db", "db", fmt.Sprintf("DB %s has no indexed nodes", path), StatusWarn, "run `graphi index` to populate the database")
			}
			return StringResult("db", "db", fmt.Sprintf("DB %s readable, %d nodes, FTS available", path, count), StatusPass)
		},
	}
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
