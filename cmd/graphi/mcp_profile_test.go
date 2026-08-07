package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtractMCPFlags_LabsRequiresExplicitOptIn(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantDB     string
		wantSocket string
		wantLabs   bool
		wantErr    bool
	}{
		{name: "default_stable"},
		{name: "labs", args: []string{"-labs"}, wantLabs: true},
		{name: "long_labs", args: []string{"--labs"}, wantLabs: true},
		{name: "mixed_order", args: []string{"-db", "graph.db", "-labs", "-daemon", "sock"}, wantDB: "graph.db", wantSocket: "sock", wantLabs: true},
		{name: "unknown_fails", args: []string{"-experimental"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, socket, _, _, labs, err := extractMCPFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if db != tc.wantDB || socket != tc.wantSocket || labs != tc.wantLabs {
				t.Fatalf("got db=%q socket=%q labs=%v", db, socket, labs)
			}
		})
	}
}

// TestExtractMCPFlags_MetaSidecar pins the -meta surface: it threads through
// to the -db Attach path exactly like the CLI verbs (previously it was
// extracted and silently dropped, so an attached MCP session never reloaded
// durable semantic vectors), and it errors without -db instead of reading as
// "vectors loaded" when they are not.
func TestExtractMCPFlags_MetaSidecar(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantDB   string
		wantMeta string
		wantErr  bool
	}{
		{name: "meta_with_db", args: []string{"-db", "graph.db", "-meta", "meta"}, wantDB: "graph.db", wantMeta: "meta"},
		{name: "meta_eq", args: []string{"-db=graph.db", "-meta=meta"}, wantDB: "graph.db", wantMeta: "meta"},
		{name: "meta_without_db", args: []string{"-meta", "meta"}, wantErr: true},
		{name: "meta_with_daemon_only", args: []string{"-daemon", "sock", "-meta", "meta"}, wantErr: true},
		{name: "meta_with_root", args: []string{"-root", "/work/mars", "-meta", "meta"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, _, meta, _, _, err := extractMCPFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if db != tc.wantDB || meta != tc.wantMeta {
				t.Fatalf("got db=%q meta=%q, want db=%q meta=%q", db, meta, tc.wantDB, tc.wantMeta)
			}
		})
	}
}

// TestExtractMCPFlags_ExplicitRoot pins the -root flag surface: both spellings
// and separators parse, -labs composes, and the flag contradicts an explicit
// store (-db/-daemon pin a session without repository detection, so combining
// them with -root can only mislead).
func TestExtractMCPFlags_ExplicitRoot(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRoot string
		wantErr  bool
	}{
		{name: "root_space", args: []string{"-root", "/work/mars"}, wantRoot: "/work/mars"},
		{name: "root_eq", args: []string{"-root=/work/mars"}, wantRoot: "/work/mars"},
		{name: "long_root", args: []string{"--root", "/work/mars"}, wantRoot: "/work/mars"},
		{name: "long_root_eq", args: []string{"--root=/work/mars"}, wantRoot: "/work/mars"},
		{name: "root_with_labs", args: []string{"-root", "/work/mars", "-labs"}, wantRoot: "/work/mars"},
		{name: "root_conflicts_db", args: []string{"-root", "/work/mars", "-db", "graph.db"}, wantErr: true},
		{name: "root_conflicts_daemon", args: []string{"-root", "/work/mars", "-daemon", "sock"}, wantErr: true},
		{name: "unknown_still_fails", args: []string{"-root", "/work/mars", "-experimental"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, root, _, err := extractMCPFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if root != tc.wantRoot {
				t.Fatalf("root = %q, want %q", root, tc.wantRoot)
			}
		})
	}
}

// TestMCPRoot_FlagWinsOverEnv pins the explicit-root precedence between the
// two input channels: the -root flag outranks the GRAPHI_ROOT environment.
func TestMCPRoot_FlagWinsOverEnv(t *testing.T) {
	if got := mcpRoot("/flag", "/env"); got != "/flag" {
		t.Fatalf("mcpRoot(flag, env) = %q, want the flag", got)
	}
	if got := mcpRoot("", "/env"); got != "/env" {
		t.Fatalf("mcpRoot(\"\", env) = %q, want the env fallback", got)
	}
	if got := mcpRoot("", ""); got != "" {
		t.Fatalf("mcpRoot(\"\", \"\") = %q, want empty", got)
	}
}

func TestMCPHelpDocumentsStableDefaultAndLabsOptIn(t *testing.T) {
	var out bytes.Buffer
	if !printSubcommandHelp("mcp", &out) {
		t.Fatal("mcp help missing")
	}
	for _, want := range []string{"Stable tools by default", "-labs", "-root", "GRAPHI_ROOT"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("MCP help missing %q:\n%s", want, out.String())
		}
	}
}
