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
		"privacy": &shellRunner{
			name: "privacy",
			// go run avoids depending on a pre-built binary in the checkout
			// (nothing in the gate workflow builds cmd/graphi/graphi).
			cmd:     "go",
			args:    []string{"run", "./cmd/graphi", "privacy-audit"},
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
