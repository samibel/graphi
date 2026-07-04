package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultRunners returns the real constituent-gate runners.
func DefaultRunners() map[string]Runner {
	return map[string]Runner{
		"testgate": &shellRunner{
			name:    "testgate",
			cmd:     "go",
			args:    []string{"run", "./cmd/testgate", "-timeout", "15m"},
			env:     append(os.Environ(), "CGO_ENABLED=0"),
			timeout: 16 * time.Minute,
			score:   100,
		},
		"eval": &shellRunner{
			name:    "eval",
			cmd:     "go",
			args:    []string{"run", "./cmd/eval", "-claim-validate", "-manifest", "corpus/manifest.json", "-tier", "1"},
			timeout: 10 * time.Minute,
			score:   100,
		},
		"coverage": &shellRunner{
			name:    "coverage",
			cmd:     "go",
			args:    []string{"run", "./cmd/coverage", "-check"},
			timeout: 5 * time.Minute,
			score:   100,
		},
		"privacy": &privacyRunner{
			timeout: 5 * time.Minute,
			score:   100,
		},
		"perf": &shellRunner{
			name:    "perf",
			cmd:     "go",
			args:    []string{"run", "./cmd/bench", "-budget", "bench/bench-budget.yml"},
			timeout: 10 * time.Minute,
			score:   100,
		},
		"web": &webRunner{
			dir:     filepath.Join(".", "web"),
			timeout: 5 * time.Minute,
			score:   100,
		},
	}
}

type shellRunner struct {
	name    string
	cmd     string
	args    []string
	env     []string
	timeout time.Duration
	score   float64
}

func (r *shellRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.cmd, r.args...)
	cmd.Env = r.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%s: %w: %s", r.name, err, strings.TrimSpace(stderr.String()))
	}
	return r.score, nil
}

// privacyRunner runs `graphi privacy-audit` with the elevation the audit's
// zero-outbound check needs: creating the loopback-only network namespace
// requires CAP_SYS_ADMIN, and without it the audit reports UNVERIFIED, which
// by design exits non-zero (false-green prevention). It mirrors the dedicated
// privacy-audit workflow: build the binary once, then execute it — under
// passwordless sudo when the process is not already root (the GitHub runner
// case). `go run` under sudo would resolve the toolchain as root, so the
// build/execute split is deliberate.
type privacyRunner struct {
	timeout time.Duration
	score   float64
}

func (r *privacyRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "graphi-release-gate-*")
	if err != nil {
		return 0, fmt.Errorf("privacy: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "graphi")

	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/graphi")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buildErr bytes.Buffer
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		return 0, fmt.Errorf("privacy: build graphi: %w: %s", err, strings.TrimSpace(buildErr.String()))
	}

	argv := []string{bin, "privacy-audit"}
	if os.Geteuid() != 0 && sudoAvailable(ctx) {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("privacy: %w: %s%s", err, strings.TrimSpace(out.String()), strings.TrimSpace(stderr.String()))
	}
	return r.score, nil
}

// sudoAvailable probes for passwordless sudo (the GitHub-runner case) without
// ever prompting.
func sudoAvailable(ctx context.Context) bool {
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

type webRunner struct {
	dir     string
	timeout time.Duration
	score   float64
}

func (r *webRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "test")
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "CI=true")
	// Suppress npm update-notifier and other noise; keep deterministic.
	cmd.Env = append(cmd.Env, "NO_UPDATE_NOTIFIER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("web tests: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return r.score, nil
}

func goos() string {
	return runtime.GOOS
}
