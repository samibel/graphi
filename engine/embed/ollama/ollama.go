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
	"net"
	"net/http"
	"strings"
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
	dim      int
	client   *http.Client
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

// Dim implements embed.Embedder. It is the dimensionality observed from the most
// recent successful Embed; 0 before the first call.
func (e *Embedder) Dim() int { return e.dim }

// ollamaEmbedRequest / ollamaEmbedResponse model the Ollama /api/embeddings shape.
type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed implements embed.Embedder. It POSTs each text to the loopback Ollama
// /api/embeddings endpoint and returns the vectors in input order. On any
// transport or decode failure it returns a clean typed error and NEVER falls back
// to another host. Endpoint loopback was already enforced fail-closed at
// construction, so this method dials loopback only.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	url := "http://" + e.endpoint + "/api/embeddings"
	for _, t := range texts {
		body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Prompt: t})
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
		// Check the HTTP status BEFORE decoding: a non-200 (e.g. 404/500 with an
		// HTML body) must report the actual status, not a misleading JSON decode
		// error from trying to parse the error page.
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("ollama: endpoint returned status %d", resp.StatusCode)
		}
		var decoded ollamaEmbedResponse
		decErr := json.NewDecoder(resp.Body).Decode(&decoded)
		_ = resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("ollama: decode response: %w", decErr)
		}
		if len(decoded.Embedding) == 0 {
			return nil, fmt.Errorf("ollama: empty embedding for input")
		}
		if e.dim == 0 {
			e.dim = len(decoded.Embedding)
		}
		out = append(out, decoded.Embedding)
	}
	return out, nil
}

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
