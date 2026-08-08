package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/doctor"
	"github.com/samibel/graphi/internal/mcpconfig"
)

// These tests cross the mcpconfig -> doctor adapter (mcpConfigReader.Plan).
// That boundary had no test at all, which is why a vocabulary mismatch between
// mcpconfig.Action ("created"/"updated"/"unchanged") and doctor.MCPPlanAction
// ("create"/"update"/"no-op") survived from 493c7c5e (2026-07-04): every live
// client fell to the check's "unknown plan action" default and `graphi doctor`
// never reported a real MCP registration status. Tests that drive the
// doctor-side constants through fakes cannot catch this by construction — only
// a test that runs the real adapter over a real config file can.

// claudeReaderAt points the "claude" client at configPath (via the documented
// CLAUDE_CONFIG_PATH override) and returns the real adapter restricted to that
// one client, so the check is hermetic and never reads the developer's own
// ~/.claude.json.
func claudeReaderAt(t *testing.T, configPath string) mcpConfigReader {
	t.Helper()
	t.Setenv(mcpconfig.EnvOverride, configPath)
	c, ok := mcpconfig.ClientByID("claude")
	if !ok {
		t.Fatal(`mcpconfig has no "claude" client`)
	}
	return mcpConfigReader{clients: []mcpconfig.Client{c}, binary: doctorTestBinary}
}

// doctorTestBinary is the graphi path the fixtures register; it is never
// executed or stat-ed, only compared.
const doctorTestBinary = "/usr/local/bin/graphi"

// writeClaudeConfig writes a Claude Code config whose mcpServers map is the
// supplied one, and returns the path.
func writeClaudeConfig(t *testing.T, servers map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	b, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// currentGraphiEntry is the entry a current registration holds — built from the
// same constructor `setup` uses, so "current" cannot drift away from the test.
func currentGraphiEntry() any { return mcpconfig.GraphiEntry(doctorTestBinary, nil) }

// TestDoctorMCPPlanAdapterMapsEveryAction pins the adapter contract directly:
// every mcpconfig.Action must arrive as the corresponding doctor.MCPPlanAction,
// never as the raw mcpconfig spelling.
func TestDoctorMCPPlanAdapterMapsEveryAction(t *testing.T) {
	cases := []struct {
		name    string
		servers map[string]any
		want    doctor.MCPPlanAction
	}{
		{
			name:    "entry matches exactly -> no-op",
			servers: map[string]any{"graphi": currentGraphiEntry()},
			want:    doctor.MCPPlanNoOp,
		},
		{
			name: "entry exists but command is stale -> update",
			servers: map[string]any{"graphi": mcpconfig.ServerEntry{
				Type: "stdio", Command: "/opt/old/graphi", Args: []string{"mcp"},
			}},
			want: doctor.MCPPlanUpdate,
		},
		{
			name:    "no graphi entry -> create",
			servers: map[string]any{"something-else": currentGraphiEntry()},
			want:    doctor.MCPPlanCreate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := claudeReaderAt(t, writeClaudeConfig(t, tc.servers))
			got, err := r.Plan(doctor.MCPClient{ID: "claude"}, doctorTestBinary)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if got != tc.want {
				t.Fatalf("adapter returned %q, want %q — the mcpconfig vocabulary must be "+
					"translated, not cast across", got, tc.want)
			}
			// Belt and braces: the raw mcpconfig spellings must never reach doctor.
			for _, raw := range []string{
				string(mcpconfig.ActionCreated),
				string(mcpconfig.ActionUpdated),
				string(mcpconfig.ActionUnchanged),
			} {
				if string(got) == raw {
					t.Fatalf("adapter leaked the raw mcpconfig action %q", raw)
				}
			}
		})
	}
}

// TestDoctorMCPCheckReportsRealRegistrationStatus is the end-to-end assertion
// SW-159 AC2 actually needs: run the real doctor mcp check over the real
// adapter and a real config file, and require a genuine per-client finding in
// Detail rather than "unknown plan action".
func TestDoctorMCPCheckReportsRealRegistrationStatus(t *testing.T) {
	cases := []struct {
		name       string
		servers    map[string]any
		wantStatus doctor.Status
		wantDetail string
	}{
		{
			name:       "registered and current",
			servers:    map[string]any{"graphi": currentGraphiEntry()},
			wantStatus: doctor.StatusPass,
			wantDetail: "", // AC5: an all-pass run carries no detail at all
		},
		{
			name: "stale command path",
			servers: map[string]any{"graphi": mcpconfig.ServerEntry{
				Type: "stdio", Command: "/opt/old/graphi", Args: []string{"mcp"},
			}},
			wantStatus: doctor.StatusWarn,
			wantDetail: "Claude Code: stale command path or args",
		},
		{
			name:       "not registered",
			servers:    map[string]any{"something-else": currentGraphiEntry()},
			wantStatus: doctor.StatusFail,
			wantDetail: "Claude Code: not registered",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := &realEnv{mcpReader: claudeReaderAt(t, writeClaudeConfig(t, tc.servers))}
			res := doctor.MCPCheck(doctorTestBinary).Run(context.Background(), env)

			if strings.Contains(res.Detail, "unknown plan action") ||
				strings.Contains(res.Message, "unknown plan action") {
				t.Fatalf("the mcp check does not understand the adapter's plan action; "+
					"status=%q message=%q detail=%q", res.Status, res.Message, res.Detail)
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (detail: %q)", res.Status, tc.wantStatus, res.Detail)
			}
			if res.Detail != tc.wantDetail {
				t.Fatalf("detail = %q, want %q", res.Detail, tc.wantDetail)
			}
			// The check's identity is unchanged by any of this (SW-159 AC7).
			if res.ID != "mcp" || res.Category != "mcp" {
				t.Fatalf("id/category changed: %q/%q", res.ID, res.Category)
			}
		})
	}
}

// TestDoctorMCPCheckDetailIsMachineReadableInJSON walks the whole shipped path
// for the failing case — check -> report -> RenderJSON — and asserts the
// per-client finding survives into the document agents actually read, at
// schema_version 2.
func TestDoctorMCPCheckDetailIsMachineReadableInJSON(t *testing.T) {
	servers := map[string]any{"something-else": currentGraphiEntry()}
	env := &realEnv{mcpReader: claudeReaderAt(t, writeClaudeConfig(t, servers))}
	res := doctor.MCPCheck(doctorTestBinary).Run(context.Background(), env)

	var buf strings.Builder
	if err := doctor.RenderJSON(&buf, doctor.Report{Results: []doctor.CheckResult{res}}); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Checks        []struct {
			ID     string `json:"id"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &doc); err != nil {
		t.Fatalf("parse doctor JSON: %v", err)
	}
	if doc.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d, want 2", doc.SchemaVersion)
	}
	if len(doc.Checks) != 1 || doc.Checks[0].ID != "mcp" {
		t.Fatalf("unexpected checks: %+v", doc.Checks)
	}
	if doc.Checks[0].Detail != "Claude Code: not registered" {
		t.Fatalf("detail in JSON = %q, want the real per-client finding", doc.Checks[0].Detail)
	}
}
