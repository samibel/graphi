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
			db, socket, labs, err := extractMCPFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if db != tc.wantDB || socket != tc.wantSocket || labs != tc.wantLabs {
				t.Fatalf("got db=%q socket=%q labs=%v", db, socket, labs)
			}
		})
	}
}

func TestMCPHelpDocumentsStableDefaultAndLabsOptIn(t *testing.T) {
	var out bytes.Buffer
	if !printSubcommandHelp("mcp", &out) {
		t.Fatal("mcp help missing")
	}
	for _, want := range []string{"Stable tools by default", "-labs"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("MCP help missing %q:\n%s", want, out.String())
		}
	}
}
