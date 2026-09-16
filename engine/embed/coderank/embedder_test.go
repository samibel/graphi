package coderank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// A real HTTP fake verifies the bytes the adapter sends and lets tests corrupt
// the authoritative response without bypassing decoding or transport policy.
type fakeSidecar struct {
	*httptest.Server
	manifest           Manifest
	mutate             func(string, map[string]any)
	documents, queries []string
	calls              int
}

func newFakeSidecar(t *testing.T) *fakeSidecar {
	t.Helper()
	s := &fakeSidecar{manifest: validManifest()}
	s.manifest.Admission.MaxTokens = 2
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		out := map[string]any{"protocol": ProtocolVersion, "identity_digest": s.manifest.IdentityDigest(), "epoch": "process-1"}
		switch r.URL.Path {
		case "/v1/attestation":
			if r.Method != http.MethodGet {
				t.Error("attestation must use GET")
			}
			out["dimension"] = 768
		case "/v1/admit":
			var req admitRequest
			if err := decodeJSON(r.Body, &req); err != nil {
				t.Error(err)
			}
			if req.Protocol != ProtocolVersion || r.Method != http.MethodPost {
				t.Error("invalid admission request")
			}
			out["text"], out["token_count"] = "alpha beta", 2
		case "/v1/embed":
			var req embedRequest
			if err := decodeJSON(r.Body, &req); err != nil {
				t.Error(err)
			}
			if req.Protocol != ProtocolVersion || r.Method != http.MethodPost {
				t.Error("invalid embedding request")
			}
			switch req.Kind {
			case "document":
				s.documents = append(s.documents, req.Texts...)
			case "query":
				s.queries = append(s.queries, req.Texts...)
			default:
				t.Errorf("unknown kind %q", req.Kind)
			}
			vectors := make([][]float32, len(req.Texts))
			for i := range vectors {
				vectors[i] = make([]float32, 768)
				vectors[i][0] = 1
			}
			out["vectors"] = vectors
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if s.mutate != nil {
			s.mutate(r.URL.Path, out)
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Error(err)
		}
	}))
	s.manifest.Endpoint = s.URL
	t.Cleanup(s.Close)
	return s
}

func constructFake(t *testing.T, s *fakeSidecar) *Embedder {
	t.Helper()
	e, err := newFromManifest(t.Context(), s.manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEmbedderBindsEveryResponseAndSeparatesQueryPreparation(t *testing.T) {
	s := newFakeSidecar(t)
	e := constructFake(t, s)
	a, err := e.Admit(t.Context(), "alpha beta gamma")
	if err != nil || a.Text != "alpha beta" || a.TokenCount != 2 || a.Bound != "tokens" {
		t.Fatalf("admitted=%+v err=%v", a, err)
	}
	if _, err := e.Embed(t.Context(), []string{a.Text}); err != nil {
		t.Fatal(err)
	}
	if v, err := e.EmbedQuery(t.Context(), "find parser"); err != nil || len(v) != 1 || len(v[0]) != 768 {
		t.Fatalf("len=%d err=%v", len(v), err)
	}
	if !reflect.DeepEqual(s.documents, []string{"alpha beta"}) {
		t.Fatalf("documents=%q", s.documents)
	}
	if !reflect.DeepEqual(s.queries, []string{"Represent this query for searching relevant code: find parser"}) {
		t.Fatalf("queries=%q", s.queries)
	}
	if e.ID() != "coderank:nomic-ai/CodeRankEmbed@model-revision:"+s.manifest.IdentityDigest() {
		t.Fatal(e.ID())
	}
	if e.Dim() != 768 || e.Revision() != "model-revision" || e.ModelSHA256() != strings.Repeat("a", 64) || e.TokenizerSHA256() != strings.Repeat("b", 64) {
		t.Fatal("lost introspection pins")
	}
	for _, pin := range []string{ProtocolVersion, "sentence-transformers", "runtime-version", strings.Repeat("c", 64), "float32", "l2", "cpu", "first-n-tokens", "coderank-code-search", s.manifest.Query.InstructionSHA256} {
		if !strings.Contains(e.ChunkerConfig(), pin) {
			t.Fatalf("config missing %q", pin)
		}
	}
	if e.Profile() != s.manifest.AdmissionSpec() {
		t.Fatal("wrong admission profile")
	}
	if err := e.ProbeDim(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestEmbedderRejectsConstructionAttestation(t *testing.T) {
	for _, field := range []string{"identity_digest", "epoch", "protocol", "dimension"} {
		t.Run(field, func(t *testing.T) {
			s := newFakeSidecar(t)
			s.mutate = func(_ string, out map[string]any) {
				if field == "dimension" {
					out[field] = 512
				} else {
					out[field] = ""
				}
			}
			if e, err := newFromManifest(t.Context(), s.manifest, nil); err == nil || e != nil {
				t.Fatal("accepted invalid initial attestation")
			}
		})
	}
}

func TestEmbedderRejectsDriftAtEveryBoundary(t *testing.T) {
	for _, field := range []string{"identity_digest", "epoch", "protocol"} {
		for _, operation := range []string{"admit", "document", "query", "attest", "probe"} {
			t.Run(field+"/"+operation, func(t *testing.T) {
				s := newFakeSidecar(t)
				e := constructFake(t, s)
				want := e.ExpectedRuntimeAttestation()
				s.mutate = func(_ string, out map[string]any) { out[field] = "changed" }
				var err error
				switch operation {
				case "admit":
					var a embed.Admitted
					a, err = e.Admit(t.Context(), "alpha beta")
					if a != (embed.Admitted{}) {
						t.Fatal("leaked admission")
					}
				case "document":
					var v [][]float32
					v, err = e.Embed(t.Context(), []string{"alpha beta"})
					if v != nil {
						t.Fatal("leaked vectors")
					}
				case "query":
					var v [][]float32
					v, err = e.EmbedQuery(t.Context(), "query")
					if v != nil {
						t.Fatal("leaked query vector")
					}
				case "attest":
					_, err = e.RuntimeAttestation(t.Context())
				case "probe":
					err = e.ProbeDim(t.Context())
				}
				var binding *embed.RuntimeAttestationError
				if !errors.As(err, &binding) {
					t.Fatalf("want binding error, got %v", err)
				}
				if e.ExpectedRuntimeAttestation() != want {
					t.Fatal("repinned changed runtime")
				}
			})
		}
	}
}

func TestEmbedderRejectsInvalidAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		count      int
	}{
		{"rewritten", "ALPHA beta", 2}, {"nonprefix", "beta", 1}, {"negative", "alpha beta", -1}, {"overlimit", "alpha beta", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeSidecar(t)
			e := constructFake(t, s)
			s.mutate = func(_ string, out map[string]any) { out["text"], out["token_count"] = tc.text, tc.count }
			a, err := e.Admit(t.Context(), "alpha beta gamma")
			if !embed.IsAdmissionError(err) || a != (embed.Admitted{}) {
				t.Fatalf("admitted=%+v err=%v", a, err)
			}
		})
	}
}

func TestEmbedderAdmissionRequiresTokenCount(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			s := newFakeSidecar(t)
			e := constructFake(t, s)
			s.mutate = func(_ string, out map[string]any) {
				if missing {
					delete(out, "token_count")
				} else {
					out["token_count"] = nil
				}
			}
			if a, err := e.Admit(t.Context(), "alpha beta"); err == nil || a != (embed.Admitted{}) {
				t.Fatalf("accepted absent token count: admitted=%+v err=%v", a, err)
			}
		})
	}
	t.Run("explicit zero for empty text", func(t *testing.T) {
		s := newFakeSidecar(t)
		e := constructFake(t, s)
		s.mutate = func(_ string, out map[string]any) { out["text"], out["token_count"] = "", 0 }
		a, err := e.Admit(t.Context(), "")
		if err != nil || a != (embed.Admitted{Text: "", TokenCount: 0, Bound: "none"}) {
			t.Fatalf("explicit zero: admitted=%+v err=%v", a, err)
		}
	})
}

func TestEmbedderRejectsInvalidVectors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vectors any
	}{
		{"missing", [][]float32{}}, {"extra", [][]float32{make([]float32, 768), make([]float32, 768)}}, {"dimension", [][]float32{{1, 0}}},
		{"nan", json.RawMessage(`[[NaN]]`)}, {"inf", json.RawMessage(`[[1e999]]`)},
		{"null component", json.RawMessage(`[[null,` + strings.Repeat("0,", 766) + `0]]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeSidecar(t)
			e := constructFake(t, s)
			// The raw transport below permits non-JSON numeric tokens from a broken server.
			if raw, ok := tc.vectors.(json.RawMessage); ok {
				e.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					body := fmt.Sprintf(`{"protocol":%q,"identity_digest":%q,"epoch":"process-1","vectors":%s}`, ProtocolVersion, s.manifest.IdentityDigest(), raw)
					return responseBody(body), nil
				})}
			} else {
				s.mutate = func(_ string, out map[string]any) { out["vectors"] = tc.vectors }
			}
			if v, err := e.Embed(t.Context(), []string{"text"}); err == nil || v != nil {
				t.Fatal("accepted invalid vectors")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func responseBody(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestNonLoopbackRejectedBeforeRoundTrip(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "http://192.168.1.1", "http://user:pass@127.0.0.1", "http://localhost.evil"} {
		t.Run(endpoint, func(t *testing.T) {
			m := validManifest()
			m.Endpoint = endpoint
			called := false
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("unexpected dial") })}
			if _, err := newFromManifest(t.Context(), m, client); err == nil || called {
				t.Fatalf("err=%v called=%v", err, called)
			}
		})
	}
}

func TestRedirectRejectedBeforeFollowing(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls++ }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	m := validManifest()
	m.Endpoint = s.URL
	if _, err := newFromManifest(t.Context(), m, nil); err == nil {
		t.Fatal("accepted redirect")
	}
	if targetCalls != 0 {
		t.Fatal("followed redirect")
	}
}

func TestEmbedderEmptyAndAvailabilityDoNotDial(t *testing.T) {
	s := newFakeSidecar(t)
	e := constructFake(t, s)
	s.Close()
	if v, err := e.Embed(t.Context(), nil); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("vectors=%v err=%v", v, err)
	}
	if err := e.CheckAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("unexpected request count %d", s.calls)
	}
}

func TestEmbedderLocalhostDoesNotResolveDNS(t *testing.T) {
	s := newFakeSidecar(t)
	s.manifest.Endpoint = strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Error("DNS attempted")
		return nil, errors.New("DNS forbidden")
	}}
	t.Cleanup(func() { net.DefaultResolver = old })
	constructFake(t, s)
}

func TestEmbedderLoadsManifest(t *testing.T) {
	s := newFakeSidecar(t)
	data, err := json.Marshal(s.manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromManifest(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromManifest(t.Context(), path+".missing"); err == nil {
		t.Fatal("accepted missing manifest")
	}
}
