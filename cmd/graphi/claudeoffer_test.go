package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/mcpconfig"
)

func TestMaybeOfferClaude(t *testing.T) {
	const hint = "Claude Code detected — run 'graphi claude' to connect graphi."
	const prompt = "Claude Code found — connect graphi? [Y/n]"
	const example = `Try in Claude Code: "Use graphi to show the callers of <symbol>"`

	cases := []struct {
		name        string
		isTTY       bool
		input       string
		configurable bool
		connected   bool
		memoPre     bool // pre-create the memo file

		wantSetup   bool
		wantMemo    bool   // memo present after the call
		wantContains []string
		wantAbsent  []string
	}{
		{
			name:         "non-TTY configurable not-connected: hint only, no write",
			isTTY:        false,
			configurable: true,
			wantSetup:    false,
			wantMemo:     false,
			wantContains: []string{hint},
			wantAbsent:   []string{prompt},
		},
		{
			name:         "TTY yes (y) connects and writes memo",
			isTTY:        true,
			input:        "y\n",
			configurable: true,
			wantSetup:    true,
			wantMemo:     true,
			wantContains: []string{prompt, example},
		},
		{
			name:         "TTY enter defaults to yes",
			isTTY:        true,
			input:        "\n",
			configurable: true,
			wantSetup:    true,
			wantMemo:     true,
			wantContains: []string{prompt, example},
		},
		{
			name:         "TTY no does not connect but writes memo",
			isTTY:        true,
			input:        "n\n",
			configurable: true,
			wantSetup:    false,
			wantMemo:     true,
			wantContains: []string{prompt},
			wantAbsent:   []string{example},
		},
		{
			name:         "TTY memo pre-exists: nothing",
			isTTY:        true,
			input:        "y\n",
			configurable: true,
			memoPre:      true,
			wantSetup:    false,
			wantMemo:     true,
			wantAbsent:   []string{prompt, hint},
		},
		{
			name:         "already connected: nothing",
			isTTY:        true,
			input:        "y\n",
			configurable: true,
			connected:    true,
			wantSetup:    false,
			wantMemo:     false,
			wantAbsent:   []string{prompt, hint},
		},
		{
			name:         "not configurable: nothing",
			isTTY:        true,
			input:        "y\n",
			configurable: false,
			wantSetup:    false,
			wantMemo:     false,
			wantAbsent:   []string{prompt, hint},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memoPath := filepath.Join(t.TempDir(), "claude-offered")
			if tc.memoPre {
				if err := os.WriteFile(memoPath, []byte("-"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			setupCalled := false
			det := func() (bool, bool) { return tc.configurable, tc.connected }
			doSetup := func() error { setupCalled = true; return nil }

			maybeOfferClaude(strings.NewReader(tc.input), &out, tc.isTTY, memoPath, det, doSetup)

			if setupCalled != tc.wantSetup {
				t.Errorf("setup called = %v, want %v", setupCalled, tc.wantSetup)
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

func TestDetectClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	t.Setenv(mcpconfig.EnvOverride, path)

	// Config WITHOUT graphi → configurable (file exists), not connected.
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configurable, connected := detectClaude()
	if !configurable {
		t.Errorf("configurable = false, want true (config file exists)")
	}
	if connected {
		t.Errorf("connected = true, want false (graphi not registered)")
	}

	// Register graphi with the SAME entry detectClaude probes for → connected.
	self, _ := os.Executable()
	if _, err := mcpconfig.Apply(path, "graphi", mcpconfig.GraphiEntry(self, []string{"mcp"}), false); err != nil {
		t.Fatal(err)
	}
	configurable, connected = detectClaude()
	if !configurable {
		t.Errorf("configurable = false after Apply, want true")
	}
	if !connected {
		t.Errorf("connected = false after Apply, want true")
	}
}
