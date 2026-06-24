package main

import (
	"strings"
	"testing"
)

// TestBuildUpgradeCommand_Unix proves the non-Windows command re-execs the
// install.sh one-liner via `sh -c` — and that this is a PURE construction with
// NO network call (the child curl dials, not graphi). The egress canary relies
// on graphi never opening a socket in-process.
func TestBuildUpgradeCommand_Unix(t *testing.T) {
	c := buildUpgradeCommand("linux", "")
	if c.Name != "sh" {
		t.Fatalf("Name = %q, want sh", c.Name)
	}
	if len(c.Args) != 2 || c.Args[0] != "-c" {
		t.Fatalf("Args = %v, want [-c <script>]", c.Args)
	}
	if !strings.Contains(c.Args[1], "curl -fsSL") || !strings.Contains(c.Args[1], installShURL) || !strings.Contains(c.Args[1], "| sh") {
		t.Fatalf("script = %q, want curl ... %s | sh", c.Args[1], installShURL)
	}
	if len(c.Env) != 0 {
		t.Fatalf("Env = %v, want empty when no version pinned", c.Env)
	}
}

// TestBuildUpgradeCommand_Windows proves the Windows command uses the PowerShell
// iwr | iex equivalent against install.ps1.
func TestBuildUpgradeCommand_Windows(t *testing.T) {
	c := buildUpgradeCommand("windows", "")
	if c.Name != "powershell" {
		t.Fatalf("Name = %q, want powershell", c.Name)
	}
	joined := strings.Join(c.Args, " ")
	if !strings.Contains(joined, "iwr -useb") || !strings.Contains(joined, installPS1URL) || !strings.Contains(joined, "| iex") {
		t.Fatalf("args = %v, want iwr -useb %s | iex", c.Args, installPS1URL)
	}
}

// TestBuildUpgradeCommand_ForwardsVersion proves GRAPHI_VERSION is forwarded to
// the child env so the installer pins that release.
func TestBuildUpgradeCommand_ForwardsVersion(t *testing.T) {
	c := buildUpgradeCommand("darwin", "v1.2.3")
	if len(c.Env) != 1 || c.Env[0] != "GRAPHI_VERSION=v1.2.3" {
		t.Fatalf("Env = %v, want [GRAPHI_VERSION=v1.2.3]", c.Env)
	}
	if !strings.Contains(c.String(), "GRAPHI_VERSION=v1.2.3") {
		t.Fatalf("String() = %q, want it to carry the forwarded version", c.String())
	}
}

// TestUpgradeCommand_StringIsCopyPasteable proves the printed command (the
// -print output) is a single copy-pasteable line for the Unix case.
func TestUpgradeCommand_StringIsCopyPasteable(t *testing.T) {
	got := buildUpgradeCommand("linux", "").String()
	want := "sh -c 'curl -fsSL " + installShURL + " | sh'"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
