// Command example-analyzer is the SW-231 (AX-11) example tier-C process
// extension: a read-only analyzer that reaches graphi's data ONLY through the
// declared host ports, over the `graphi-ext/1` stdio protocol.
//
// # It is part of a disposable spike
//
// Nothing ships this. The `graphi` binary does not import it and does not know
// it exists; it is built by engine/exthost's journey test and by whoever is
// reading the go/no-go decision. If SW-231 records a no-go, deleting this
// directory and engine/exthost removes the spike without trace (AC-6).
//
// # A subprocess is trusted local code, not a sandbox
//
// This program runs with the user's own OS rights. graphi bounds what it can
// reach OF GRAPHI'S DATA — the port list in example-analyzer.yaml is the whole
// grant — but it cannot bound what the process does with the machine. That is
// ADR 0013 D3, and an extension author is the person who most needs to know it:
// the trust a user extends by activating this is the trust they extend to a
// shell script.
//
// # The operation
//
// `example_symbol_census` takes {"symbol": "<query>"}, asks the host's
// graph.search port for matches, and returns a per-path census of them. It is
// deterministic by construction: the census is sorted by path, ties broken by
// name, and nothing reads the clock, the environment or the filesystem.
//
// # -spike-fault / GRAPHI_SPIKE_FAULT
//
// The fault switch is the test-fixture half of the spike: it makes this program
// misbehave on purpose so engine/exthost's crash, hang, oversize and
// fail-closed tests exercise a REAL process rather than a mock. It defaults to
// off, and a shipped extension would obviously not carry it. Each mode is named
// after the host behaviour it is there to prove.
//
// It is readable from the environment as well as the flag because the host
// spawns an extension with NO arguments — the descriptor pins a binary, not a
// command line — so the environment is the only channel a test has to reach it
// without staging a wrapper script. That is itself a finding for the decision
// document: this protocol has no way for a user to configure an extension.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/engine/exthost"
)

const (
	extensionID      = "example-analyzer"
	extensionVersion = "0.1.0"
	operation        = "example_symbol_census"
)

// Fault modes. Each exists to make one host guarantee testable against a real
// process instead of an in-memory fake.
const (
	faultNone = ""
	// faultCrash: exit non-zero mid-call, after the handshake. Proves the host
	// reports ErrCrashed with an exit status instead of hanging.
	faultCrash = "crash"
	// faultHang: accept the call and never answer. Proves the timeout kills.
	faultHang = "hang"
	// faultFlood: answer with a frame far above max_response_bytes. Proves the
	// declared length is refused before the body is read.
	faultFlood = "flood"
	// faultUndeclaredPort: ask for a port the descriptor does not list. Proves
	// the host refuses with registry.ErrMissingDependency and records it.
	faultUndeclaredPort = "undeclared-port"
	// faultConfirmed: claim confidence "confirmed". Proves ADR 0013 D5 is
	// enforced by rejection, not by downgrade.
	faultConfirmed = "confirmed"
	// faultBadProtocol: acknowledge a different protocol version.
	faultBadProtocol = "bad-protocol"
	// faultBadAPI: declare an api range this host is outside of.
	faultBadAPI = "bad-api"
	// faultBadIdentity: introduce itself under another name.
	faultBadIdentity = "bad-identity"
	// faultExtraOperation: advertise an operation the descriptor never declared.
	faultExtraOperation = "extra-operation"
	// faultSilentExit: exit 0 before the handshake completes.
	faultSilentExit = "silent-exit"
)

func main() {
	fault := flag.String("spike-fault", os.Getenv("GRAPHI_SPIKE_FAULT"),
		"SPIKE ONLY: misbehave on purpose so the host's containment can be tested against a real process")
	flag.Parse()

	if err := run(*fault); err != nil {
		fmt.Fprintln(os.Stderr, "example-analyzer:", err)
		os.Exit(1)
	}
}

func run(fault string) error {
	if fault == faultSilentExit {
		return nil
	}
	in := bufio.NewReaderSize(os.Stdin, 32<<10)

	// The host's hello arrives first. Its declared limits are what this program
	// is held to; reading them rather than assuming them is what lets an
	// extension fail legibly instead of being killed.
	hello, err := exthost.ReadFrame(in, exthost.MaxResponseCeiling)
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Type != exthost.MsgHello {
		return fmt.Errorf("expected %q, got %q", exthost.MsgHello, hello.Type)
	}
	limit := exthost.MaxResponseCeiling
	if hello.Limits != nil && hello.Limits.MaxResponseBytes > 0 {
		limit = hello.Limits.MaxResponseBytes
	}
	if err := writeAck(fault); err != nil {
		return err
	}

	for {
		f, err := exthost.ReadFrame(in, limit)
		if err != nil {
			// EOF is the host closing stdin — the normal end of a session.
			return nil
		}
		switch f.Type {
		case exthost.MsgShutdown:
			return nil
		case exthost.MsgCall:
			if err := serve(in, limit, f, fault); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected frame %q", f.Type)
		}
	}
}

func writeAck(fault string) error {
	protocol := exthost.ProtocolVersion
	if fault == faultBadProtocol {
		protocol = "graphi-ext/99"
	}
	api := exthost.APIRange{Min: "1.0", Max: "1.0"}
	if fault == faultBadAPI {
		api = exthost.APIRange{Min: "9.0", Max: "9.9"}
	}
	id, version := extensionID, extensionVersion
	if fault == faultBadIdentity {
		id, version = "someone-else", "9.9.9"
	}
	ops := []string{operation}
	if fault == faultExtraOperation {
		ops = append(ops, "example_undeclared_extra")
	}
	return exthost.WriteFrame(os.Stdout, exthost.Frame{
		Type:             exthost.MsgHelloAck,
		Protocol:         protocol,
		API:              &api,
		ExtensionID:      id,
		ExtensionVersion: version,
		Operations:       ops,
	})
}

// serve answers one call.
func serve(in *bufio.Reader, limit int64, call exthost.Frame, fault string) error {
	switch fault {
	case faultCrash:
		fmt.Fprintln(os.Stderr, "example-analyzer: deliberate crash (-spike-fault=crash)")
		os.Exit(3)
	case faultHang:
		// Sleep far past any descriptor timeout. The host is expected to kill
		// this process; if it does not, the test's own deadline catches it.
		time.Sleep(10 * time.Minute)
		return nil
	case faultFlood:
		return exthost.WriteFrame(os.Stdout, exthost.Frame{
			Type:       exthost.MsgResult,
			ID:         call.ID,
			Confidence: exthost.ConfidenceDerived,
			Findings:   json.RawMessage(`{"padding":"` + strings.Repeat("A", int(limit)+4096) + `"}`),
		})
	}

	port := "graph.search"
	if fault == faultUndeclaredPort {
		// A port that exists in opcatalog's vocabulary but is NOT in this
		// extension's descriptor: the interesting case, because a typo would be
		// caught by the port name being unknown while this one is a real grant
		// the user did not make.
		port = "source.read"
	}

	var args struct {
		Symbol string `json:"symbol"`
	}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return exthost.WriteFrame(os.Stdout, exthost.Frame{
				Type: exthost.MsgError, ID: call.ID,
				Message: fmt.Sprintf("arguments are not an object with a string `symbol`: %v", err),
			})
		}
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return exthost.WriteFrame(os.Stdout, exthost.Frame{
			Type: exthost.MsgError, ID: call.ID,
			Message: "argument `symbol` is required and must not be blank",
		})
	}

	query, err := json.Marshal(map[string]string{"symbol": args.Symbol})
	if err != nil {
		return err
	}
	if err := exthost.WriteFrame(os.Stdout, exthost.Frame{
		Type: exthost.MsgPortCall, ID: call.ID, Port: port, Payload: query,
	}); err != nil {
		return err
	}
	reply, err := exthost.ReadFrame(in, limit)
	if err != nil {
		return fmt.Errorf("read port result: %w", err)
	}
	if reply.Type != exthost.MsgPortResult {
		return fmt.Errorf("expected %q, got %q", exthost.MsgPortResult, reply.Type)
	}
	if !reply.OK {
		// The host refused or the port failed. Report it as this extension's own
		// error, legibly — the host's refusal message is the useful half.
		return exthost.WriteFrame(os.Stdout, exthost.Frame{
			Type: exthost.MsgError, ID: call.ID,
			Message: fmt.Sprintf("port %s: %s", port, reply.Message),
		})
	}

	findings, err := census(args.Symbol, reply.Payload)
	if err != nil {
		return exthost.WriteFrame(os.Stdout, exthost.Frame{
			Type: exthost.MsgError, ID: call.ID, Message: err.Error(),
		})
	}
	confidence := exthost.ConfidenceDerived
	if fault == faultConfirmed {
		confidence = "confirmed"
	}
	return exthost.WriteFrame(os.Stdout, exthost.Frame{
		Type: exthost.MsgResult, ID: call.ID, Confidence: confidence, Findings: findings,
	})
}

// searchReply is the shape this extension expects from the graph.search port.
type searchReply struct {
	Matches []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"matches"`
}

// census reduces search matches to a per-path count.
//
// Deterministic by construction: the output is sorted by path, and the sample
// name kept for each path is the lexicographically smallest, so map iteration
// order cannot reach the bytes. That is what engine/extpack/conformance's
// determinism check verifies, and it is verified across two independent PROCESS
// runs, not two calls into one.
func census(symbol string, payload json.RawMessage) (json.RawMessage, error) {
	var reply searchReply
	if err := json.Unmarshal(payload, &reply); err != nil {
		return nil, fmt.Errorf("graph.search returned a payload this analyzer cannot read: %w", err)
	}
	counts := map[string]int{}
	sample := map[string]string{}
	for _, m := range reply.Matches {
		counts[m.Path]++
		if cur, ok := sample[m.Path]; !ok || m.Name < cur {
			sample[m.Path] = m.Name
		}
	}
	paths := make([]string, 0, len(counts))
	for p := range counts {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	type row struct {
		Path    string `json:"path"`
		Count   int    `json:"count"`
		Example string `json:"example"`
	}
	out := struct {
		Symbol  string `json:"symbol"`
		Total   int    `json:"total"`
		ByPath  []row  `json:"by_path"`
		Analyze string `json:"analyzer"`
	}{Symbol: symbol, Total: len(reply.Matches), ByPath: make([]row, 0, len(paths)), Analyze: extensionID + "@" + extensionVersion}
	for _, p := range paths {
		out.ByPath = append(out.ByPath, row{Path: p, Count: counts[p], Example: sample[p]})
	}
	return json.Marshal(out)
}
