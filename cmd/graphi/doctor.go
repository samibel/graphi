package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/samibel/graphi/internal/doctor"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/releaseinfo"
	"github.com/samibel/graphi/internal/state"
)

// runDoctor implements the read-only `graphi doctor` subcommand.
//
//	graphi doctor [-db path] [-daemon socket] [--json]
//
// It performs no store mutation, no ingest, and no network dial.
func runDoctor(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	jsonOut := false
	for i, a := range rest {
		if a == "--json" || a == "-json" {
			jsonOut = true
			rest = append(rest[:i], rest[i+1:]...)
			break
		}
	}
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	// socket is ignored for doctor; doctor is read-only and does not connect to a daemon.
	_ = socket

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi doctor: cannot resolve executable: %v\n", err)
		return 1
	}

	env := &realEnv{
		repoRoot:    getwd(),
		dbPath:      dbPath,
		release:     releaseinfo.New(),
		stateReader: stateReader{},
		mcpReader:   mcpConfigReader{clients: mcpconfig.Clients(), binary: exe},
	}

	reg := doctor.NewRegistry()
	reg.Register(doctor.BinaryCheck(env.Release()))
	reg.Register(doctor.PATHCheck())
	reg.Register(doctor.MCPCheck(exe))
	reg.Register(doctor.DBCheck())
	reg.Register(doctor.PrivacyCheck())
	reg.Register(doctor.LocalFirstCheck())

	runner := doctor.NewRunner(reg)
	report := runner.Run(context.Background(), env)

	var w io.Writer = os.Stdout
	if jsonOut {
		if err := doctor.RenderJSON(w, report); err != nil {
			fmt.Fprintf(os.Stderr, "graphi doctor: render json: %v\n", err)
			return 1
		}
	} else {
		if err := doctor.RenderHuman(w, report); err != nil {
			fmt.Fprintf(os.Stderr, "graphi doctor: render human: %v\n", err)
			return 1
		}
	}
	return doctor.ExitCodeFromReport(report)
}

// realEnv is the read-only environment exposed to doctor checks.
type realEnv struct {
	repoRoot    string
	dbPath      string
	release     releaseinfo.Info
	stateReader stateReader
	mcpReader   mcpConfigReader
}

func (e *realEnv) RepoRoot() string                  { return e.repoRoot }
func (e *realEnv) DBPath() string                    { return e.dbPath }
func (e *realEnv) MCPConfig() doctor.MCPConfigReader { return e.mcpReader }
func (e *realEnv) Release() doctor.ReleaseInfo       { return e.release }
func (e *realEnv) State() doctor.StateReader         { return e.stateReader }

// stateReader adapts state.DiscoverDB to the doctor.StateReader interface.
type stateReader struct{}

func (stateReader) DiscoverDB(repoRoot string) (string, error) {
	return state.DiscoverDB(repoRoot, "")
}

// mcpConfigReader adapts mcpconfig.Client to the doctor.MCPConfigReader interface.
type mcpConfigReader struct {
	clients []mcpconfig.Client
	binary  string
}

func (m mcpConfigReader) Clients() []doctor.MCPClient {
	out := make([]doctor.MCPClient, 0, len(m.clients))
	for _, c := range m.clients {
		path, _ := c.ConfigPath()
		out = append(out, doctor.MCPClient{
			ID:         c.ID,
			Display:    c.Display,
			ConfigPath: path,
		})
	}
	return out
}

func (m mcpConfigReader) Plan(client doctor.MCPClient, binary string) (doctor.MCPPlanAction, error) {
	c, ok := mcpconfig.ClientByID(client.ID)
	if !ok {
		return "", fmt.Errorf("unknown client %q", client.ID)
	}
	act, err := c.Plan(binary, nil)
	if err != nil {
		return "", err
	}
	return doctor.MCPPlanAction(act), nil
}

// releaseInfoAdapter adapts releaseinfo.Info to doctor.ReleaseInfo if needed.
// realEnv.Release already returns the value, so this is not used directly.
