// Package ollama is graphi's OPT-IN, loopback-only Ollama embedder.
//
// Layering: it is an engine leaf under engine/embed. It imports engine/embed for
// the Embedder contract and the standard library only (net/http, net) — no CGO,
// no third-party deps. It registers its selector scheme ("ollama") into the embed
// constructor table via init (embed.RegisterScheme) so the embed leaf never
// imports this package (no import cycle).
//
// Security contract (SW-059): the embedder talks to a LOOPBACK Ollama endpoint
// (default 127.0.0.1:11434) and is reached ONLY when explicitly opted in via
// config (e.g. GRAPHI_EMBEDDER=ollama or ollama:host:port). It is NEVER
// constructed on the default path. Construction FAILS CLOSED on any non-loopback
// host (a positive loopback allowlist), independent of and in addition to the
// runtime canary dial interceptor — defense-in-depth.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/engine/embed"
)

// Scheme is the selector scheme this package handles (e.g. "ollama:host:port").
const Scheme = "ollama"

// DefaultEndpoint is the loopback Ollama address used when the selector argument
// omits a host:port.
const DefaultEndpoint = "127.0.0.1:11434"

// defaultModel is the embedding model requested when none is supplied.
const defaultModel = "nomic-embed-text"

func init() {
	// Register the loopback Ollama scheme so an explicit GRAPHI_EMBEDDER=ollama
	// selector can construct it. Importing this package registers the CONSTRUCTOR
	// only; nothing is constructed or dialed until the selector names it.
	embed.RegisterScheme(Scheme, func(arg string) (embed.Embedder, error) {
		return New(arg, defaultModel)
	})
}

// Embedder is the loopback-only Ollama HTTP embedder.
type Embedder struct {
	endpoint string // "host:port", validated loopback
	model    string
	// dim is discovered from the first successful response and is written by
	// two paths (ProbeDim and Embed), so it is guarded: embed.Embedder
	// requires implementations to be safe for concurrent use.
	dimMu     sync.RWMutex
	dim       int
	dimCtx    int    // effective context length, surfaced by the admission profile
	dimDigest string // model digest, surfaced by the admission profile when available

	client *http.Client
}

// New constructs an Ollama embedder targeting endpoint (a "host:port", defaulting
// to DefaultEndpoint when empty). It FAILS CLOSED: any non-loopback host is
// rejected at construction with an error, so a misconfigured or hostile endpoint
// can never be dialed. model selects the embedding model (defaulted when empty).
func New(endpoint, model string) (*Embedder, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	if err := assertLoopbackEndpoint(endpoint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}
	return &Embedder{
		endpoint: endpoint,
		model:    model,
		// Dim is discovered from the first response; 0 until then. The mock and
		// tests do not require a fixed Dim() up front, and the index tolerates it.
		dim:    0,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// ID implements embed.Embedder.
func (e *Embedder) ID() string { return Scheme + ":" + e.model }

// Dim implements embed.Embedder. It is the dimensionality observed from the
// most recent successful request; 0 before the first one.
//
// The value is guarded because embed.Embedder requires implementations to be
// safe for concurrent use, and two writers exist: ProbeDim (the pre-fingerprint
// probe) and Embed (the ordinary path). An earlier revision documented this as
// "concurrency-safe because requests serialise through the http.Client
// transport", which was not true of the field itself — the transport serialises
// nothing about e.dim.
func (e *Embedder) Dim() int {
	e.dimMu.RLock()
	defer e.dimMu.RUnlock()
	return e.dim
}

// setDimOnce records the discovered dimension the first time a response
// reveals it. Later responses of the same size are a no-op; a response of a
// DIFFERENT size is ignored here rather than silently re-pointing the
// embedder mid-build — the fingerprint recorded at build start is what the
// generation is published under, so a mid-flight change must not rewrite it.
func (e *Embedder) setDimOnce(n int) {
	if n <= 0 {
		return
	}
	e.dimMu.Lock()
	defer e.dimMu.Unlock()
	if e.dim == 0 {
		e.dim = n
	}
}

// DimProbeText is the text the embedder sends to learn its dim before any
// real work. It is a single ASCII string so the request shape mirrors the
// production path exactly; Ollama's /api/embeddings returns the dim
// regardless of the input text, so the value is meaningless.
const DimProbeText = "graphi-dim-probe"

// ProbeDim forces the embedder to send ONE request to the loopback
// endpoint so the dim field is populated from the response. Ollama
// reports dim only after a successful call; without this probe, the
// fingerprint's dim field is 0 until the first real Embed call — and a
// fingerprint built with dim=0 cannot detect a real dim change
// (SW-261 review round 2 MAJOR 5). The probe uses the same /api/embed
// endpoint (the SW-267 AC-4 fail-closed path), the same model, and
// the same loopback validation as a real call. A probe failure
// surfaces the error verbatim so the build fails closed rather than
// silently fingerprinting with dim=0.
//
// The probe is safe to call concurrently with Embed: the discovered
// dimension is guarded by dimMu and recorded once (see setDimOnce).
func (e *Embedder) ProbeDim(ctx context.Context) error {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: DimProbeText, Truncate: false})
	if err != nil {
		return fmt.Errorf("ollama: probe marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+e.endpoint+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: probe build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: probe request to %s failed: %w", e.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama: probe endpoint returned status %d: %s", resp.StatusCode, string(body))
	}
	var decoded ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("ollama: probe decode response: %w", err)
	}
	if len(decoded.Embeddings) == 0 || len(decoded.Embeddings[0]) == 0 {
		return fmt.Errorf("ollama: probe returned empty embedding")
	}
	if e.Dim() == 0 {
		e.setDimOnce(len(decoded.Embeddings[0]))
	}
	return nil
}

// ollamaEmbedRequest models the Ollama /api/embed request shape
// (SW-267 reviewer fix Critical 3: the legacy /api/embeddings
// `prompt` field is wrong; the /api/embed endpoint expects `input`,
// which may be a single string or an array of strings).
//
// Truncate:false (SW-267 AC-4) is the FAIL-CLOSED admission call: the
// daemon rejects inputs that exceed its effective context instead of
// silently truncating them. The runtime surfaces the rejection as a
// typed error so the build never publishes a partial generation.
type ollamaEmbedRequest struct {
	Model    string `json:"model"`
	Input    string `json:"input"`
	Truncate bool   `json:"truncate"`
}

// ollamaEmbedResponse models the Ollama /api/embed response shape
// (SW-267 reviewer fix Critical 3: the legacy /api/embeddings singular
// `embedding` field is wrong; /api/embed returns `embeddings`, an
// array of vectors, one per input — for a single-string input the
// array has length 1).
type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed implements embed.Embedder. It POSTs each text to the loopback Ollama
// /api/embed endpoint with `truncate:false` (SW-267 AC-4) so the daemon is
// the final authority on input admission. The request body uses the
// real /api/embed wire shape (reviewer fix Critical 3: `input`, not
// `prompt`; the response is `embeddings` plural, an array of one
// vector per input). Any non-200 status, including the daemon's
// "input exceeds context length" response, surfaces as a TYPED
// *embed.AdmissionError (reviewer fix Critical 3: not a plain
// fmt.Errorf) naming the node and the limit so the calling build
// fails closed. Endpoint loopback was already enforced fail-closed
// at construction, so this method dials loopback only.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	url := "http://" + e.endpoint + "/api/embed"
	for _, t := range texts {
		body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: t, Truncate: false})
		if err != nil {
			return nil, fmt.Errorf("ollama: marshal request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("ollama: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ollama: request to %s failed: %w", e.endpoint, err)
		}
		// Check the HTTP status BEFORE decoding: a non-200 (e.g. 400/500
		// for an oversize input) must report the actual status and the
		// server's error message, not a misleading JSON decode error from
		// trying to parse the error page. SW-267 AC-4 fail-closed.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, &embed.AdmissionError{
				NodeID:  "", // caller fills in via BuildDocument / GenerateAndPersist
				Path:    "",
				Limit:   -1, // server-controlled, not the adapter's MaxTokens
				Actual:  len(t),
				Profile: e.Profile(),
				Reason:  fmt.Sprintf("ollama: server returned status %d: %s", resp.StatusCode, string(body)),
			}
		}
		var decoded ollamaEmbedResponse
		decErr := json.NewDecoder(resp.Body).Decode(&decoded)
		_ = resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("ollama: decode response: %w", decErr)
		}
		// Reviewer fix Critical 3: each request sends ONE input, so
		// the response always has its vector at embeddings[0],
		// regardless of the outer batch index. The previous
		// implementation read embeddings[i] and broke on the
		// second text. A test with a batch of two or more texts
		// catches it; a one-text batch cannot.
		if len(decoded.Embeddings) == 0 || len(decoded.Embeddings[0]) == 0 {
			return nil, &embed.AdmissionError{
				NodeID:  "",
				Path:    "",
				Limit:   -1,
				Actual:  len(t),
				Profile: e.Profile(),
				Reason:  "ollama: server returned no embedding for the input",
			}
		}
		if e.Dim() == 0 {
			e.setDimOnce(len(decoded.Embeddings[0]))
		}
		out = append(out, decoded.Embeddings[0])
	}
	return out, nil
}

// Compile-time interface assertions: ollama.Embedder satisfies the
// runtime's DimDiscoverer, Admission (fail-closed per-document), and
// AdmissionProfile (the SW-267 AC-3 / AC-8 contract) contracts.
var (
	_ embed.DimDiscoverer    = (*Embedder)(nil)
	_ embed.Admission        = (*Embedder)(nil)
	_ embed.AdmissionProfile = (*Embedder)(nil)
)

// Profile implements embed.AdmissionProfile.
func (e *Embedder) Profile() embed.AdmissionSpec {
	return embed.AdmissionSpec{
		TokenizerID:      "ollama-server-bound",
		TokenizerSHA256:  e.digest(),
		TokenizerVersion: "1",
		MaxTokens:        e.contextLen(),
		Reserve:          0,
		Algorithm:        "server-side-truncate-false",
		AlgorithmVersion: "1",
	}
}

// contextLen returns the model's effective context length from the
// most recent response, or 0 when the daemon has not been queried yet.
func (e *Embedder) contextLen() int {
	e.dimMu.RLock()
	defer e.dimMu.RUnlock()
	return e.dimCtx
}

// digest returns the model digest the embedder last saw, or "" when the
// daemon has not been queried yet. Future Ollama digest binding (when
// available) plugs in here.
func (e *Embedder) digest() string {
	e.dimMu.RLock()
	defer e.dimMu.RUnlock()
	return e.dimDigest
}

// Admit implements embed.Admission (SW-267 AC-2, AC-7). Ollama's
// /api/embed endpoint is the AUTHORITATIVE admission call when the
// embedder is configured with `truncate:false` (the SW-267 AC-4
// fail-closed posture): the daemon validates the input against its
// own tokenizer + effective context and returns a typed error if it
// does not fit. Admit here applies the byte-resource cap (AC-6) and
// returns the input unchanged; a server-side rejection surfaces as a
// real *embed.AdmissionError from the Embed call, so the build fails
// closed without ever admitting an oversized document.
func (e *Embedder) Admit(_ context.Context, text string) (embed.Admitted, error) {
	if len(text) > maxOllamaPayloadBytes {
		return embed.Admitted{}, &embed.AdmissionError{
			Limit:   maxOllamaPayloadBytes,
			Actual:  len(text),
			Profile: e.Profile(),
		}
	}
	return embed.Admitted{Text: text, TokenCount: 0, Bound: embed.BoundNone}, nil
}

// maxOllamaPayloadBytes caps the byte budget the Ollama adapter admits
// without consulting the server. Larger payloads are rejected here so
// a misconfigured daemon never sees them; the server-side
// authoritative check is the per-request Embed call (with
// truncate:false).
const maxOllamaPayloadBytes = embed.MaxCapsuleBytes

// assertLoopbackEndpoint is the fail-closed positive loopback allowlist. It
// accepts only a host that is "localhost", an IPv4 in 127.0.0.0/8, or IPv6 ::1;
// every other host (including a resolvable public name) is rejected so the
// embedder can never dial off-box. The port is not constrained.
func assertLoopbackEndpoint(endpoint string) error {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		// Allow a bare host with no port (default port applied by Ollama URL form
		// would be unusual, but reject rather than guess): treat the whole string
		// as the host for validation.
		host = endpoint
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ollama: refusing non-loopback host %q (loopback-only, fail-closed): a non-IP host requires DNS and is off-box", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("ollama: refusing non-loopback host %q (loopback-only, fail-closed)", host)
	}
	return nil
}
