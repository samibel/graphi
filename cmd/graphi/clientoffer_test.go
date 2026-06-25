package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/mcpconfig"
)

func TestMaybeOfferClients(t *testing.T) {
	const hint = "detected — run 'graphi setup' to connect graphi."
	const prompt = "found — connect graphi to them? [Y/n]"
	const example = `Try in your agent: "Use graphi to show the callers of <symbol>"`

	// two fake detected clients to exercise the multi-client display list.
	twoClients := []mcpconfig.Client{
		{ID: "claude", Display: "Claude Code"},
		{ID: "cursor", Display: "Cursor"},
	}

	cases := []struct {
		name     string
		isTTY    bool
		input    string
		detected []mcpconfig.Client
		memoPre  bool

		wantSetup    bool
		wantMemo     bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "non-TTY with detected clients: hint only, no write",
			isTTY:        false,
			detected:     twoClients,
			wantSetup:    false,
			wantMemo:     false,
			wantContains: []string{"Claude Code and Cursor " + hint},
			wantAbsent:   []string{prompt},
		},
		{
			name:         "TTY yes connects all and writes memo",
			isTTY:        true,
			input:        "y\n",
			detected:     twoClients,
			wantSetup:    true,
			wantMemo:     true,
			wantContains: []string{"Claude Code and Cursor " + prompt, example},
		},
		{
			name:         "TTY enter defaults to yes",
			isTTY:        true,
			input:        "\n",
			detected:     twoClients,
			wantSetup:    true,
			wantMemo:     true,
			wantContains: []string{prompt, example},
		},
		{
			name:         "TTY no does not connect but writes memo",
			isTTY:        true,
			input:        "n\n",
			detected:     twoClients,
			wantSetup:    false,
			wantMemo:     true,
			wantContains: []string{prompt},
			wantAbsent:   []string{example},
		},
		{
			name:       "TTY memo pre-exists: nothing",
			isTTY:      true,
			input:      "y\n",
			detected:   twoClients,
			memoPre:    true,
			wantSetup:  false,
			wantMemo:   true,
			wantAbsent: []string{prompt, hint},
		},
		{
			name:       "none detected: nothing",
			isTTY:      true,
			input:      "y\n",
			detected:   nil,
			wantSetup:  false,
			wantMemo:   false,
			wantAbsent: []string{prompt, hint},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memoPath := filepath.Join(t.TempDir(), "clients-offered")
			if tc.memoPre {
				if err := os.WriteFile(memoPath, []byte("-"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			var gotClients []mcpconfig.Client
			detect := func() []mcpconfig.Client { return tc.detected }
			doSetup := func(cs []mcpconfig.Client) error { gotClients = cs; return nil }

			maybeOfferClients(strings.NewReader(tc.input), &out, tc.isTTY, memoPath, detect, doSetup)

			if (len(gotClients) > 0) != tc.wantSetup {
				t.Errorf("setup called = %v, want %v", len(gotClients) > 0, tc.wantSetup)
			}
			_, statErr := os.Stat(memoPath)
			gotMemo := statErr == nil
			if gotMemo != tc.wantMemo {
				t.Errorf("memo present = %v, want %v", gotMemo, tc.wantMemo)
			}
			got := out.String()
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\ngot: %q", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("output unexpectedly contains %q\ngot: %q", absent, got)
				}
			}
		})
	}
}

// TestDetectClients_SkipsConnectedAndUnconfigured verifies detection filters to
// installed-but-not-yet-connected clients, using the claude env override.
func TestDetectClients_SkipsConnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	t.Setenv(mcpconfig.EnvOverride, path)

	// Empty config exists → claude is configurable and NOT connected → detected.
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range detectClients() {
		if c.ID == "claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claude among detected (configurable, not connected)")
	}

	// After wiring graphi with the same entry detectClients probes for → skipped.
	self, _ := os.Executable()
	if _, err := mcpconfig.Apply(path, "graphi", mcpconfig.GraphiEntry(self, []string{"mcp"}), false); err != nil {
		t.Fatal(err)
	}
	for _, c := range detectClients() {
		if c.ID == "claude" {
			t.Errorf("claude should be skipped once connected")
		}
	}
}

func TestDisplayList(t *testing.T) {
	mk := func(names ...string) []mcpconfig.Client {
		cs := make([]mcpconfig.Client, len(names))
		for i, n := range names {
			cs[i] = mcpconfig.Client{Display: n}
		}
		return cs
	}
	cases := map[string]string{
		"A":     "A",
		"A|B":   "A and B",
		"A|B|C": "A, B and C",
	}
	for in, want := range cases {
		got := displayList(mk(strings.Split(in, "|")...))
		if got != want {
			t.Errorf("displayList(%q) = %q, want %q", in, got, want)
		}
	}
}
