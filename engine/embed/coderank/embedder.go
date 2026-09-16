package coderank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samibel/graphi/engine/embed"
)

// Embedder is an explicitly constructed, loopback-only development adapter.
// Its manifest and first serving epoch are immutable for its lifetime.
type Embedder struct {
	manifest Manifest
	client   *http.Client
	expected embed.RuntimeAttestation
}

var (
	_ embed.Embedder            = (*Embedder)(nil)
	_ embed.QueryEmbedder       = (*Embedder)(nil)
	_ embed.Admission           = (*Embedder)(nil)
	_ embed.AdmissionProfile    = (*Embedder)(nil)
	_ embed.DimDiscoverer       = (*Embedder)(nil)
	_ embed.AvailabilityChecker = (*Embedder)(nil)
	_ embed.RuntimeAttestor     = (*Embedder)(nil)
)

// NewFromManifest validates every pin before dialing, then pins the initial
// sidecar attestation. It never registers or selects an embedder globally.
func NewFromManifest(ctx context.Context, path string) (*Embedder, error) {
	m, err := LoadManifest(path)
	if err != nil {
		return nil, err
	}
	return newFromManifest(ctx, m, nil)
}

func newFromManifest(ctx context.Context, m Manifest, client *http.Client) (*Embedder, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		transport, err := loopbackTransport(m.Endpoint)
		if err != nil {
			return nil, err
		}
		client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	// Never inherit a client's redirect policy, including when tests inject a
	// transport. A redirect response itself is rejected by request below.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	e := &Embedder{manifest: m, client: &c}
	got, err := e.fetchAttestation(ctx)
	if err != nil {
		return nil, err
	}
	e.expected = got
	return e, nil
}

func loopbackTransport(endpoint string) (*http.Transport, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	address := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	return &http.Transport{
		// No proxy or resolver can substitute a different destination. The
		// hostname was either a literal loopback or mapped above, before dialing.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		},
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}, nil
}

func (e *Embedder) request(ctx context.Context, path string, input, output any) error {
	method := http.MethodGet
	var body bytes.Buffer
	if input != nil {
		method = http.MethodPost
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return fmt.Errorf("coderank: encode request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, e.manifest.Endpoint+path, &body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("coderank: request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("coderank: %s returned HTTP %d", path, resp.StatusCode)
	}
	if err := decodeResponse(resp.Body, output); err != nil {
		return fmt.Errorf("coderank: decode %s response: %w", path, err)
	}
	return nil
}

func (e *Embedder) fetchAttestation(ctx context.Context) (embed.RuntimeAttestation, error) {
	var out attestationResponse
	if err := e.request(ctx, "/v1/attestation", nil, &out); err != nil {
		return embed.RuntimeAttestation{}, err
	}
	got := embed.RuntimeAttestation{IdentityDigest: out.IdentityDigest, Epoch: out.Epoch}
	want := e.expected
	if want.IdentityDigest == "" {
		want.IdentityDigest = e.manifest.IdentityDigest()
	}
	if err := embed.ValidateRuntimeAttestation(got); err != nil {
		return embed.RuntimeAttestation{}, &embed.RuntimeAttestationError{Phase: "attestation", Expected: want, Observed: got, Reason: err.Error()}
	}
	if out.Protocol != ProtocolVersion || got.IdentityDigest != want.IdentityDigest || (want.Epoch != "" && got.Epoch != want.Epoch) || out.Dimension != e.manifest.Dimension {
		return embed.RuntimeAttestation{}, &embed.RuntimeAttestationError{Phase: "attestation", Expected: want, Observed: got, Reason: "manifest or runtime binding mismatch"}
	}
	return got, nil
}

func (e *Embedder) verifyBinding(phase string, b responseBinding) error {
	got := embed.RuntimeAttestation{IdentityDigest: b.IdentityDigest, Epoch: b.Epoch}
	if b.Protocol != ProtocolVersion || got != e.expected {
		return &embed.RuntimeAttestationError{Phase: phase, Expected: e.expected, Observed: got, Reason: "response binding mismatch"}
	}
	return nil
}

func unchangedUTF8Prefix(original, admitted string) bool {
	return utf8.ValidString(admitted) && strings.HasPrefix(original, admitted)
}

// Admit accepts only the authoritative tokenizer's unchanged UTF-8 prefix.
func (e *Embedder) Admit(ctx context.Context, text string) (embed.Admitted, error) {
	if !utf8.ValidString(text) {
		return embed.Admitted{}, e.admissionError(0, "input is not valid UTF-8")
	}
	var out admitResponse
	if err := e.request(ctx, "/v1/admit", admitRequest{Protocol: ProtocolVersion, Text: text}, &out); err != nil {
		return embed.Admitted{}, err
	}
	if err := e.verifyBinding("admission", out.responseBinding); err != nil {
		return embed.Admitted{}, err
	}
	if out.TokenCount == nil {
		return embed.Admitted{}, e.admissionError(0, "response requires an integer token_count")
	}
	if !unchangedUTF8Prefix(text, out.Text) || *out.TokenCount < 0 || *out.TokenCount > e.manifest.Admission.MaxTokens {
		return embed.Admitted{}, e.admissionError(*out.TokenCount, "response is not an unchanged prefix within the token limit")
	}
	bound := "none"
	if out.Text != text {
		bound = "tokens"
	}
	return embed.Admitted{Text: out.Text, TokenCount: *out.TokenCount, Bound: bound}, nil
}

func (e *Embedder) admissionError(count int, reason string) error {
	return &embed.AdmissionError{Limit: e.manifest.Admission.MaxTokens, Actual: count, Profile: e.Profile(), Reason: reason}
}

// Embed sends document bytes unchanged. An empty batch follows the shared
// Embedder contract and does not contact the sidecar.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	return e.embed(ctx, "document", texts)
}

// EmbedQuery applies the pinned query instruction exactly once per call.
func (e *Embedder) EmbedQuery(ctx context.Context, text string) ([][]float32, error) {
	return e.embed(ctx, "query", []string{e.manifest.Query.Instruction + text})
}

func (e *Embedder) embed(ctx context.Context, kind string, texts []string) ([][]float32, error) {
	for _, text := range texts {
		if !utf8.ValidString(text) {
			return nil, errors.New("coderank: input is not valid UTF-8")
		}
	}
	var out embedResponse
	if err := e.request(ctx, "/v1/embed", embedRequest{Protocol: ProtocolVersion, Kind: kind, Texts: texts}, &out); err != nil {
		return nil, err
	}
	if err := e.verifyBinding("embedding", out.responseBinding); err != nil {
		return nil, err
	}
	if len(out.Vectors) != len(texts) {
		return nil, errors.New("coderank: response vector cardinality mismatch")
	}
	vectors := make([][]float32, len(out.Vectors))
	for i, vector := range out.Vectors {
		if len(vector) != e.Dim() {
			return nil, errors.New("coderank: response vector dimension mismatch")
		}
		vectors[i] = make([]float32, len(vector))
		for j, value := range vector {
			if value == nil {
				return nil, errors.New("coderank: null vector component")
			}
			if math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) {
				return nil, errors.New("coderank: non-finite vector value")
			}
			vectors[i][j] = *value
		}
	}
	return vectors, nil
}

func (e *Embedder) ExpectedRuntimeAttestation() embed.RuntimeAttestation { return e.expected }
func (e *Embedder) RuntimeAttestation(ctx context.Context) (embed.RuntimeAttestation, error) {
	return e.fetchAttestation(ctx)
}
func (e *Embedder) ProbeDim(ctx context.Context) error { _, err := e.fetchAttestation(ctx); return err }

// CheckAvailable is local and read-only as required by AvailabilityChecker.
// Query and generation boundaries use VerifyRuntime for network freshness.
func (e *Embedder) CheckAvailable(context.Context) error {
	if err := e.manifest.Validate(); err != nil {
		return err
	}
	if err := embed.ValidateRuntimeAttestation(e.expected); err != nil {
		return err
	}
	if e.expected.IdentityDigest != e.manifest.IdentityDigest() {
		return errors.New("coderank: pinned manifest identity mismatch")
	}
	return nil
}

func (e *Embedder) ID() string {
	return "coderank:" + e.manifest.Model.ID + "@" + e.manifest.Model.Revision + ":" + e.expected.IdentityDigest
}
func (e *Embedder) Dim() int                     { return e.manifest.Dimension }
func (e *Embedder) Profile() embed.AdmissionSpec { return e.manifest.AdmissionSpec() }
func (e *Embedder) Revision() string             { return e.manifest.Model.Revision }
func (e *Embedder) ModelSHA256() string          { return e.manifest.Model.SHA256 }
func (e *Embedder) TokenizerSHA256() string      { return e.manifest.Tokenizer.SHA256 }
func (e *Embedder) ChunkerConfig() string {
	m := e.manifest
	m.Endpoint = "" // Relocation and serving epoch must not change durable identity.
	data, _ := json.Marshal(m)
	return string(data)
}
