package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/internal/divergence"
	"github.com/samibel/graphi/internal/doctor"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/releaseinfo"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
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
	for _, a := range rest {
		if a == "--divergence" || a == "-divergence" {
			// SW-232 AC-2: the executor-seam divergence record, read WITHOUT
			// starting a server. It is a mode of the existing diagnostic verb
			// rather than a new top-level subcommand on purpose — the verb set
			// is frozen by the AX-00 baseline and the coverage matrix, and this
			// story adds observability, not surface.
			return runDoctorDivergence(os.Stdout, jsonOut)
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
	indexRoot, indexMeta := "", ""
	if root, ok := state.DetectRepo(getwd()); ok {
		if p, perr := state.Resolve(root); perr == nil {
			indexRoot, indexMeta = root, p.Meta
		}
	}
	reg.Register(doctor.IndexCheck(indexRoot, indexMeta))
	reg.Register(doctor.PrivacyCheck())
	reg.Register(doctor.LocalFirstCheck())
	seamPositions, seamErr := executorSeamPositions()
	reg.Register(doctor.ExecutorSeamCheck(seamPositions, seamErr))
	seamDivergence, divergenceErr := executorDivergence()
	reg.Register(doctor.ExecutorDivergenceCheck(seamDivergence, divergenceErr))
	reg.Register(doctor.KnownDefectsCheck())

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

// executorSeamPositions reads the SW-228 (AX-08) kill-switch positions out of
// surfaces/client and translates them into the doctor package's vocabulary.
//
// The reading happens HERE rather than inside internal/doctor on purpose: the
// doctor package's contract is that a check computes only over what it is
// given, and it holds no surface imports at all. The composition root already
// imports both sides, so the coupling costs nothing and buys the check its
// testability.
//
// It applies the environment first, exactly as the composition root does for a
// real session. Without that the readout would report the compiled-in default
// no matter what the operator had set, which is the failure mode this whole
// check exists to prevent — a diagnostic that cannot be anything but green.
// Applying it here is safe because `graphi doctor` dispatches nothing: the
// positions it installs govern a seam this verb never reaches.
//
// A mistyped value is returned rather than swallowed. It would fail a real
// session at startup, so the honest thing for a diagnostic to do is say so.
//
// The positions it reports are THIS process's, derived from THIS environment,
// which is the honest scope: a server started from the same environment is in
// the same position. The divergence COUNTER is deliberately not reported — see
// ExecutorSeamCheck's doc comment for why a cross-process zero would be a false
// green.
func executorSeamPositions() ([]doctor.ExecutorSeamPosition, error) {
	if err := runtime.ApplyCanaryMode(); err != nil {
		return nil, err
	}
	positions := client.CanaryPositions()
	out := make([]doctor.ExecutorSeamPosition, 0, len(positions))
	for _, p := range positions {
		out = append(out, doctor.ExecutorSeamPosition{
			Operation:  p.Operation,
			Mode:       string(p.Mode),
			Overridden: p.Overridden,
			EnvVar:     runtime.EnvCanaryModeFor(p.Operation),
		})
	}
	return out, nil
}

// runDoctorDivergence renders the persisted executor-seam divergence record
// (SW-232 AC-2/AC-3) and returns the process exit code.
//
// # Why this reads a file and starts nothing
//
// The record is written by the long-running servers that dispatch through the
// seam (`graphi mcp`, `graphi serve`) and read here, in a different, short-lived
// process. That asymmetry is the entire reason the record had to become durable:
// before SW-232 the only evidence lived in the writing process's memory, so
// there was no reader at all. This path therefore opens the state directory and
// nothing else — no store, no daemon, no bind.
//
// # Why it exits 0 on a DIVERGED record
//
// It is a readout, not a gate. A divergence is reported in the document, and
// separately WARNed by the `executor-divergence` doctor check — which does not
// change `graphi doctor`'s exit code either, since only a FAIL does. Making the
// reader itself exit non-zero would put a second, quieter policy in a place
// people pipe into jq. Exit 1 is reserved for "the record could not be read",
// the one case where the OUTPUT cannot be trusted.
func runDoctorDivergence(w io.Writer, jsonOut bool) int {
	report, err := divergence.Read(state.StateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi doctor: read divergence record: %v\n", err)
		return 1
	}
	doc := divergence.Assess(report, client.MigratedOperations())
	if jsonOut {
		if err := divergence.RenderJSON(w, doc); err != nil {
			fmt.Fprintf(os.Stderr, "graphi doctor: render divergence json: %v\n", err)
			return 1
		}
		return 0
	}
	if err := divergence.RenderHuman(w, doc); err != nil {
		fmt.Fprintf(os.Stderr, "graphi doctor: render divergence: %v\n", err)
		return 1
	}
	return 0
}

// executorDivergence reads the persisted record for the doctor check, in the
// composition root, for the same reason executorSeamPositions does its reading
// here: internal/doctor computes only over what it is given.
//
// A read failure is RETURNED, not swallowed into an empty record. An empty
// record and an unreadable one are different facts and the check renders them
// differently — collapsing them would make an unreadable state directory look
// like a clean one, which is the false green this whole story exists to remove.
func executorDivergence() (doctor.ExecutorDivergence, error) {
	report, err := divergence.Read(state.StateDir())
	if err != nil {
		return doctor.ExecutorDivergence{}, err
	}
	doc := divergence.Assess(report, client.MigratedOperations())
	out := doctor.ExecutorDivergence{
		State:        string(doc.State),
		Directory:    doc.Directory,
		Observations: doc.Observations,
		Mismatches:   doc.Mismatches,
		Unreadable:   doc.Unreadable,
		Pruned:       doc.Pruned,
	}
	for _, op := range doc.Operations {
		switch op.State {
		case divergence.StateDiverged:
			out.Diverged = append(out.Diverged, op.Operation)
		case divergence.StateUnknown:
			out.Unobserved = append(out.Unobserved, op.Operation)
		}
	}
	return out, nil
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
	return doctorPlanAction(act), nil
}

// doctorPlanAction translates an mcpconfig.Action into the doctor vocabulary.
// The two are deliberately different words for the same three outcomes —
// mcpconfig names the write it *would perform* ("created"/"updated"/
// "unchanged"), doctor names the state it *observes* ("create"/"update"/
// "no-op") — so they must be mapped, never cast. Casting is what made every
// live client fall to the mcp check's "unknown plan action" branch.
//
// An action this function does not know is passed through verbatim so the
// check reports it as unverified rather than silently guessing a status: an
// unrecognized plan is not evidence of a healthy registration.
func doctorPlanAction(act mcpconfig.Action) doctor.MCPPlanAction {
	switch act {
	case mcpconfig.ActionUnchanged:
		return doctor.MCPPlanNoOp
	case mcpconfig.ActionCreated:
		return doctor.MCPPlanCreate
	case mcpconfig.ActionUpdated:
		return doctor.MCPPlanUpdate
	default:
		return doctor.MCPPlanAction(act)
	}
}

// Contending implements doctor.MCPContentionReader: it lists this client's
// zero-config graphi entries so the mcp check can warn when several of them
// would contend on one repository's ingest lock.
func (m mcpConfigReader) Contending(client doctor.MCPClient) ([]string, error) {
	c, ok := mcpconfig.ClientByID(client.ID)
	if !ok {
		return nil, fmt.Errorf("unknown client %q", client.ID)
	}
	return c.ContendingGraphiServers()
}

// releaseInfoAdapter adapts releaseinfo.Info to doctor.ReleaseInfo if needed.
// realEnv.Release already returns the value, so this is not used directly.
