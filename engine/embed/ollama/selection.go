package ollama

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Explicit selection is additive: legacy ollama[:host:port] is unchanged.
// Model selectors require a full digest, runtime version and context setting,
// so construction/reload stays zero-I/O and identity is stable before probing.
// Example: ollama:127.0.0.1:11434?model=qwen3-embedding:0.6b&digest=<sha256>&context=8192&runtime=<version>&profile=qwen3-code-v1
type modelBinding struct {
	digest, runtime, profile, compute string
	context                           int
}

func fromSelector(arg string) (*Embedder, error) {
	endpoint, query, explicit := strings.Cut(arg, "?")
	if !explicit {
		return New(arg, defaultModel)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return nil, fmt.Errorf("ollama: invalid selector query: %w", err)
	}
	for key, list := range values {
		if len(list) != 1 || (key != "model" && key != "digest" && key != "context" && key != "runtime" && key != "profile" && key != "compute") {
			return nil, fmt.Errorf("ollama: unknown or repeated selector key %q", key)
		}
	}
	model, digest, runtime, profile := values.Get("model"), values.Get("digest"), values.Get("runtime"), values.Get("profile")
	if !safeName(model) || !safeName(runtime) {
		return nil, fmt.Errorf("ollama: explicit model and runtime version are required")
	}
	digestBytes, err := hex.DecodeString(digest)
	if err != nil || len(digestBytes) != 32 {
		return nil, fmt.Errorf("ollama: explicit model requires a full SHA-256 digest")
	}
	contextLen, err := strconv.Atoi(values.Get("context"))
	if err != nil || contextLen <= 0 || contextLen > 1<<20 {
		return nil, fmt.Errorf("ollama: context must be in 1..1048576")
	}
	if profile != "plain-v1" && profile != "qwen3-code-v1" {
		return nil, fmt.Errorf("ollama: profile must be plain-v1 or qwen3-code-v1")
	}
	compute := values.Get("compute")
	if compute == "" {
		compute = "auto"
	}
	if compute != "auto" && compute != "cpu" {
		return nil, fmt.Errorf("ollama: compute must be auto or cpu")
	}
	e, err := New(endpoint, model)
	if err != nil {
		return nil, err
	}
	e.binding = &modelBinding{digest: strings.ToLower(digest), runtime: runtime, context: contextLen, profile: profile, compute: compute}
	// A single-thread CPU request can exceed the legacy interactive timeout
	// for a long admitted capsule. This changes only the request deadline;
	// source admission and scoring remain unchanged and bounded.
	if compute == "cpu" {
		e.client.Timeout = 2 * time.Minute
	}
	return e, nil
}

func safeName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:/+-", r) {
			continue
		}
		return false
	}
	return true
}

// EmbedQuery owns model-specific preparation; documents passed to Embed are
// not prefixed. The fixed instruction is versioned in ID, not tuned per query.
func (e *Embedder) EmbedQuery(ctx context.Context, query string) ([][]float32, error) {
	if e.binding != nil && e.binding.profile == "qwen3-code-v1" {
		query = "Instruct: Given a question about a code repository, retrieve relevant source code that answers the question.\nQuery: " + query
	}
	return e.Embed(ctx, []string{query})
}

func (e *Embedder) ModelSHA256() string {
	if e.binding != nil {
		return e.binding.digest
	}
	return ""
}

func (e *Embedder) Revision() string {
	if e.binding != nil {
		return e.binding.runtime
	}
	return ""
}

// The GGUF manifest binds its tokenizer as well as its tensors. This is not a
// separately claimed digest of a tokenizer.json file.
func (e *Embedder) TokenizerSHA256() string { return e.ModelSHA256() }

// Revalidate around every batch, including queries. A persistent retag or
// serving-version change fails closed; no vectors from that batch are returned.
// Ollama does not attest a digest in each embed response, so this is a local
// server trust contract, not cryptographic proof against concurrent ABA retags.
func (e *Embedder) checkBinding(ctx context.Context) error {
	var tags struct {
		Models []struct{ Name, Model, Digest string }
	}
	if err := e.readJSON(ctx, "/api/tags", nil, &tags); err != nil {
		return err
	}
	found := false
	for _, m := range tags.Models {
		if m.Name != e.model && m.Model != e.model {
			continue
		}
		found = true
		if strings.TrimPrefix(strings.ToLower(m.Digest), "sha256:") != e.binding.digest {
			return fmt.Errorf("ollama: model digest mismatch for %s", e.model)
		}
	}
	if !found {
		return fmt.Errorf("ollama: pinned model %s is not installed", e.model)
	}
	var version struct{ Version string }
	if err := e.readJSON(ctx, "/api/version", nil, &version); err != nil {
		return err
	}
	if version.Version != e.binding.runtime {
		return fmt.Errorf("ollama: runtime version mismatch: got %q, pinned %q", version.Version, e.binding.runtime)
	}
	return nil
}

func (e *Embedder) inspectModel(ctx context.Context) error {
	var show struct {
		ModelInfo    map[string]json.RawMessage `json:"model_info"`
		Capabilities []string
	}
	if err := e.readJSON(ctx, "/api/show", map[string]any{"model": e.model, "verbose": false}, &show); err != nil {
		return err
	}
	capable := false
	for _, c := range show.Capabilities {
		capable = capable || c == "embedding"
	}
	if !capable {
		return fmt.Errorf("ollama: selected model does not declare embedding capability")
	}
	var arch string
	if err := json.Unmarshal(show.ModelInfo["general.architecture"], &arch); err != nil {
		return fmt.Errorf("ollama: missing model architecture: %w", err)
	}
	var contextLen, dim int
	if err := json.Unmarshal(show.ModelInfo[arch+".context_length"], &contextLen); err != nil {
		return fmt.Errorf("ollama: missing context length: %w", err)
	}
	if err := json.Unmarshal(show.ModelInfo[arch+".embedding_length"], &dim); err != nil {
		return fmt.Errorf("ollama: missing embedding dimension: %w", err)
	}
	if contextLen < e.binding.context || dim <= 0 || dim > 65536 {
		return fmt.Errorf("ollama: invalid dimension or requested context exceeds model capacity")
	}
	if e.binding.profile == "qwen3-code-v1" && arch != "qwen3" {
		return fmt.Errorf("ollama: qwen3-code-v1 requires qwen3 architecture")
	}
	if prior := e.Dim(); prior != 0 && prior != dim {
		return fmt.Errorf("ollama: model dimension changed")
	}
	e.setDimOnce(dim)
	return nil
}

func (e *Embedder) readJSON(ctx context.Context, path string, input, output any) error {
	method := http.MethodGet
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body, method = bytes.NewReader(raw), http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+e.endpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: %s returned status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output); err != nil {
		return fmt.Errorf("ollama: %s invalid response: %w", path, err)
	}
	return nil
}
