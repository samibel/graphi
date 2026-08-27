package exthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/opcatalog"
)

// registryName is the name the SW-222 typed lifecycle errors carry from this
// package. The vocabulary is reused, never re-invented: "you reached for a port
// this extension did not declare" is registry.ErrMissingDependency, exactly as
// it is in engine/extpack/conformance's port gate.
const registryName = "exthost"

// PortHandler answers one host-port request on the extension's behalf.
//
// The payload in and out are raw JSON — the wire's shape, not a Go type — for
// engine/extpack/conformance's reason: the handles a port hands out are
// engine-service-shaped, and typing them here would mean this package knowing
// every service's Go type. What this package owns is the AUTHORISATION.
//
// A handler must respect ctx: it runs inside the call's wall-clock budget.
type PortHandler func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error)

// Config activates one extension. There is no zero-value that starts anything.
type Config struct {
	// Activated must be explicitly true. This is ADR 0013 I4 as a required
	// field rather than a default: a caller cannot reach a running extension by
	// forgetting to configure something.
	Activated bool
	// DescriptorPath names ONE descriptor file the caller chose. A directory is
	// refused; nothing here searches for extensions (N2).
	DescriptorPath string
	// Ports supplies exactly one handler per declared port. A missing handler is
	// registry.ErrMissingDependency; a SURPLUS handler is refused too, because a
	// handler for an undeclared port is a grant nobody wrote down.
	Ports map[opcatalog.Port]PortHandler
	// WorkingDir is the child's cwd. Defaults to the descriptor's directory.
	WorkingDir string
	// Env is the child's ENTIRE environment. nil means an EMPTY environment.
	//
	// The default is empty rather than inherited so a host's secrets are not
	// handed to the extension by accident. Read the honesty statement before
	// reading that as protection: an empty environment reduces ACCIDENTAL
	// leakage into a process that then leaks it onward; it does not stop an
	// extension from reading anything the user can read (ADR 0013 D3, T2).
	Env []string
}

// Extension is one running, verified, handshaken extension process.
type Extension struct {
	loaded Loaded
	ports  map[opcatalog.Port]PortHandler

	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	stderr *ringWriter

	frames  chan Frame
	readErr struct {
		mu  sync.Mutex
		err error
	}
	done chan struct{}

	waitOnce sync.Once
	waitErr  error

	// callMu serialises Call. The protocol correlates by id and could multiplex,
	// but the spike deliberately does not: a single-flight host is one where a
	// timeout kill cannot abort somebody else's in-flight request, and where two
	// callers' frames cannot interleave on one pipe. mu, separately, guards the
	// small mutable state below and is never held across a wait.
	callMu sync.Mutex

	mu         sync.Mutex
	nextID     uint64
	closed     bool
	operations []string
	violations []string
}

// startGrace bounds the handshake and the polite half of shutdown. It is
// deliberately independent of the descriptor's per-call timeout: an extension
// that cannot say hello is broken, and giving it a sixty-second budget to prove
// that would make every misconfiguration feel like a hang.
const startGrace = 5 * time.Second

// Start verifies, spawns and handshakes one extension.
//
// The order is the contract, and every step of it is pinned by an attack test:
//
//	activation → descriptor validation → ARTIFACT HASH VERIFICATION → spawn-safety
//	→ port wiring → spawn → handshake (protocol, api, identity, operations)
//
// No operation request is written before the handshake completes, and nothing is
// spawned whose bytes did not match the pinned hash.
func Start(ctx context.Context, cfg Config) (*Extension, error) {
	if !cfg.Activated {
		return nil, fmt.Errorf("%w: Config.Activated is false. A tier-C extension is default-off and "+
			"Labs-only (ADR 0013 I4); there is no discovery path and no implicit activation",
			ErrNotActivated)
	}
	if cfg.DescriptorPath == "" {
		return nil, fmt.Errorf("%w: Config.DescriptorPath is empty. Activation names ONE descriptor "+
			"file explicitly; this host never searches a directory (ADR 0013 N2)", ErrNotActivated)
	}
	loaded, err := LoadDescriptor(cfg.DescriptorPath)
	if err != nil {
		return nil, err
	}
	if err := refuseUnsafeBinary(loaded.BinaryPath); err != nil {
		return nil, err
	}
	ports, err := wirePorts(loaded.Descriptor, cfg.Ports)
	if err != nil {
		return nil, err
	}

	e := &Extension{
		loaded: loaded,
		ports:  ports,
		stderr: newRingWriter(maxStderrBytes),
		frames: make(chan Frame, 4),
		done:   make(chan struct{}),
	}
	if err := e.spawn(ctx, loaded, cfg); err != nil {
		return nil, err
	}
	if err := e.handshake(ctx); err != nil {
		e.kill()
		return nil, err
	}
	return e, nil
}

// maxStderrBytes bounds the child's captured stderr. A crashing extension's last
// words are a diagnostic; an unbounded one is a memory leak with a story.
const maxStderrBytes = 8 << 10

func (e *Extension) spawn(ctx context.Context, loaded Loaded, cfg Config) error {
	// os.Pipe rather than cmd.StdoutPipe: with an *os.File the exec package does
	// not spin a copying goroutine, so Wait never races the reader for the pipe
	// — the shape that makes "the process crashed" a clean EOF instead of a
	// "file already closed" surprise.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("exthost: stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return fmt.Errorf("exthost: stdout pipe: %w", err)
	}

	dir := cfg.WorkingDir
	if dir == "" {
		dir = loaded.Dir
	}
	env := cfg.Env
	if env == nil {
		env = []string{}
	}

	// exec.Command, NOT exec.CommandContext: the ctx here bounds STARTUP, while
	// killing on ctx expiry is the per-call timeout's job (see Call). Handing the
	// process to CommandContext would tie the extension's life to whichever
	// context happened to start it.
	cmd := exec.Command(loaded.BinaryPath)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = e.stderr
	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return fmt.Errorf("%w: %v", ErrCrashed, err)
	}
	// The parent's copies of the child's ends must go, or stdout never reaches
	// EOF when the child dies and a crash would read as a hang.
	stdinR.Close()
	stdoutW.Close()

	e.cmd = cmd
	e.stdin = stdinW
	e.stdout = stdoutR
	go e.readLoop(loaded.Descriptor.Limits.MaxResponseBytes)
	return nil
}

// readLoop is the single reader of the child's stdout.
func (e *Extension) readLoop(limit int64) {
	defer close(e.frames)
	br := bufio.NewReaderSize(e.stdout, 32<<10)
	for {
		f, err := ReadFrame(br, limit)
		if err != nil {
			e.setReadErr(err)
			return
		}
		select {
		case e.frames <- f:
		case <-e.done:
			return
		}
	}
}

func (e *Extension) setReadErr(err error) {
	e.readErr.mu.Lock()
	defer e.readErr.mu.Unlock()
	if e.readErr.err == nil {
		e.readErr.err = err
	}
}

func (e *Extension) takeReadErr() error {
	e.readErr.mu.Lock()
	defer e.readErr.mu.Unlock()
	return e.readErr.err
}

// handshake exchanges hello/hello_ack and refuses every disagreement.
func (e *Extension) handshake(ctx context.Context) error {
	d := e.loaded.Descriptor
	ports := make([]string, len(d.Ports))
	for i, p := range d.Ports {
		ports[i] = string(p)
	}
	limits := d.Limits
	hello := Frame{
		Type:             MsgHello,
		Protocol:         ProtocolVersion,
		HostAPI:          HostAPIVersion,
		ExtensionID:      d.ID,
		ExtensionVersion: d.Version,
		ArtifactSHA256:   d.Artifact.SHA256,
		Ports:            ports,
		Limits:           &limits,
	}
	if err := WriteFrame(e.stdin, hello); err != nil {
		return fmt.Errorf("%w: could not send the handshake: %v", ErrCrashed, err)
	}

	hsCtx, cancel := context.WithTimeout(ctx, startGrace)
	defer cancel()
	f, err := e.awaitFrame(hsCtx)
	if err != nil {
		return err
	}
	if f.Type != MsgHelloAck {
		return fmt.Errorf("%w: the first frame was %q, the handshake requires %q",
			ErrProtocolViolation, extpack.Bound(string(f.Type)), MsgHelloAck)
	}
	if f.Protocol != ProtocolVersion {
		return fmt.Errorf("%w: the extension acknowledged protocol %q, this host speaks %q",
			ErrProtocolMismatch, extpack.Bound(f.Protocol), ProtocolVersion)
	}
	if f.API == nil {
		return fmt.Errorf("%w: the handshake declares no api range; an unstated target host API "+
			"cannot be found incompatible, which is the same as not checking", ErrProtocolViolation)
	}
	// Reuse extpack.APIRange.Validate: it is the SAME check a tier-A pack gets,
	// against the SAME host API constant, so a pack author and an extension
	// author cannot be told two different compatibility stories.
	if err := (extpack.APIRange{Min: f.API.Min, Max: f.API.Max}).Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrAPIVersionUnsupported, err)
	}
	if f.ExtensionID != d.ID || f.ExtensionVersion != d.Version {
		return fmt.Errorf("%w: descriptor pins %s@%s, the process introduced itself as %s@%s. "+
			"The bytes were verified, so the descriptor names a different extension than the one it pins",
			ErrIdentityMismatch, d.ID, d.Version,
			extpack.Bound(f.ExtensionID), extpack.Bound(f.ExtensionVersion))
	}
	if err := checkOperations(d.Capabilities.Provides, f.Operations); err != nil {
		return err
	}
	e.mu.Lock()
	e.operations = append([]string(nil), f.Operations...)
	e.mu.Unlock()
	return nil
}

// checkOperations requires the advertised set to be EXACTLY the declared set.
//
// Not a subset: an extension offering an operation its descriptor does not
// declare is offering capability the user never granted, and an extension
// missing one it declared is a descriptor that lies about what installing it
// gets you. Both are refused, and the message says which.
func checkOperations(declared, advertised []string) error {
	want := append([]string(nil), declared...)
	got := append([]string(nil), advertised...)
	sort.Strings(want)
	sort.Strings(got)
	if len(want) == len(got) {
		same := true
		for i := range want {
			if want[i] != got[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	bounded := make([]string, len(got))
	for i, op := range got {
		bounded[i] = extpack.Bound(op)
	}
	return fmt.Errorf("%w: the descriptor declares capabilities.provides [%s], the process advertises "+
		"[%s]; the two must match exactly — an extra operation is capability the user never granted, "+
		"a missing one is a descriptor that lies",
		ErrProtocolViolation, strings.Join(want, ", "), strings.Join(bounded, ", "))
}

// Operations returns the operations this extension advertises.
func (e *Extension) Operations() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.operations...)
}

// PortViolations returns every undeclared port the extension reached for, in the
// order it reached for them.
//
// The record is kept whether or not the call that triggered it was later
// reported as a failure, for engine/extpack/conformance's reason: a violation
// has to be visible on the basis of what the extension DID, not on whether the
// host's error survived somebody's error handling.
func (e *Extension) PortViolations() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.violations...)
}

// Descriptor returns the loaded, verified descriptor.
//
// The slices are copied: a Descriptor handed out with its Ports aliased would let
// a caller edit the grant this host is enforcing, which is the one field on it
// that must not be editable from outside.
func (e *Extension) Descriptor() Descriptor {
	d := e.loaded.Descriptor
	d.Ports = append([]opcatalog.Port(nil), d.Ports...)
	d.Permissions = append([]opcatalog.Permission(nil), d.Permissions...)
	d.Capabilities.Provides = append([]string(nil), d.Capabilities.Provides...)
	return d
}

// Call runs one operation and returns its result with full provenance.
//
// Calls are serialised: this protocol correlates by id but the spike does not
// need concurrency, and a single-flight host is one where a timeout kill cannot
// abort somebody else's in-flight request.
//
// The wall-clock budget is min(caller's ctx, descriptor timeout). On expiry the
// PROCESS IS KILLED and ErrTimeout is returned — the host does not wait politely
// for an extension that has already failed its contract, because "we asked it to
// stop" is not a limit.
func (e *Extension) Call(ctx context.Context, operation string, args json.RawMessage) (Result, error) {
	e.callMu.Lock()
	defer e.callMu.Unlock()

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return Result{}, ErrClosed
	}
	known := false
	for _, op := range e.operations {
		if op == operation {
			known = true
			break
		}
	}
	if !known {
		advertised := strings.Join(e.operations, ", ")
		e.mu.Unlock()
		return Result{}, fmt.Errorf("%w: operation %q is not advertised by %s; it offers [%s]",
			registry.ErrMissingDependency, extpack.Bound(operation), e.loaded.Descriptor.ID, advertised)
	}
	e.nextID++
	id := e.nextID
	e.mu.Unlock()

	d := e.loaded.Descriptor
	budget := time.Duration(d.Limits.TimeoutMS) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	if err := WriteFrame(e.stdin, Frame{
		Type:      MsgCall,
		ID:        id,
		Operation: operation,
		Arguments: args,
	}); err != nil {
		return Result{}, e.diagnoseWriteFailure(err)
	}

	for {
		f, err := e.awaitFrame(callCtx)
		if err != nil {
			if errors.Is(err, ErrTimeout) {
				e.kill()
				// Cancellation and expiry both kill, and both are ErrTimeout —
				// but they are different events for the person reading the log,
				// so the message distinguishes them. `ctx` is the CALLER's
				// context; callCtx below it carries the descriptor's deadline.
				if ctx.Err() != nil {
					return Result{}, fmt.Errorf("%w: the caller cancelled %q on %s; the process was "+
						"killed and this host is unaffected%s",
						ErrTimeout, extpack.Bound(operation), d.ID, e.stderrTail())
				}
				return Result{}, fmt.Errorf("%w: %s did not answer %q within %d ms; the process was "+
					"killed and this host is unaffected%s",
					ErrTimeout, d.ID, extpack.Bound(operation), d.Limits.TimeoutMS, e.stderrTail())
			}
			// The stream ended or desynchronised (an oversize frame is refused
			// with its body still unread, so the next byte is not a header).
			// There is nothing to resynchronise TO: kill, and let the caller
			// start a fresh process if it still wants an answer.
			e.kill()
			return Result{}, err
		}
		switch f.Type {
		case MsgPortCall:
			if err := e.servePortCall(callCtx, f); err != nil {
				return Result{}, err
			}
		case MsgResult:
			if f.ID != id {
				return Result{}, fmt.Errorf("%w: result carries id %d, the request was id %d",
					ErrProtocolViolation, f.ID, id)
			}
			confidence, err := checkConfidence(f.Confidence)
			if err != nil {
				return Result{}, err
			}
			return Result{
				Provenance: newProvenance(e.loaded, operation, confidence),
				Findings:   f.Findings,
			}, nil
		case MsgError:
			return Result{}, fmt.Errorf("%w: %s: %s", ErrExtension, d.ID, extpack.Bound(f.Message))
		default:
			return Result{}, fmt.Errorf("%w: %q is not a frame an extension may send during a call",
				ErrProtocolViolation, extpack.Bound(string(f.Type)))
		}
	}
}

// servePortCall answers one port request, or refuses it.
//
// A refusal is written to the extension AND fails the call. Both halves matter:
// the frame is what makes the extension's own error message legible, and the
// returned error is what keeps a host that refused a grant from returning an
// answer computed without it. Fail closed — "refused the port, kept the result"
// would be exactly the quiet degradation graphi's standards forbid.
func (e *Extension) servePortCall(ctx context.Context, f Frame) error {
	port := opcatalog.Port(f.Port)
	handler, declared := e.ports[port]
	if !declared {
		e.recordViolation(f.Port)
		refusal := registry.Errorf(registry.ErrMissingDependency, registryName, "Use", f.Port,
			"%s: port %q was not declared by %s; an extension reaches data only through the ports "+
				"its descriptor lists, and that list is what the user granted",
			registryName, extpack.Bound(f.Port), e.loaded.Descriptor.ID)
		_ = WriteFrame(e.stdin, Frame{Type: MsgPortResult, ID: f.ID, OK: false, Message: refusal.Error()})
		return refusal
	}
	payload, err := handler(ctx, f.Payload)
	if err != nil {
		// A port that failed is the extension's problem to handle, not the
		// host's to abort on: the request was legitimate and the answer is "no".
		return WriteFrame(e.stdin, Frame{
			Type: MsgPortResult, ID: f.ID, OK: false,
			Message: fmt.Sprintf("port %s failed: %v", port, err),
		})
	}
	if limit := e.loaded.Descriptor.Limits.MaxResponseBytes; int64(len(payload)) > limit {
		return WriteFrame(e.stdin, Frame{
			Type: MsgPortResult, ID: f.ID, OK: false,
			Message: fmt.Sprintf("port %s produced %d bytes, above this extension's %d-byte limit",
				port, len(payload), limit),
		})
	}
	return WriteFrame(e.stdin, Frame{Type: MsgPortResult, ID: f.ID, OK: true, Payload: payload})
}

func (e *Extension) recordViolation(port string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.violations = append(e.violations, extpack.Bound(port))
}

// awaitFrame reads the next frame, or explains why there will not be one.
func (e *Extension) awaitFrame(ctx context.Context) (Frame, error) {
	select {
	case <-ctx.Done():
		return Frame{}, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
	case f, ok := <-e.frames:
		if ok {
			return f, nil
		}
		return Frame{}, e.diagnoseStreamEnd()
	}
}

// diagnoseStreamEnd turns "the pipe closed" into something a user who did not
// write this extension can act on: the reader's own error if it had one, and
// otherwise the exit status plus the process's last words.
func (e *Extension) diagnoseStreamEnd() error {
	if err := e.takeReadErr(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w%s", err, e.stderrTail())
	}
	status := e.reap(startGrace)
	return fmt.Errorf("%w: %s ended without answering (%s)%s",
		ErrCrashed, e.loaded.Descriptor.ID, status, e.stderrTail())
}

// diagnoseWriteFailure explains a failed write to the child's stdin.
//
// It is almost always a dead child (EPIPE), so the report names the extension
// and quotes its last words rather than surfacing a raw pipe error. It does NOT
// drain the frame channel: a frame already in flight is somebody else's evidence,
// and discarding it to make this message tidier would destroy it.
func (e *Extension) diagnoseWriteFailure(err error) error {
	return fmt.Errorf("%w: could not send the request to %s: %v%s",
		ErrCrashed, e.loaded.Descriptor.ID, err, e.stderrTail())
}

func (e *Extension) stderrTail() string {
	tail := strings.TrimSpace(e.stderr.String())
	if tail == "" {
		return "; the process wrote nothing to stderr"
	}
	return "; last stderr: " + tail
}

// reap waits for the process, bounded, and describes how it ended.
func (e *Extension) reap(limit time.Duration) string {
	done := make(chan struct{})
	go func() {
		e.waitOnce.Do(func() { e.waitErr = e.cmd.Wait() })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(limit):
		return "still running after closing its stdout"
	}
	if e.waitErr == nil {
		return "exited cleanly"
	}
	var exit *exec.ExitError
	if errors.As(e.waitErr, &exit) {
		return "exited " + exit.String()
	}
	return e.waitErr.Error()
}

// kill terminates the process and releases the reader.
func (e *Extension) kill() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.mu.Unlock()

	close(e.done)
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	e.waitOnce.Do(func() { e.waitErr = e.cmd.Wait() })
	if e.stdout != nil {
		_ = e.stdout.Close()
	}
}

// Close shuts the extension down: shutdown frame, closed stdin, then a kill.
//
// The polite half is best-effort and bounded by startGrace. An extension that
// ignores it is killed, and Close still returns nil — "the process would not
// leave" is not a caller error, and the whole point of the process boundary is
// that the host does not need the extension's cooperation to be healthy again.
func (e *Extension) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	_ = WriteFrame(e.stdin, Frame{Type: MsgShutdown})
	_ = e.stdin.Close()

	exited := make(chan struct{})
	go func() {
		for range e.frames { //nolint:revive // drain until EOF
		}
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(startGrace):
	}
	e.kill()
	return nil
}

// wirePorts pairs each declared port with its handler, and refuses both
// directions of mismatch.
func wirePorts(d Descriptor, supplied map[opcatalog.Port]PortHandler) (map[opcatalog.Port]PortHandler, error) {
	out := make(map[opcatalog.Port]PortHandler, len(d.Ports))
	for _, p := range d.Ports {
		h, ok := supplied[p]
		if !ok || h == nil {
			return nil, registry.Errorf(registry.ErrMissingDependency, registryName, "Start", string(p),
				"%s: %s declares port %q and the host supplied no handler for it; a declared port with "+
					"no handle is a HOST setup gap, not an extension fault",
				registryName, d.ID, p)
		}
		out[p] = h
	}
	for p := range supplied {
		if _, declared := out[p]; !declared {
			return nil, registry.Errorf(registry.ErrUnsupportedOverride, registryName, "Start", string(p),
				"%s: the host supplied a handler for port %q, which %s does not declare; a grant "+
					"nobody wrote down is refused rather than quietly held",
				registryName, p, d.ID)
		}
	}
	return out, nil
}

// refuseUnsafeBinary is the spawn-safety gate.
//
// # Why the *.test refusal is here and is fatal
//
// On 2026-08-27 surfaces/daemon's auto-start defaulted its binary to os.Args[0].
// Under `go test`, os.Args[0] IS the test binary, so every dial of an absent
// socket spawned the suite, which dialled again — 374 live processes, and four
// kernel panics on the development host (fixed by commit 4328a5c). Any package
// that spawns processes inherits that lesson. This one refuses two shapes of it
// before exec: a path ending in .test, and the running executable itself. A
// shipped extension is neither, so nothing legitimate is refused.
func refuseUnsafeBinary(path string) error {
	if path == "" {
		return fmt.Errorf("%w: no path", ErrUnsafeBinary)
	}
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".test") {
		return fmt.Errorf("%w: %q is a Go test binary. A test binary's entry point is a test suite, "+
			"so executing one as an extension re-runs the suite — the recursive process explosion "+
			"that kernel-panicked a host on 2026-08-27 (commit 4328a5c)", ErrUnsafeBinary, base)
	}
	if self, err := os.Executable(); err == nil && sameFile(self, path) {
		return fmt.Errorf("%w: %q is this very process's executable; an extension is a DIFFERENT "+
			"program, and spawning ourselves is how a fork bomb starts", ErrUnsafeBinary, base)
	}
	if len(os.Args) > 0 && os.Args[0] != "" && sameFile(os.Args[0], path) {
		return fmt.Errorf("%w: %q is os.Args[0]; an extension binary is always named explicitly by a "+
			"descriptor, never defaulted to the running command", ErrUnsafeBinary, base)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeBinary, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeBinary, base)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: %q is not executable (mode %v)", ErrUnsafeBinary, base, info.Mode().Perm())
	}
	return nil
}

// sameFile compares two paths by identity where the OS can, and by cleaned
// absolute path otherwise. os.SameFile is the honest comparison — a symlink or a
// second hard link to the test binary is still the test binary.
func sameFile(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil && os.SameFile(ai, bi) {
		return true
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

// ringWriter keeps the LAST n bytes written to it. The child's stderr is
// extension-controlled text that lands in a host error message, so it is bounded
// at the sink rather than trusted at the source.
type ringWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRingWriter(max int) *ringWriter { return &ringWriter{max: max} }

func (w *ringWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

func (w *ringWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
