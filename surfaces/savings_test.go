// SW-020 savings readout tests: MCP<->CLI parity, structured fields, CLI
// headline, and the meter->price->cap->ledger compose path. Lives in the
// surfaces_test package alongside the query/search parity tests.
package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/cap"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/meter"
	"github.com/samibel/graphi/engine/price"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// newSavingsClient builds an in-process Direct client with a fresh ledger
// attached, returning the client and the ledger so the test can drive records.
func newSavingsClient(t *testing.T) (client.Client, *ledger.Ledger) {
	t.Helper()
	dir := t.TempDir()
	l, err := ledger.Open(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// query/search services are irrelevant to the savings readout; pass nil.
	c := client.NewDirect(nil, nil).WithLedger(l)
	return c, l
}

// readoutOverMCP calls the savings tool through the MCP stdio server and returns
// the canonical text payload (the structured readout JSON).
func readoutOverMCP(t *testing.T, c client.Client) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(c)
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "savings", "arguments": map[string]any{}},
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve savings: %v", err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp savings response %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp savings error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp savings content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// readoutOverCLI runs RunSavings and returns the LAST line (the canonical
// structured readout JSON), matching how the MCP tool result is shaped.
func readoutOverCLI(t *testing.T, c client.Client) (headline string, structured []byte, full []byte) {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := cli.RunSavings(context.Background(), c, &out, &errOut); err != nil {
		t.Fatalf("cli.RunSavings: %v (stderr: %s)", err, errOut.String())
	}
	full = out.Bytes()
	lines := strings.Split(strings.TrimRight(string(full), "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("cli savings output too short: %q", full)
	}
	headline = lines[0]
	structured = []byte(lines[len(lines)-1])
	return headline, structured, full
}

// AC: MCP readout includes per-call, per-session, cumulative USD as structured
// fields.
func TestSavings_MCPStructuredFields(t *testing.T) {
	c, l := newSavingsClient(t)
	defer l.Close()
	l.Record(ledger.Credit{CallID: "c1", Model: "gpt-4o", MicroUSD: 2_500_000, Priced: true})
	b := readoutOverMCP(t, c)
	var r ledger.Readout
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("readout not structured JSON: %v (%s)", err, b)
	}
	if r.LastCallMicroUSD != 2_500_000 || r.SessionMicroUSD != 2_500_000 || r.CumulativeMicroUSD != 2_500_000 {
		t.Errorf("structured fields wrong: %+v", r)
	}
}

// AC: MCP<->CLI parity — the structured readout is byte-identical over both
// surfaces for the same ledger state.
func TestSavings_MCPCLIParity(t *testing.T) {
	c, l := newSavingsClient(t)
	defer l.Close()
	l.Record(ledger.Credit{CallID: "c1", Model: "gpt-4o", MicroUSD: 1_000_000, Priced: true})
	l.Record(ledger.Credit{CallID: "c2", Model: "gpt-4o", MicroUSD: 1_500_000, Priced: true})
	mcpBytes := readoutOverMCP(t, c)
	_, cliStructured, _ := readoutOverCLI(t, c)
	if !bytes.Equal(mcpBytes, cliStructured) {
		t.Errorf("MCP<->CLI savings parity mismatch:\nMCP: %s\nCLI: %s", mcpBytes, cliStructured)
	}
}

// AC: CLI prints "Saved $X this session" alongside per-call and cumulative,
// matching the MCP figures.
func TestSavings_CLIHeadline(t *testing.T) {
	c, l := newSavingsClient(t)
	defer l.Close()
	l.Record(ledger.Credit{CallID: "c1", Model: "gpt-4o", MicroUSD: 2_500_000, Priced: true}) // $2.50
	headline, structured, _ := readoutOverCLI(t, c)
	if !strings.HasPrefix(headline, "Saved $2.50 this session") {
		t.Errorf("headline: want 'Saved $2.50 this session', got %q", headline)
	}
	// The structured readout must match the MCP figures for the same state.
	mcpBytes := readoutOverMCP(t, c)
	if !bytes.Equal(mcpBytes, structured) {
		t.Errorf("CLI figures do not match MCP: CLI=%s MCP=%s", structured, mcpBytes)
	}
}

// AC: transparent cap — the CLI surfaces a note when a cap was applied; the
// structured readout carries the cap flags (never raw-as-actual).
func TestSavings_CLITransparentCap(t *testing.T) {
	c, l := newSavingsClient(t)
	defer l.Close()
	// Record a capped contribution (raw would have been 5M, cap clamped to 1M).
	if _, err := l.RecordCapped(ledger.Credit{CallID: "c1", Model: "gpt-4o", MicroUSD: 1_000_000, Priced: true}, true); err != nil {
		t.Fatal(err)
	}
	_, structured, full := readoutOverCLI(t, c)
	var r ledger.Readout
	if err := json.Unmarshal(structured, &r); err != nil {
		t.Fatal(err)
	}
	if !r.LastCallCapped || !r.SessionCapped {
		t.Errorf("structured readout must report cap applied: %+v", r)
	}
	// The readout amount is the CAPPED 1M, not a raw 5M.
	if r.LastCallMicroUSD != 1_000_000 {
		t.Errorf("capped readout amount wrong: %d", r.LastCallMicroUSD)
	}
	if !strings.Contains(string(full), "anti-gaming cap applied") {
		t.Errorf("CLI must surface cap-applied note; got %q", full)
	}
}

// AC: the full compose path meter -> price -> cap -> ledger -> readout produces
// an honest, capped, parity-proven figure end to end. Local-only, deterministic.
func TestSavings_ComposePath(t *testing.T) {
	dir := t.TempDir()
	// Artifact the call "would otherwise have required" (whole-file read baseline).
	art := filepath.Join(dir, "big.go")
	// 6 tokens whole-file; graphi shipped 1 token (huge savings, but the cap
	// bounds it).
	if err := os.WriteFile(art, []byte("package big\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pt, err := price.Load()
	if err != nil {
		t.Fatal(err)
	}
	mtr := meter.New(meter.NewLocalFileReader())
	lg, err := ledger.Open(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	c := client.NewDirect(nil, nil).WithLedger(lg)
	antiGaming := cap.Cap{PerOpMicroUSD: 5_000} // $0.005 per-op ceiling (raw savings ~$0.01 exceeds it)

	// 1) meter the call: actual=1 token, baseline=whole-file of the artifact.
	rec, err := mtr.Record("call-1", "gpt-4o", 1, []string{art})
	if err != nil {
		t.Fatal(err)
	}
	// 2) price the token delta.
	usd := price.Savings(pt, rec.Model, rec.SavingsTokens)
	if !usd.Priced {
		t.Fatalf("expected priced savings, got %+v", usd)
	}
	// 3) apply the anti-gaming cap (transparently).
	clamped, capped := antiGaming.Apply(usd.MicroUSD, lg.SessionTotal())
	// 4) record durably with the cap flag.
	if _, err := lg.RecordCapped(ledger.Credit{
		CallID: rec.CallID, Model: usd.Model, MicroUSD: clamped, Priced: usd.Priced,
	}, capped); err != nil {
		t.Fatal(err)
	}
	// 5) readout over MCP and CLI — must agree.
	mcpBytes := readoutOverMCP(t, c)
	_, cliStructured, _ := readoutOverCLI(t, c)
	if !bytes.Equal(mcpBytes, cliStructured) {
		t.Fatalf("compose-path parity mismatch:\nMCP: %s\nCLI: %s", mcpBytes, cliStructured)
	}
	var r ledger.Readout
	if err := json.Unmarshal(mcpBytes, &r); err != nil {
		t.Fatal(err)
	}
	// The cap must have applied (raw savings ~$0.01 > $0.005) and the readout shows it.
	if !r.LastCallCapped {
		t.Errorf("compose path: expected cap to apply (raw > ceiling), got readout %+v", r)
	}
	if r.LastCallMicroUSD > 5_000 {
		t.Errorf("compose path: per-op cap violated: %d > 5_000", r.LastCallMicroUSD)
	}
}

// AC: deterministic + offline — engine/cap has no net import (static guard).
func TestCapPackageNoNetImport(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "engine", "cap", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trim := strings.TrimSpace(line)
			if strings.Contains(trim, "\"net\"") && !strings.HasPrefix(trim, "//") {
				t.Errorf("%s: forbidden net import: %s", f, trim)
			}
		}
	}
}
