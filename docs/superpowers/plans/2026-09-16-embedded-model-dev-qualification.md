# Embedded-model Dev Qualification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a fail-closed, reproducible four-arm development qualification that determines whether pinned CodeRankEmbed can recover the missing nine whole-query passes before GrapHi spends another holdout.

**Architecture:** Add a generic runtime-attestation seam to the embedding lifecycle, then implement a development-only CodeRank sidecar client and a local reference sidecar. Extend the existing qrel-blind capture machinery with injected embedders, exact stage measurements, oracle controls, deterministic paired statistics, reproducibility checks, and a single promotion decision. This plan stops at the development promotion boundary; public selector integration, production parity, and a newly commissioned holdout require separate plans after this gate passes.

**Tech Stack:** Go 1.26.6, Go standard-library HTTP/JSON/crypto packages, SQLite generation store, existing GrapHi retrieval/eval packages, Python 3 standard-library HTTP server plus a separately installed pinned `sentence-transformers` runtime for live CodeRank inference.

**Spec:** `docs/superpowers/specs/2026-09-16-embedded-model-release-recovery-design.md`

## Global Constraints

- The standard GrapHi binary remains buildable with `CGO_ENABLED=0`.
- No embedder or model is shipped, activated, downloaded, or contacted without explicit operator opt-in.
- GrapHi performs no automatic non-loopback model access.
- Model, revision, runtime, tokenizer, query preparation, precision, dimension, and admission behavior are reproducibly pinned.
- A durable fingerprint change invalidates the semantic generation and requires re-indexing.
- The primary bundle ceiling remains exactly 1,200 `cl100k_base` tokens.
- The spent holdout remains `47/64`, `RELEASE: NO`; it is never reused for selection or authorization.
- The release population remains `N=64`, with the preregistered threshold `k=56`.
- `RELEASE: YES` requires at least 56 passing queries and every preregistered stratum, reproducibility, operating-budget, and run-validity gate.
- CodeRank is development-only in this plan: do not add `init`, `RegisterScheme`, a CLI selector, default configuration, model download, or sidecar process launch.
- The CodeRank endpoint must be literal loopback (`127.0.0.0/8`, `::1`, or `localhost`), must reject redirects and credentials, and must never resolve or dial a remote hostname.
- The reference sidecar loads only a local artifact directory with `local_files_only=True`; its model-provided Python code is trusted only after every local artifact and runtime digest has been verified.
- Documents receive no query prefix. Queries receive exactly `Represent this query for searching relevant code: ` under a versioned preparation profile whose instruction digest enters the durable identity.
- Process epoch is a runtime TOCTOU barrier and never enters the durable generation fingerprint.
- No automatic Potion/CodeRank substitution, cross-fingerprint vector mixing, degraded capture, threshold waiver, or post-hoc gate change is allowed.

---

## File Structure

### Embedding lifecycle

- Create `engine/embed/attestation.go`: runtime identity/epoch types, validation, comparison, and typed mismatch errors.
- Create `engine/embed/attestation_test.go`: unit coverage for malformed attestations and identity/epoch changes.
- Modify `engine/embed/query.go`: attest immediately before every query embedding and verify again after it.
- Modify `engine/embed/query_test.go`: prove query-time fail-closed behavior while leaving non-attested embedders unchanged.
- Modify `engine/embed/generate.go`: verify attestation before dimension discovery and immediately before every generation commit.
- Modify `engine/embed/generate_test.go`: prove an epoch/identity change aborts staging and preserves the previous active generation, including a carry-forward-only build.

### CodeRank development adapter and sidecar

- Create `engine/embed/coderank/manifest.go`: strict local manifest schema, canonical durable identity, digest validation, and endpoint policy.
- Create `engine/embed/coderank/manifest_test.go`: table tests for every pinned identity field and endpoint rejection.
- Create `engine/embed/coderank/protocol.go`: versioned request/response DTOs shared by the adapter methods.
- Create `engine/embed/coderank/embedder.go`: pure-Go loopback client implementing embedding, admission, query preparation, availability, dimension discovery, and runtime attestation.
- Create `engine/embed/coderank/embedder_test.go`: `httptest` contract tests for exact bytes, response binding, redirects, restart/reload drift, and no remote dial.
- Create `scripts/eval/coderank_sidecar.py`: evaluation-only local HTTP process using pinned local SentenceTransformer artifacts.
- Create `scripts/eval/tests/test_coderank_sidecar.py`: Python `unittest` protocol tests with a fake encoder; no model download is needed.
- Create `docs/eval/retrieval/coderank-sidecar-manifest.example.json`: fully populated schema example with deliberately non-live all-zero digests.
- Create `docs/eval/retrieval/coderank-sidecar-protocol.md`: operator-visible protocol, trust boundary, local setup, and failure semantics.

### Four-arm qualification

- Modify `engine/embed/static/static.go` and `engine/embed/static/embed.go`: add a non-registered evaluation constructor for an explicit 8,192-token Potion profile while retaining the 512-token production constructor.
- Modify `engine/embed/static/admission_test.go`: prove diagnostic admission and fingerprints differ without changing the public selector.
- Modify `internal/eval/retrieval/taskcontext.go`: split selector resolution from index construction so an already-constructed development embedder can be injected.
- Modify `internal/eval/retrieval/taskcontext_test.go`: verify injected and selector-created embedders share the same production index path.
- Create `internal/eval/retrieval/model_qualification.go`: arm/preregistration/observation/result types, exact dataset validation, and run-validity checks.
- Create `internal/eval/retrieval/model_qualification_test.go`: exact `64`-query and stratum-shape tests plus fail-closed validity tests.
- Create `internal/eval/retrieval/model_qualification_capture_test.go`: environment-gated four-arm capture over two independent builds.
- Create `internal/eval/retrieval/model_qualification_oracle.go`: evaluation-only oracle packers and candidate injection, kept outside production packages.
- Create `internal/eval/retrieval/model_qualification_oracle_test.go`: leakage and fixed-budget tests for all three controls.
- Create `internal/eval/retrieval/model_qualification_stats.go`: stable paired bootstrap, stratum deltas, operating-budget checks, and promotion decision.
- Create `internal/eval/retrieval/model_qualification_stats_test.go`: fixtures for every independent gate and the combined all-gates rule.
- Create `internal/eval/retrieval/model_qualification_report.go`: canonical JSON and Markdown report writer.
- Create `internal/eval/retrieval/model_qualification_report_test.go`: golden report proving all provenance and gate fields are present.

## Task 1: Add the runtime-attestation contract and protect query embeddings

**Files:**
- Create: `engine/embed/attestation.go`
- Create: `engine/embed/attestation_test.go`
- Modify: `engine/embed/query.go`
- Modify: `engine/embed/query_test.go`

**Interfaces:**
- Consumes: existing `embed.Embedder` and optional `embed.QueryEmbedder`.
- Produces: `RuntimeAttestation`, `RuntimeAttestor`, `ValidateRuntimeAttestation(RuntimeAttestation) error`, `VerifyRuntime(context.Context, Embedder, string) error`, and typed `*RuntimeAttestationError`.

- [ ] **Step 1: Write failing attestation validation and query-boundary tests**

```go
func TestValidateRuntimeAttestation(t *testing.T) {
	valid := RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "pid-42-start-1700000000"}
	if err := ValidateRuntimeAttestation(valid); err != nil { t.Fatal(err) }
	for _, bad := range []RuntimeAttestation{
		{IdentityDigest: "abc", Epoch: valid.Epoch},
		{IdentityDigest: strings.Repeat("A", 64), Epoch: valid.Epoch},
		{IdentityDigest: valid.IdentityDigest, Epoch: ""},
		{IdentityDigest: valid.IdentityDigest, Epoch: strings.Repeat("x", 129)},
	} {
		if err := ValidateRuntimeAttestation(bad); err == nil { t.Fatalf("accepted %#v", bad) }
	}
}

func TestEmbedQueryRejectsEpochChangeBeforeEmbedding(t *testing.T) {
	e := &attestedQueryFake{
		expected: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-1"},
		observed: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-2"},
	}
	_, err := EmbedQuery(t.Context(), e, "where is config loaded")
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || e.embedCalls != 0 { t.Fatalf("err=%v calls=%d", err, e.embedCalls) }
}
```

- [ ] **Step 2: Run the focused tests and confirm the missing symbols fail compilation**

Run: `go test ./engine/embed -run 'TestValidateRuntimeAttestation|TestEmbedQueryRejectsEpochChangeBeforeEmbedding'`

Expected: FAIL because `RuntimeAttestation`, `ValidateRuntimeAttestation`, and `RuntimeAttestationError` do not exist.

- [ ] **Step 3: Implement the attestation seam and typed error**

```go
type RuntimeAttestation struct {
	IdentityDigest string `json:"identity_digest"`
	Epoch          string `json:"epoch"`
}

type RuntimeAttestor interface {
	ExpectedRuntimeAttestation() RuntimeAttestation
	RuntimeAttestation(context.Context) (RuntimeAttestation, error)
}

type RuntimeAttestationError struct {
	Phase    string
	Expected RuntimeAttestation
	Observed RuntimeAttestation
	Reason   string
}

func VerifyRuntime(ctx context.Context, e Embedder, phase string) error {
	a, ok := e.(RuntimeAttestor)
	if !ok { return nil }
	want := a.ExpectedRuntimeAttestation()
	if err := ValidateRuntimeAttestation(want); err != nil {
		return &RuntimeAttestationError{Phase: phase, Expected: want, Reason: err.Error()}
	}
	got, err := a.RuntimeAttestation(ctx)
	if err != nil { return fmt.Errorf("embed: runtime attestation at %s: %w", phase, err) }
	if err := ValidateRuntimeAttestation(got); err != nil {
		return &RuntimeAttestationError{Phase: phase, Expected: want, Observed: got, Reason: err.Error()}
	}
	if got != want {
		return &RuntimeAttestationError{Phase: phase, Expected: want, Observed: got, Reason: "identity digest or process epoch changed"}
	}
	return nil
}
```

Validation must accept only a 64-character lowercase hexadecimal identity digest and a non-empty epoch of at most 128 printable ASCII characters. `Error()` must include the phase and must not include secrets or request text.

- [ ] **Step 4: Wrap the existing query dispatch with pre/post verification**

```go
func EmbedQuery(ctx context.Context, e Embedder, query string) ([][]float32, error) {
	if err := VerifyRuntime(ctx, e, "before query embed"); err != nil { return nil, err }
	var vectors [][]float32
	var err error
	if q, ok := e.(QueryEmbedder); ok {
		vectors, err = q.EmbedQuery(ctx, query)
	} else {
		vectors, err = e.Embed(ctx, []string{query})
	}
	if err != nil { return nil, err }
	if err := VerifyRuntime(ctx, e, "after query embed"); err != nil { return nil, err }
	return vectors, nil
}
```

Add tests proving an epoch change after the embedding response is rejected and a legacy non-attested embedder still receives exactly one call.

- [ ] **Step 5: Run and commit the query-attestation unit**

Run: `gofmt -w engine/embed/attestation.go engine/embed/attestation_test.go engine/embed/query.go engine/embed/query_test.go && go test ./engine/embed`

Expected: PASS.

```bash
git add engine/embed/attestation.go engine/embed/attestation_test.go engine/embed/query.go engine/embed/query_test.go
git commit -m "feat(embed): attest every query embedding"
```

## Task 2: Protect generation publication with initial and pre-commit attestation

**Files:**
- Modify: `engine/embed/generate.go`
- Modify: `engine/embed/generate_test.go`

**Interfaces:**
- Consumes: `embed.VerifyRuntime(context.Context, Embedder, string) error` from Task 1 and existing `GenerationStore` staging/commit behavior.
- Produces: attestation checks at `before generation build` and `before generation commit` for normal, empty, and carry-forward-only builds.

- [ ] **Step 1: Add failing tests for build drift and prior-generation preservation**

```go
func TestGenerateAndPersistEpochChangeBeforeCommitPreservesPriorActive(t *testing.T) {
	store, prior := readyGenerationFixture(t)
	e := newAttestedEmbedder("epoch-1")
	e.changeEpochAfterEmbed = "epoch-2"
	_, err := GenerateAndPersist(t.Context(), registryWith(t, e), fixtureNodes(), fixtureDocuments(), NewIndex(), store, "graph-2")
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || mismatch.Phase != "before generation commit" { t.Fatalf("%T %v", err, err) }
	got, state, err := store.Active(t.Context(), prior.Fingerprint, nil)
	if err != nil || state != StateReady || got.ID != prior.ID { t.Fatalf("active=%+v state=%s err=%v", got, state, err) }
}

func TestGenerateAndPersistCarryForwardStillReattestsBeforeCommit(t *testing.T) {
	// Seed a ready generation, run unchanged documents with zero Embed calls,
	// change the observed epoch on the pre-commit attestation, and assert abort.
}
```

Also add an empty-node test and an initial-attestation failure test; both must assert no generation is promoted.

- [ ] **Step 2: Run the focused tests and verify they fail on the current unchecked commit paths**

Run: `go test ./engine/embed -run 'TestGenerateAndPersist.*Attest|TestGenerateAndPersistEpochChange'`

Expected: FAIL because generation currently performs no runtime verification.

- [ ] **Step 3: Add the initial verification before dimension discovery**

```go
	res := GenerateResult{Configured: true, EmbedderID: emb.ID()}
	if err := VerifyRuntime(ctx, emb, "before generation build"); err != nil {
		return GenerateResult{}, err
	}
```

Place this after the nil `DocumentSource` guard and before `DimDiscoverer.ProbeDim`, so fingerprint construction cannot use a stale process identity.

- [ ] **Step 4: Add one pre-commit helper and call it on both commit paths**

```go
func commitAttested(ctx context.Context, emb Embedder, build Build) error {
	if err := VerifyRuntime(ctx, emb, "before generation commit"); err != nil {
		if build != nil { _ = build.Abort(ctx) }
		return err
	}
	if build == nil { return nil }
	return build.Commit(ctx)
}
```

Replace the zero-node and normal `build.Commit(ctx)` calls with `commitAttested`. Do not add per-row generic attestations: CodeRank response binding in Task 4 covers every `/admit` and `/embed` response without adding network calls for legacy embedders.

- [ ] **Step 5: Run generation/store tests and commit**

Run: `gofmt -w engine/embed/generate.go engine/embed/generate_test.go && go test ./engine/embed -run 'GenerateAndPersist|GenerationStore'`

Expected: PASS, including the prior-active preservation assertions.

```bash
git add engine/embed/generate.go engine/embed/generate_test.go
git commit -m "feat(embed): attest generation publication"
```

## Task 3: Define and validate the pinned CodeRank manifest and wire protocol

**Files:**
- Create: `engine/embed/coderank/manifest.go`
- Create: `engine/embed/coderank/manifest_test.go`
- Create: `engine/embed/coderank/protocol.go`
- Create: `docs/eval/retrieval/coderank-sidecar-manifest.example.json`

**Interfaces:**
- Consumes: `embed.AdmissionSpec` and SHA-256 canonicalization conventions.
- Produces: `LoadManifest(string) (Manifest, error)`, `Manifest.IdentityDigest() string`, `Manifest.AdmissionSpec() embed.AdmissionSpec`, protocol constants and DTOs.

- [ ] **Step 1: Write failing manifest tests for a valid pin set and every forbidden endpoint**

```go
func TestManifestValidateAndIdentityDigest(t *testing.T) {
	m := validManifest()
	if err := m.Validate(); err != nil { t.Fatal(err) }
	if got := m.IdentityDigest(); len(got) != 64 { t.Fatalf("digest=%q", got) }
	changed := m
	changed.Query.InstructionSHA256 = strings.Repeat("b", 64)
	if changed.IdentityDigest() == m.IdentityDigest() { t.Fatal("query instruction did not change identity") }
}

func TestManifestRejectsNonLoopbackAndCredentials(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.com:8443", "http://192.168.1.5:8080",
		"http://user:pass@127.0.0.1:8080", "http://coderank.local:8080",
	} {
		m := validManifest(); m.Endpoint = endpoint
		if err := m.Validate(); err == nil { t.Fatalf("accepted %s", endpoint) }
	}
}
```

- [ ] **Step 2: Run the package test and confirm it fails because the package does not exist**

Run: `go test ./engine/embed/coderank`

Expected: FAIL with missing package/files.

- [ ] **Step 3: Implement the strict manifest schema and canonical identity**

```go
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	Protocol      string          `json:"protocol"`
	Endpoint      string          `json:"endpoint"`
	Model         ArtifactPin     `json:"model"`
	Tokenizer     ArtifactPin     `json:"tokenizer"`
	Runtime       RuntimePin      `json:"runtime"`
	Dimension     int             `json:"dimension"`
	Precision     string          `json:"precision"`
	Normalization string          `json:"normalization"`
	Compute       string          `json:"compute"`
	Admission     AdmissionPin    `json:"admission"`
	Query         QueryProfilePin `json:"query"`
}

type ArtifactPin struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}
type RuntimePin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}
type AdmissionPin struct {
	MaxTokens        int    `json:"max_tokens"`
	Reserve          int    `json:"reserve"`
	Algorithm        string `json:"algorithm"`
	AlgorithmVersion string `json:"algorithm_version"`
}
type QueryProfilePin struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	Instruction       string `json:"instruction"`
	InstructionSHA256 string `json:"instruction_sha256"`
}
```

Use explicit JSON tags on every nested field. `Validate` must require schema `1`, protocol `graphi-coderank/1`, dimension `768`, precision `float32`, normalization `l2`, compute `cpu`, positive admission values, exact instruction digest, and lowercase 64-hex digests. Parse the endpoint with `net/url`; allow only `http`, no user info/path/query/fragment, and host names `localhost`, literal `::1`, or an IPv4 address whose first octet is `127`. Do not perform DNS resolution.

Canonicalize every field in a fixed length-prefixed order and hash the result with SHA-256. `IdentityDigest` includes the endpoint-independent embedding-space fields but excludes `Endpoint` and process epoch; moving the same pinned sidecar to another loopback port must not force re-indexing.

- [ ] **Step 4: Define protocol DTOs that bind every response to identity and epoch**

```go
const ProtocolVersion = "graphi-coderank/1"

type responseBinding struct {
	Protocol       string `json:"protocol"`
	IdentityDigest string `json:"identity_digest"`
	Epoch          string `json:"epoch"`
}
type attestationResponse struct {
	responseBinding
	Dimension int `json:"dimension"`
}
type admitRequest struct {
	Protocol string `json:"protocol"`
	Text     string `json:"text"`
}
type admitResponse struct {
	responseBinding
	Text       string `json:"text"`
	TokenCount int    `json:"token_count"`
}
type embedRequest struct {
	Protocol string   `json:"protocol"`
	Kind     string   `json:"kind"`
	Texts    []string `json:"texts"`
}
type embedResponse struct {
	responseBinding
	Vectors [][]float32 `json:"vectors"`
}
```

Accept no unknown response fields by decoding with `json.Decoder.DisallowUnknownFields()`.

- [ ] **Step 5: Add a non-live example manifest and commit**

The example must use `http://127.0.0.1:8765`, CodeRank model/revision labels, the exact query instruction, all-zero digest sentinels, and a warning that the sentinel manifest is intentionally rejected until the operator replaces every digest with locally computed values.

Run: `gofmt -w engine/embed/coderank/*.go && go test ./engine/embed/coderank`

Expected: PASS.

```bash
git add engine/embed/coderank docs/eval/retrieval/coderank-sidecar-manifest.example.json
git commit -m "feat(eval): define pinned coderank sidecar contract"
```

## Task 4: Implement the fail-closed pure-Go CodeRank adapter

**Files:**
- Create: `engine/embed/coderank/embedder.go`
- Create: `engine/embed/coderank/embedder_test.go`

**Interfaces:**
- Consumes: Task 3 `Manifest`/DTOs and Task 1 `embed.RuntimeAttestor`.
- Produces: `NewFromManifest(context.Context, string) (*Embedder, error)` implementing `embed.Embedder`, `embed.QueryEmbedder`, `embed.Admission`, `embed.AdmissionProfile`, `embed.DimDiscoverer`, `embed.AvailabilityChecker`, and `embed.RuntimeAttestor`.

- [ ] **Step 1: Write a fake-sidecar test for construction, admission, document embed, and query embed**

```go
func TestEmbedderBindsEveryResponseAndSeparatesQueryPreparation(t *testing.T) {
	s := newFakeSidecar(t, fakeSidecarOptions{})
	m := validManifestForServer(t, s.URL)
	e, err := newFromManifest(t.Context(), m, testClient())
	if err != nil { t.Fatal(err) }
	admitted, err := e.Admit(t.Context(), "alpha beta gamma")
	if err != nil || admitted.Text != "alpha beta" || admitted.TokenCount != 2 { t.Fatalf("%+v %v", admitted, err) }
	if _, err := e.Embed(t.Context(), []string{admitted.Text}); err != nil { t.Fatal(err) }
	if _, err := e.EmbedQuery(t.Context(), "find parser"); err != nil { t.Fatal(err) }
	if got := s.DocumentTexts(); !reflect.DeepEqual(got, []string{"alpha beta"}) { t.Fatalf("documents=%q", got) }
	if got := s.QueryTexts(); !reflect.DeepEqual(got, []string{CodeSearchInstruction + "find parser"}) { t.Fatalf("queries=%q", got) }
}
```

Add tests that reject rewritten/non-prefix admitted text, mismatched token count, wrong vector count/dimension, NaN/Inf, identity drift, epoch drift, a redirect, and any non-loopback URL before `RoundTrip` is called.

- [ ] **Step 2: Run tests and verify constructor/interface failures**

Run: `go test ./engine/embed/coderank -run 'TestEmbedder|TestRedirect|TestNonLoopback'`

Expected: FAIL because `Embedder` is not implemented.

- [ ] **Step 3: Construct the adapter by validating the manifest and pinning the first attestation**

```go
type Embedder struct {
	manifest Manifest
	client   *http.Client
	expected embed.RuntimeAttestation
}

func NewFromManifest(ctx context.Context, path string) (*Embedder, error) {
	m, err := LoadManifest(path)
	if err != nil { return nil, err }
	c := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: loopbackTransport(m.Endpoint),
	}
	e := &Embedder{manifest: m, client: c}
	got, err := e.fetchAttestation(ctx)
	if err != nil { return nil, err }
	wantIdentity := m.IdentityDigest()
	if got.IdentityDigest != wantIdentity { return nil, &embed.RuntimeAttestationError{Phase: "adapter construction", Expected: embed.RuntimeAttestation{IdentityDigest: wantIdentity}, Observed: got, Reason: "manifest identity mismatch"} }
	e.expected = got
	return e, nil
}
```

The custom transport must dial the already-parsed literal loopback address only. `ID()` must be `coderank:<model-id>@<revision>:<full-identity-digest>`. The introspection methods must expose the pinned revision, model digest, tokenizer digest, dimension, and a chunker/config string containing protocol, runtime, precision, normalization, compute, admission, and query-profile identities.

- [ ] **Step 4: Implement response-bound admission and embedding**

```go
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
```

`Admit` posts canonical document text and accepts only an unchanged UTF-8 prefix with a non-negative token count not exceeding the manifest limit. `Embed` posts `kind=document`; `EmbedQuery` prepends the manifest instruction once and posts `kind=query`. Both reject empty batches, response cardinality/dimension mismatch, and non-finite values. Every response passes `verifyBinding` before data is returned.

- [ ] **Step 5: Implement availability, dimension, and attestation interfaces without registration**

```go
func (e *Embedder) ExpectedRuntimeAttestation() embed.RuntimeAttestation { return e.expected }
func (e *Embedder) RuntimeAttestation(ctx context.Context) (embed.RuntimeAttestation, error) { return e.fetchAttestation(ctx) }
func (e *Embedder) ProbeDim(ctx context.Context) error {
	a, err := e.fetchAttestation(ctx); if err != nil { return err }
	if a != e.expected { return mismatch("dimension probe", e.expected, a) }
	return nil
}
func (e *Embedder) CheckAvailable(ctx context.Context) error { return embed.VerifyRuntime(ctx, e, "availability check") }
```

Add compile-time interface assertions. Confirm the package has no `init` function and never calls `embed.RegisterScheme`.

- [ ] **Step 6: Run package and default-build safety tests, then commit**

Run: `gofmt -w engine/embed/coderank/*.go && go test ./engine/embed/coderank ./engine/embed ./internal/canary/gate && CGO_ENABLED=0 go build ./cmd/graphi`

Expected: PASS; no sidecar process is needed because package tests use loopback fakes.

```bash
git add engine/embed/coderank
git commit -m "feat(eval): add fail-closed coderank adapter"
```

## Task 5: Add the evaluation-only local CodeRank reference sidecar

**Files:**
- Create: `scripts/eval/coderank_sidecar.py`
- Create: `scripts/eval/tests/test_coderank_sidecar.py`
- Create: `docs/eval/retrieval/coderank-sidecar-protocol.md`

**Interfaces:**
- Consumes: Task 3 protocol `graphi-coderank/1` and manifest fields.
- Produces: loopback-only `/v1/attestation`, `/v1/admit`, and `/v1/embed` endpoints whose every response carries the immutable process binding.

- [ ] **Step 1: Write fake-encoder Python tests before importing SentenceTransformers**

```python
class SidecarContractTest(unittest.TestCase):
    def test_query_and_document_paths_are_distinct(self):
        app = SidecarApp(valid_manifest(), FakeEncoder())
        doc = app.embed({"protocol": PROTOCOL, "kind": "document", "texts": ["x"]})
        qry = app.embed({"protocol": PROTOCOL, "kind": "query", "texts": ["find x"]})
        self.assertEqual(app.encoder.inputs, ["x", QUERY_INSTRUCTION + "find x"])
        self.assertEqual(doc["epoch"], qry["epoch"])

    def test_admission_returns_an_unchanged_utf8_prefix(self):
        app = SidecarApp(valid_manifest(max_tokens=2), FakeEncoder(tokens=["alpha", " beta", " gamma"]))
        got = app.admit({"protocol": PROTOCOL, "text": "alpha beta gamma"})
        self.assertEqual((got["text"], got["token_count"]), ("alpha beta", 2))
```

Also test malformed JSON, wrong protocol/kind, oversized request bodies, more than 32 texts, non-loopback bind addresses, runtime version mismatch, and artifact digest mismatch.

- [ ] **Step 2: Run the Python tests and confirm the missing module fails**

Run: `python3 -m unittest scripts.eval.tests.test_coderank_sidecar -v`

Expected: FAIL because `scripts.eval.coderank_sidecar` does not exist.

- [ ] **Step 3: Implement manifest verification and the model-loading boundary**

```python
def load_encoder(manifest: dict, model_dir: pathlib.Path):
    verify_tree_digest(model_dir, manifest["model"]["sha256"])
    verify_runtime_versions(manifest["runtime"])
    from sentence_transformers import SentenceTransformer
    return SentenceTransformer(
        str(model_dir), device="cpu", trust_remote_code=True, local_files_only=True
    )
```

The script must import `sentence_transformers` only inside `load_encoder`, refuse a model ID or URL in place of a local directory, set offline environment flags before import, verify tokenizer and model/tree digests, and compare installed package/runtime versions with the manifest. The process epoch is generated once from `secrets.token_hex(32)` and is immutable.

- [ ] **Step 4: Implement the bounded loopback HTTP service**

Use `http.server.ThreadingHTTPServer`; reject bind hosts other than `127.0.0.1` or `::1`, cap request bodies at 1 MiB, use strict JSON key checks, and return JSON errors without stack traces. `admit` must tokenize without silent truncation, choose the longest UTF-8 prefix whose prepared token count fits, and report the exact post-preparation count. `embed` must set truncation off, encode documents unchanged, prefix queries exactly once, normalize vectors, and reject any dimension other than the manifest dimension.

- [ ] **Step 5: Document exact operator commands and trust limits**

The protocol document must include these literal lifecycle steps:

```bash
python3 scripts/eval/coderank_sidecar.py verify --manifest /absolute/path/coderank.json --model-dir /absolute/path/CodeRankEmbed
python3 scripts/eval/coderank_sidecar.py serve --manifest /absolute/path/coderank.json --model-dir /absolute/path/CodeRankEmbed --bind 127.0.0.1 --port 8765
curl --fail --silent http://127.0.0.1:8765/v1/attestation
```

It must state that `trust_remote_code=True` executes locally pinned model code, the contract protects against accidental drift rather than a lying process, GrapHi never launches/downloads the sidecar, and any restart requires a new adapter instance.

- [ ] **Step 6: Run tests and commit**

Run: `python3 -m unittest scripts.eval.tests.test_coderank_sidecar -v`

Expected: PASS using only the fake encoder.

```bash
git add scripts/eval/coderank_sidecar.py scripts/eval/tests/test_coderank_sidecar.py docs/eval/retrieval/coderank-sidecar-protocol.md
git commit -m "feat(eval): add local coderank reference sidecar"
```

## Task 6: Add the Potion/8192 diagnostic and injected-embedder eval seam

**Files:**
- Modify: `engine/embed/static/static.go`
- Modify: `engine/embed/static/embed.go`
- Modify: `engine/embed/static/admission_test.go`
- Modify: `internal/eval/retrieval/taskcontext.go`
- Modify: `internal/eval/retrieval/taskcontext_test.go`

**Interfaces:**
- Consumes: existing production `static.New(string)` and task-context index builder.
- Produces: `static.NewForEvaluation(string, int) (*static.Embedder, error)` and `buildTaskContextIndexWithEmbedder(context.Context, string, string, embed.Embedder, string, io.Writer) (*taskContextIndex, error)`.

- [ ] **Step 1: Write failing tests for an 8,192-token diagnostic identity**

```go
func TestNewForEvaluationUsesDistinct8192AdmissionProfile(t *testing.T) {
	production := newPinnedFixtureEmbedder(t)
	diagnostic := newPinnedFixtureEvaluationEmbedder(t, 8192)
	if got := production.Profile().MaxTokens; got != 512 { t.Fatalf("production=%d", got) }
	if got := diagnostic.Profile().MaxTokens; got != 8192 { t.Fatalf("diagnostic=%d", got) }
	if production.ID() == diagnostic.ID() { t.Fatal("diagnostic profile reused production identity") }
	long := strings.Repeat("known ", 700)
	a, err := diagnostic.Admit(t.Context(), long)
	if err != nil || a.TokenCount <= 512 { t.Fatalf("tokens=%d err=%v", a.TokenCount, err) }
}
```

Add a registry test showing the existing `static:<model>@<revision>` selector still constructs 512 and that no `static-8192` selector exists.

- [ ] **Step 2: Run focused tests and observe the missing evaluation constructor**

Run: `go test ./engine/embed/static -run 'Evaluation|8192|AdmissionProfile'`

Expected: FAIL because `NewForEvaluation` is undefined.

- [ ] **Step 3: Parameterize the loaded model without changing production defaults**

Add `admissionMaxTokens int` to `Embedder`, initialize it to `DefaultMaxLength` in `NewWithPinnedModel`, and apply it to `Model.maxLength` immediately after every successful eager or lazy load. Implement:

```go
func NewForEvaluation(arg string, maxTokens int) (*Embedder, error) {
	if maxTokens <= 0 || maxTokens > 8192 { return nil, fmt.Errorf("static: evaluation max tokens %d outside 1..8192", maxTokens) }
	e, err := New(arg)
	if err != nil { return nil, err }
	e.admissionMaxTokens = maxTokens
	if e.loadedM != nil { e.loadedM.maxLength = maxTokens }
	return e, nil
}
```

Use the active `maxLength` in `Model.admissionSpec`, `Embedder.Profile`, and `Embedder.ID`. Include `eval-max-8192` in the implementation-contract segment when the limit differs from 512. Do not add a registry constructor.

- [ ] **Step 4: Write the failing injected-index parity test**

```go
func TestBuildTaskContextIndexWithEmbedderUsesProvidedInstance(t *testing.T) {
	e := &countingEmbedder{id: "injected", dim: 3}
	idx, err := buildTaskContextIndexWithEmbedder(t.Context(), fixtureRoot(t), t.TempDir(), e, "eval:injected", io.Discard)
	if err != nil { t.Fatal(err) }
	defer idx.store.Close()
	if e.calls == 0 || idx.embedderID != e.ID() { t.Fatalf("calls=%d id=%q", e.calls, idx.embedderID) }
}
```

- [ ] **Step 5: Refactor selector resolution into a delegating wrapper**

```go
func buildTaskContextIndex(ctx context.Context, root, workDir, selector string, log io.Writer) (*taskContextIndex, error) {
	emb, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil || emb == nil { return nil, fmt.Errorf("task-context eval: embedder %q unavailable: %v", selector, err) }
	return buildTaskContextIndexWithEmbedder(ctx, root, workDir, emb, selector, log)
}
```

Move the remainder of the current function unchanged into `buildTaskContextIndexWithEmbedder`, removing only the constructor call. The label is provenance, not a registry selector, and must never override `emb.ID()` in the fingerprint.

- [ ] **Step 6: Run package tests and commit**

Run: `gofmt -w engine/embed/static/*.go internal/eval/retrieval/taskcontext.go internal/eval/retrieval/taskcontext_test.go && go test ./engine/embed/static ./internal/eval/retrieval`

Expected: PASS; existing static fixtures still assert 512.

```bash
git add engine/embed/static internal/eval/retrieval/taskcontext.go internal/eval/retrieval/taskcontext_test.go
git commit -m "feat(eval): add potion 8192 diagnostic seam"
```

## Task 7: Freeze the qualification schema and fail-closed preregistration

**Files:**
- Create: `internal/eval/retrieval/model_qualification.go`
- Create: `internal/eval/retrieval/model_qualification_test.go`

**Interfaces:**
- Consumes: existing `Loaded`, `Query`, `Fingerprint`, retrieval states, candidate binding, and compact version.
- Produces: qualification constants/types, `ValidateQualificationDataset(*Loaded) error`, and `ValidateQualificationPreregistration(QualificationPreregistration) error`.

- [ ] **Step 1: Write failing exact-population tests**

```go
func TestValidateQualificationDatasetRequiresExactHoldoutShape(t *testing.T) {
	loaded := qualificationDatasetFixture(t, map[string]int{
		StratumAmbiguous: 10, StratumArchitectureFlow: 11, StratumConfigDocs: 10,
		StratumExactIdentifier: 11, StratumExactPath: 11, StratumNLBehaviour: 11,
	})
	if err := ValidateQualificationDataset(loaded); err != nil { t.Fatal(err) }
	loaded.Dataset.Queries[0].Split = SplitHoldout
	if err := ValidateQualificationDataset(loaded); err == nil { t.Fatal("accepted holdout row") }
}
```

Add single-mutation tests for 63/65 queries, every wrong stratum count, `no_hit`, missing grade-3 span, blank family/provenance, duplicate family, and a dataset path under a directory containing the spent holdout ID.

- [ ] **Step 2: Define immutable arm and preregistration types**

```go
type QualificationArm string
const (
	ArmLexical QualificationArm = "M0_lexical"
	ArmPotion512 QualificationArm = "M1_potion_512"
	ArmPotion8192 QualificationArm = "M2_potion_8192"
	ArmCodeRank QualificationArm = "M3_coderank"
)

type QualificationPreregistration struct {
	SchemaVersion       int                        `json:"schema_version"`
	DatasetSHA256       string                     `json:"dataset_sha256"`
	SourceRepoSHA       string                     `json:"source_repo_sha"`
	CandidateSHA        string                     `json:"candidate_sha"`
	CandidateDiffSHA256 string                     `json:"candidate_diff_sha256"`
	ReaderPromptSHA256  string                     `json:"reader_prompt_sha256"`
	GraderPromptSHA256  string                     `json:"grader_prompt_sha256"`
	Arms                map[QualificationArm]ArmPin `json:"arms"`
	CompactVersion      string                     `json:"compact_version"`
	TokenBudget         int                        `json:"token_budget"`
	BootstrapSamples    int                        `json:"bootstrap_samples"`
	BootstrapSeed       uint64                     `json:"bootstrap_seed"`
	Thresholds          QualificationThresholds    `json:"thresholds"`
	ReferenceMachine    ReferenceMachine           `json:"reference_machine"`
}

type ArmPin struct {
	Label                 string `json:"label"`
	EmbedderID            string `json:"embedder_id,omitempty"`
	FingerprintCanonical string `json:"fingerprint_canonical,omitempty"`
	ManifestSHA256        string `json:"manifest_sha256,omitempty"`
	AdmissionSHA256       string `json:"admission_sha256,omitempty"`
}

type QualificationThresholds struct {
	MinPasses                    int     `json:"min_passes"`
	MinPairedGain                int     `json:"min_paired_gain"`
	MinWeakStrataWithPositiveGain int    `json:"min_weak_strata_with_positive_gain"`
	BootstrapConfidence          float64 `json:"bootstrap_confidence"`
	MaxSidecarRSSBytes           int64   `json:"max_sidecar_rss_bytes"`
	MaxArtifactBytes             int64   `json:"max_artifact_bytes"`
	MaxQueryP95Millis            int64   `json:"max_query_p95_millis"`
	MinQuerySamples              int     `json:"min_query_samples"`
	MaxReindexSeconds            int64   `json:"max_reindex_seconds"`
}

type ReferenceMachine struct {
	OS             string `json:"os"`
	OSVersion      string `json:"os_version"`
	CPU            string `json:"cpu"`
	PhysicalCores  int    `json:"physical_cores"`
	RuntimeThreads int    `json:"runtime_threads"`
	BackgroundLoad string `json:"background_load"`
}
```

Use separate fields/tags in real code rather than grouped declarations so JSON names are explicit. `QualificationThresholds` must literally pin `Passes=56`, `PairedGain=9`, `WeakStrataWithPositiveGain=2`, `BootstrapConfidence=0.95`, `MaxSidecarRSSBytes=2<<30`, `MaxArtifactBytes=1<<30`, `MaxQueryP95=1s`, `MinQuerySamples=100`, and `MaxReindex=10m`.

- [ ] **Step 3: Implement strict preregistration validation**

Require all four arms exactly once; `M0` has no embedding fingerprint, while `M1`–`M3` require full fingerprints and manifest/profile digests. Require `compact/17`, 1,200 tokens, 100,000 bootstrap samples, a non-zero seed, non-empty OS/CPU/core/thread/background-load fields, pinned reader/grader prompt digests, and the exact thresholds above. Reject unknown arms and extra experiment variants.

- [ ] **Step 4: Run tests and commit**

Run: `gofmt -w internal/eval/retrieval/model_qualification*.go && go test ./internal/eval/retrieval -run 'QualificationDataset|QualificationPreregistration'`

Expected: PASS.

```bash
git add internal/eval/retrieval/model_qualification.go internal/eval/retrieval/model_qualification_test.go
git commit -m "feat(eval): freeze embedded-model qualification contract"
```

## Task 8: Capture all four arms, stage boundaries, and byte reproducibility

**Files:**
- Create: `internal/eval/retrieval/model_qualification_capture_test.go`
- Modify: `internal/eval/retrieval/model_qualification.go`
- Modify: `internal/eval/retrieval/blindeval_capture.go`
- Modify: `internal/eval/retrieval/blindeval_capture_test.go`

**Interfaces:**
- Consumes: Task 4 CodeRank constructor, Task 6 evaluation Potion/index seam, Task 7 preregistration, and existing MCP capture.
- Produces: `QualificationObservation`, `QualificationBuildDigest`, injected-embedder capture options, and environment-gated `TestEmbeddedModelQualificationCapture`.

- [ ] **Step 1: Add a failing test that injected capture rejects any invalid semantic run**

```go
func TestQualificationCaptureRejectsFingerprintOrDegradation(t *testing.T) {
	for _, mutate := range []func(*qualificationCaptureFixture){
		func(f *qualificationCaptureFixture) { f.semanticState = embed.StateStale },
		func(f *qualificationCaptureFixture) { f.indexFingerprint.ModelID = "wrong" },
		func(f *qualificationCaptureFixture) { f.degradation = engineretrieval.StateLexicalOnly },
	} {
		f := validQualificationCaptureFixture(t); mutate(f)
		if _, err := f.capture(); err == nil { t.Fatal("invalid run was captured") }
	}
}
```

Add a positive case where lexical rows/backfill appear in a `StateReady` semantic-first result; it must remain valid and must not be labeled sidecar degradation.

- [ ] **Step 2: Extend capture options with an optional injected embedder**

```go
type CandidateCaptureOptions struct {
	// existing fields remain unchanged
	Embedder embed.Embedder
	ExpectedFingerprint *embed.Fingerprint
}
```

When `Embedder` is non-nil, call `buildTaskContextIndexWithEmbedder`; otherwise preserve the existing selector path. For strict qualification capture, compare the loaded generation, search requested fingerprint, retrieval summary model/index fingerprints, and preregistered expected fingerprint canonically before accepting the first payload byte.

- [ ] **Step 3: Define exact stage observations and reproducibility digests**

```go
type QualificationObservation struct {
	Arm                  QualificationArm `json:"arm"`
	QueryID              string           `json:"query_id"`
	Stratum              string           `json:"stratum"`
	SemanticTop50        StageHit         `json:"semantic_top_50"`
	PostFusion           StageHit         `json:"post_fusion"`
	CompleteGrade3Span   bool             `json:"complete_grade_3_span"`
	BundleSHA256         string           `json:"bundle_sha256"`
	PayloadSHA256        string           `json:"payload_sha256"`
	BundleTokens         int              `json:"bundle_tokens"`
	AdmissionTruncations int              `json:"admission_truncations"`
	UnknownTokens        int              `json:"unknown_tokens"`
	ZeroVectors          int              `json:"zero_vectors"`
	RetrievalState       string           `json:"retrieval_state"`
	ModelFingerprint     string           `json:"model_fingerprint"`
	IndexFingerprint     string           `json:"index_fingerprint"`
	Degraded             bool             `json:"degraded"`
}
type StageHit struct { Present bool `json:"present"`; BestRank int `json:"best_rank"` }
type QualificationBuildDigest struct {
	Arm                 QualificationArm `json:"arm"`
	VectorBytesSHA256   string           `json:"vector_bytes_sha256"`
	PersistedRowsSHA256 string           `json:"persisted_rows_sha256"`
	BundlesSHA256       string           `json:"bundles_sha256"`
	TokenCountsSHA256   string           `json:"token_counts_sha256"`
}
```

Compute semantic top-50 from the semantic service before retrieval fusion; compute post-fusion rank from `engine.Retrieve(...Limit:50)`; compute complete grade-3 coverage from exact serialized source spans using `SpanMatches` plus line-by-line coverage of the full judged interval. Never use qrels to alter either candidate list or normal bundle.

- [ ] **Step 4: Add the environment-gated two-build, four-arm driver**

```go
func TestEmbeddedModelQualificationCapture(t *testing.T) {
	root := requireEnv(t, "GRAPHI_QUALIFICATION_REPO")
	datasetPath := requireEnv(t, "GRAPHI_QUALIFICATION_DATASET")
	preregPath := requireEnv(t, "GRAPHI_QUALIFICATION_PREREGISTRATION")
	manifestPath := requireEnv(t, "GRAPHI_CODERANK_MANIFEST")
	out := requireEmptyOutputDir(t, "GRAPHI_QUALIFICATION_OUT")
	loaded := mustLoadQualificationDataset(t, datasetPath)
	prereg := mustLoadQualificationPreregistration(t, preregPath)
	for _, build := range []int{1, 2} {
		for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
			captureQualificationArm(t, root, out, build, arm, loaded, prereg, manifestPath)
		}
	}
	compareQualificationBuilds(t, out, prereg)
}
```

Construct M1 with `static.New`, M2 with `static.NewForEvaluation(..., 8192)`, and M3 with `coderank.NewFromManifest`. M0 explicitly runs lexical-only as a negative control and is exempt only from semantic `StateReady`; it must still have no error, exact retrieval version, exact bundle budget, and no unrecorded degradation. Run arms in the preregistered order, never in an order chosen after seeing output.

- [ ] **Step 5: Enforce byte reproducibility across independent builds**

Sort persisted rows by node ID and hash canonical fields plus raw IEEE-754 vector bytes. Separately hash admitted document records, query vectors, final MCP payload bytes, payload digests, and token counts. Fail the whole run if any M1–M3 pair differs. Record graph generation IDs separately but exclude their intentionally unique staging IDs from comparison.

- [ ] **Step 6: Run non-live tests and commit**

Run: `gofmt -w internal/eval/retrieval/*.go && go test ./internal/eval/retrieval -run 'QualificationCapture|CandidateCapture'`

Expected: PASS; the live qualification test SKIPs unless every required environment variable is set.

```bash
git add internal/eval/retrieval/model_qualification_capture_test.go internal/eval/retrieval/model_qualification.go internal/eval/retrieval/blindeval_capture.go internal/eval/retrieval/blindeval_capture_test.go
git commit -m "feat(eval): capture reproducible four-arm qualification"
```

## Task 9: Implement the three qrel-only stage-ceiling controls

**Files:**
- Create: `internal/eval/retrieval/model_qualification_oracle.go`
- Create: `internal/eval/retrieval/model_qualification_oracle_test.go`

**Interfaces:**
- Consumes: frozen normal candidate rows, dataset qrels, existing `SpanMatches`, compact serializer, real tokenizer counter, and 1,200-token budget.
- Produces: `BuildOracleControls(OracleInput) (OracleControls, error)` with current-candidate/oracle-packer, oracle-candidate/current-selector, and oracle-candidate/oracle-packer bundles.

- [ ] **Step 1: Write failing tests for the three distinct ceilings and leakage barrier**

```go
func TestBuildOracleControlsKeepsNormalCaptureImmutable(t *testing.T) {
	in := oracleFixture(t)
	before := canonicalRows(t, in.CurrentCandidates)
	got, err := BuildOracleControls(in)
	if err != nil { t.Fatal(err) }
	if canonicalRows(t, in.CurrentCandidates) != before { t.Fatal("oracle mutated normal candidates") }
	if !got.CurrentCandidatesOraclePacker.CompleteGrade3Span { t.Fatal("oracle packer missed available span") }
	if !got.OracleCandidateCurrentSelector.Injected { t.Fatal("oracle candidate was not marked") }
	if got.OracleCandidateOraclePacker.TokenCount > SavingsCandidateBudget { t.Fatal("oracle exceeded budget") }
}
```

Add a fixture where the relevant candidate is absent, one where it is present but the selector drops it, and one where even the shortest exact grade-3 source exceeds the fixed budget.

- [ ] **Step 2: Run the focused test and confirm missing implementation**

Run: `go test ./internal/eval/retrieval -run 'OracleControls'`

Expected: FAIL because `BuildOracleControls` is undefined.

- [ ] **Step 3: Implement controls in the eval package only**

```go
type OracleControls struct {
	CurrentCandidatesOraclePacker OracleBundle `json:"current_candidates_oracle_packer"`
	OracleCandidateCurrentSelector OracleBundle `json:"oracle_candidate_current_selector"`
	OracleCandidateOraclePacker OracleBundle `json:"oracle_candidate_oracle_packer"`
}
```

The oracle packer may inspect qrels only after normal candidates are frozen; it selects the smallest complete grade-3 spans first, with deterministic path/start/end tie breaks, and stops before 1,200 real tokenizer tokens. Oracle candidate injection adds exact judged source spans to a copy of the candidate list, marks every injected row, then calls the unchanged current selector. Never export oracle rows under the normal capture filename or candidate provenance.

- [ ] **Step 4: Serialize controls for the same blind grader and commit**

Each oracle payload must use a distinct `control_kind`, retain query IDs but omit qrels and answer labels, and satisfy the same MCP payload validator and blind grading packet format as normal bundles.

Run: `gofmt -w internal/eval/retrieval/model_qualification_oracle*.go && go test ./internal/eval/retrieval -run 'OracleControls'`

Expected: PASS.

```bash
git add internal/eval/retrieval/model_qualification_oracle.go internal/eval/retrieval/model_qualification_oracle_test.go
git commit -m "feat(eval): add retrieval stage-ceiling controls"
```

## Task 10: Compute deterministic paired statistics and the indivisible promotion gate

**Files:**
- Create: `internal/eval/retrieval/model_qualification_stats.go`
- Create: `internal/eval/retrieval/model_qualification_stats_test.go`

**Interfaces:**
- Consumes: Task 7 preregistration, Task 8 observations/build digests, imported blind grading decisions, and operating measurements.
- Produces: `EvaluateQualification(QualificationInput) (QualificationDecision, error)` and deterministic `PairedBootstrap95([]PairedOutcome, uint64, int) Interval`.

Define the paired inputs and interval exactly as:

```go
type PairedOutcome struct {
	QueryID string `json:"query_id"`
	M1Pass  bool   `json:"m1_pass"`
	M3Pass  bool   `json:"m3_pass"`
}

type Interval struct {
	Point float64 `json:"point"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type QualificationInput struct {
	Preregistration QualificationPreregistration `json:"preregistration"`
	Observations    []QualificationObservation   `json:"observations"`
	BuildDigests    []QualificationBuildDigest   `json:"build_digests"`
	Grades          map[QualificationArm][]Grade `json:"grades"`
	OracleControls  []OracleControls              `json:"oracle_controls"`
	Operating       OperatingMeasurements         `json:"operating_budget"`
}
```

- [ ] **Step 1: Write failing bootstrap and gate-table tests**

```go
func TestPairedBootstrap95IsDeterministic(t *testing.T) {
	outcomes := pairedFixture(64, 12, 2) // 12 M3 wins, 2 M1 wins, net +10
	a := PairedBootstrap95(outcomes, 0x475241504849, 100000)
	b := PairedBootstrap95(outcomes, 0x475241504849, 100000)
	if a != b || a.Lower <= 0 { t.Fatalf("a=%+v b=%+v", a, b) }
}

func TestEvaluateQualificationRequiresEveryGate(t *testing.T) {
	valid := passingQualificationInput(t)
	if got, err := EvaluateQualification(valid); err != nil || !got.Promote { t.Fatalf("%+v %v", got, err) }
	for _, breakOne := range qualificationGateMutations() {
		in := valid; breakOne.Apply(&in)
		got, err := EvaluateQualification(in)
		if err != nil { t.Fatal(err) }
		if got.Promote { t.Fatalf("gate %s was bypassed", breakOne.Name) }
	}
}
```

Gate mutations must independently break: 56 passes, +9 paired gain, positive CI, two weak strata, no strong-stratum loss, serialized-span transfer, two-build byte equality, each of four operating limits, CPU-only mode, `StateReady`, full fingerprint equality, and no degradation.

- [ ] **Step 2: Run focused tests and confirm missing statistics**

Run: `go test ./internal/eval/retrieval -run 'PairedBootstrap|EvaluateQualification'`

Expected: FAIL because the statistics functions are undefined.

- [ ] **Step 3: Implement a repository-owned stable PRNG and percentile interval**

```go
type splitMix64 struct{ state uint64 }
func (r *splitMix64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
```

For each of exactly 100,000 resamples, draw 64 paired query indices with replacement and store the mean `M3-M1` pass delta. Sort ascending; use indices `floor(0.025*n)` and `ceil(0.975*n)-1`. Record the seed, sample count, algorithm ID `splitmix64-percentile-paired-v1`, point estimate, and bounds. Do not depend on `math/rand` algorithm stability.

- [ ] **Step 4: Implement all gates as named records and combine with logical AND**

```go
type GateResult struct { Name string `json:"name"`; Passed bool `json:"passed"`; Observed string `json:"observed"`; Required string `json:"required"` }
type QualificationDecision struct { Promote bool `json:"promote"`; Gates []GateResult `json:"gates"`; Branch string `json:"branch"` }
```

`Promote` is true only when every gate record passes. The three strong strata are `config_docs`, `exact_identifier`, and `exact_path`; the three weak strata are `ambiguous`, `architecture_flow`, and `nl_behaviour`. Require at least one complete-grade-3-span net gain and at least nine final paired pass gains, so ranking-only gains cannot qualify.

Return one of these exact branches: `promote_coderank_to_product_integration`, `investigate_projection_or_fusion`, `design_potion_admission_candidate`, `prefer_simpler_potion_candidate`, `representation_or_budget_ceiling`, or `stop_no_new_holdout`, using the decision order in the spec.

- [ ] **Step 5: Implement operating-budget validation**

```go
type OperatingMeasurements struct {
	CPUOnly                      bool            `json:"cpu_only"`
	ArtifactBytes                 int64           `json:"artifact_bytes"`
	PeakAdditionalSidecarRSSBytes int64           `json:"peak_additional_sidecar_rss_bytes"`
	QueryEmbedLatencies           []time.Duration `json:"query_embed_latencies"`
	FullReindex                   time.Duration   `json:"full_reindex"`
}
```

Require at least 100 post-warm-up query latencies, sort a copy, and define p95 as element `ceil(0.95*n)-1`. Compare against `1s`; compare artifact bytes to `1<<30`, RSS to `2<<30`, and reindex to `10m`. Reject missing samples or zero-valued measurements rather than treating them as passes.

- [ ] **Step 6: Run tests and commit**

Run: `gofmt -w internal/eval/retrieval/model_qualification_stats*.go && go test ./internal/eval/retrieval -run 'PairedBootstrap|EvaluateQualification|Operating'`

Expected: PASS.

```bash
git add internal/eval/retrieval/model_qualification_stats.go internal/eval/retrieval/model_qualification_stats_test.go
git commit -m "feat(eval): enforce coderank promotion gates"
```

## Task 11: Emit an auditable qualification report and execute the frozen run

**Files:**
- Create: `internal/eval/retrieval/model_qualification_report.go`
- Create: `internal/eval/retrieval/model_qualification_report_test.go`
- Create at execution time: `docs/eval/retrieval/drafts/<run-id>/preregistration.json`
- Create at execution time: `docs/eval/retrieval/drafts/<run-id>/qualification.json`
- Create at execution time: `docs/eval/retrieval/drafts/<run-id>/qualification.md`
- Create at execution time: `docs/eval/retrieval/drafts/<run-id>/raw/` content-addressed captures and blind grading packets.

**Interfaces:**
- Consumes: all prior qualification artifacts and existing candidate/checkout binding records.
- Produces: `WriteQualificationReport(string, QualificationReport) error`, a canonical machine decision, and a human-readable evidence table.

The report root is a closed struct, not a free-form map:

```go
type QualificationReport struct {
	SchemaVersion   int                          `json:"schema_version"`
	Preregistration QualificationPreregistration `json:"preregistration"`
	Observations    []QualificationObservation   `json:"observations"`
	BuildDigests    []QualificationBuildDigest   `json:"build_digests"`
	OracleControls  []OracleControls              `json:"oracle_controls"`
	Operating       OperatingMeasurements         `json:"operating_budget"`
	Decision        QualificationDecision         `json:"decision"`
}
```

- [ ] **Step 1: Write a failing golden report test**

```go
func TestWriteQualificationReportContainsEveryReleaseRelevantGate(t *testing.T) {
	dir := t.TempDir()
	report := completeQualificationReportFixture(t)
	if err := WriteQualificationReport(dir, report); err != nil { t.Fatal(err) }
	raw := mustRead(t, filepath.Join(dir, "qualification.json"))
	for _, required := range []string{
		`"candidate_sha"`, `"dataset_sha256"`, `"M3_coderank"`,
		`"paired_bootstrap_95"`, `"stratum_deltas"`, `"reproducibility"`,
		`"operating_budget"`, `"run_validity"`, `"promote"`,
	} {
		if !bytes.Contains(raw, []byte(required)) { t.Fatalf("missing %s", required) }
	}
}
```

- [ ] **Step 2: Implement canonical JSON plus derived Markdown**

Write JSON with stable struct field order, two-space indentation, and a trailing newline. The Markdown must show arm totals, M3–M1 paired wins/losses/net, CI, all six stratum deltas, stage retention, both build digests, all operating measurements, all run-validity checks, all oracle ceilings, and every named gate. End with `DEVELOPMENT PROMOTION: YES|NO`; never write `RELEASE: YES`, because this plan does not authorize release.

- [ ] **Step 3: Run report tests and the full non-live verification suite**

Run: `gofmt -w internal/eval/retrieval/model_qualification_report*.go && go test ./internal/eval/retrieval -run 'QualificationReport'`

Expected: PASS.

Run: `CGO_ENABLED=0 go test ./...`

Expected: PASS with live model/evaluation tests skipped when opt-in environment is absent.

Run: `go vet ./... && test -z "$(gofmt -l engine internal cmd)" && CGO_ENABLED=0 go build ./cmd/graphi`

Expected: all commands exit 0 and `gofmt -l` prints nothing.

- [ ] **Step 4: Freeze the independent dev population and preregistration before starting the sidecar**

The curator supplies a new dataset satisfying Task 7. The operator records its exact SHA-256, source checkout, candidate binding, four arm pins, CodeRank manifest digest, grader/reader prompt digests, stable bootstrap seed, and reference-machine fields in `preregistration.json`. Validate it before any capture:

```bash
go test ./internal/eval/retrieval -run TestEmbeddedModelQualificationCapture -count=1
```

Expected before all environment variables are supplied: SKIP. Expected with an invalid dataset/preregistration: FAIL before constructing any embedder or writing a payload.

- [ ] **Step 5: Verify and start the pinned local sidecar, then run capture exactly once**

```bash
python3 scripts/eval/coderank_sidecar.py verify --manifest "$GRAPHI_CODERANK_MANIFEST" --model-dir "$GRAPHI_CODERANK_MODEL_DIR"
python3 scripts/eval/coderank_sidecar.py serve --manifest "$GRAPHI_CODERANK_MANIFEST" --model-dir "$GRAPHI_CODERANK_MODEL_DIR" --bind 127.0.0.1 --port 8765
CGO_ENABLED=0 go test ./internal/eval/retrieval -run '^TestEmbeddedModelQualificationCapture$' -count=1 -v
```

The execution environment must set `GRAPHI_QUALIFICATION_REPO`, `GRAPHI_QUALIFICATION_DATASET`, `GRAPHI_QUALIFICATION_PREREGISTRATION`, `GRAPHI_QUALIFICATION_OUT`, `GRAPHI_CODERANK_MANIFEST`, and the already existing static-model cache variable. The output directory must be new and empty. Any restart, attestation mismatch, degraded query, fingerprint mismatch, duplicate run, or partial output invalidates the run; create a new run ID rather than overwriting evidence.

- [ ] **Step 6: Blind-grade normal and oracle packets, import decisions, and finalize**

Use the repository's existing blind reader/grader packet workflow without showing arm labels, qrels, or paired results to graders. Import exactly one final decision per `(arm, query_id)` and each oracle control, reject duplicates/missing IDs, then run the report finalizer. The finalizer must refuse to run until 64 decisions exist for each required arm and all validity/reproducibility/operating measurements are present.

- [ ] **Step 7: Apply the precommitted branch without spending a holdout**

- If the report says `promote_coderank_to_product_integration`, write a new plan for public selector integration, operator UX, default-build/no-dial canaries, and byte-for-byte production parity.
- If it says `design_potion_admission_candidate`, write a separate Potion candidate spec; M2 itself is not a release candidate.
- If it says `investigate_projection_or_fusion`, use only the frozen dev measurements to repair that stage.
- If it says `representation_or_budget_ceiling`, stop model work and open a separately governed 1,200-token/representation design.
- If it says `stop_no_new_holdout`, make no release attempt.
- Commission a new independent holdout only after the selected product candidate passes production parity. Its decision remains `RELEASE: YES` only at `>=56/64` and only when every preregistered stratum, reproducibility, operating-budget, and run-validity gate passes.

- [ ] **Step 8: Commit the reporting machinery; commit run evidence separately after independent review**

```bash
git add internal/eval/retrieval/model_qualification_report.go internal/eval/retrieval/model_qualification_report_test.go
git commit -m "feat(eval): report embedded-model qualification"
```

After the actual run is independently reviewed, stage only the explicit run directory and commit it with a run-specific evidence message. Do not stage other untracked research/session directories.

## Plan Completion Boundary

This plan is complete when the repository can produce an auditable `DEVELOPMENT PROMOTION: YES|NO` from a frozen 64-query dev population and all non-live checks pass. It intentionally does not claim the release is fixed and does not authorize another holdout. A CodeRank development win unlocks a second plan for product integration/parity; only that parity result can unlock a third plan for a newly curated holdout and the final compound release gate.
