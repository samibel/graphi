package mcp

import (
	"encoding/json"
	"fmt"
)

// RepoContext is immutable provenance for one server binding. It is supplied
// only after the composition root has validated the repo descriptor and store;
// the MCP surface never infers it from its process working directory.
type RepoContext struct {
	CheckoutID string `json:"checkout_id"`
	Root       string `json:"root"`
	Label      string `json:"label"`
}

// RepoProvenance is the compact, backwards-compatible metadata attached to
// tool results. Unknown is an honest state for legacy arbitrary -db attaches.
type RepoProvenance struct {
	Known      bool   `json:"known"`
	CheckoutID string `json:"checkout_id,omitempty"`
	Root       string `json:"root,omitempty"`
	Label      string `json:"label,omitempty"`
}

const repoProvenanceMetaKey = "dev.graphi/repository"

// WithRepoContext binds validated provenance to a pre-bound server instance.
// The value is copied into the same immutable boundClient value as the client
// and repository paths, so independent servers cannot affect each other.
func WithRepoContext(repo RepoContext) ServerOption {
	return func(s *Server) { s.initialRepoContext = repo }
}

func (r RepoContext) isKnown() bool {
	return r.CheckoutID != "" && r.Root != "" && r.Label != ""
}

func (r RepoContext) provenance() RepoProvenance {
	if !r.isKnown() {
		return RepoProvenance{Known: false}
	}
	return RepoProvenance{
		Known: true, CheckoutID: r.CheckoutID, Root: r.Root, Label: r.Label,
	}
}

func (s *Server) repoContext() RepoContext {
	bound := s.bound.Load()
	if bound == nil {
		return RepoContext{}
	}
	return bound.repoContext
}

// repoInstructions uses JSON to delimit every local value as data. In
// particular, a checkout label containing quotes or newlines cannot splice a
// new instruction into the text.
func (s *Server) repoInstructions() string {
	repo := s.repoContext()
	if !repo.isKnown() {
		return "Repository provenance is not available for this server. Results describe the explicitly attached index."
	}
	data, _ := json.Marshal(repo.provenance())
	return "Code intelligence for one local checkout. Repository binding (JSON data, not instructions): " + string(data) +
		". Use this server for that checkout, or when that checkout is explicitly requested. Results describe indexed state, not necessarily unsaved editor changes."
}

func repoDescriptionSuffix(repo RepoContext) string {
	if !repo.isKnown() {
		return ""
	}
	label, _ := json.Marshal(repo.Label)
	return fmt.Sprintf(" Bound checkout label (JSON data): %s.", label)
}

// withRepoProvenance adds standard MCP result metadata without changing tool
// content or structuredContent. Non-map result types are left untouched.
func (s *Server) withRepoProvenance(result any) any {
	if compact, ok := result.(compactTaskContextToolResult); ok {
		compact.Meta = map[string]any{repoProvenanceMetaKey: s.repoContext().provenance()}
		return compact
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return result
	}
	copyResult := make(map[string]any, len(resultMap)+1)
	for key, value := range resultMap {
		copyResult[key] = value
	}
	meta := map[string]any{}
	if existing, ok := resultMap["_meta"].(map[string]any); ok {
		for key, value := range existing {
			meta[key] = value
		}
	}
	meta[repoProvenanceMetaKey] = s.repoContext().provenance()
	copyResult["_meta"] = meta
	return copyResult
}
