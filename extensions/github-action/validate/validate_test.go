package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoActionDir resolves the directory holding the real action.yml + entrypoint
// relative to this test file (extensions/github-action/validate -> ..).
func repoActionDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd) // .../extensions/github-action
}

// TestRealActionYMLSatisfiesContract is the AC1/AC3 gate: the shipped action.yml
// documents every required input (type/default/required) and output, and uses a
// pinned composite runtime with full-SHA-pinned `uses` steps.
func TestRealActionYMLSatisfiesContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoActionDir(t), "action.yml"))
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}
	res := ValidateActionYML(string(b))
	if len(res.Errors) != 0 {
		t.Fatalf("action.yml contract violations:\n  - %s", strings.Join(res.Errors, "\n  - "))
	}
	// Spot-check the parsed shape so a future silent regression is caught.
	if res.Using != "composite" {
		t.Fatalf("runs.using = %q, want composite", res.Using)
	}
	tok, ok := res.Inputs["github-token"]
	if !ok || !tok.Required {
		t.Fatalf("github-token must be a required input; got %+v (ok=%v)", tok, ok)
	}
	if tok.HasDefault {
		t.Fatalf("github-token (a secret) must NOT carry a default")
	}
	if len(res.UsesRefs) < 2 {
		t.Fatalf("expected at least 2 pinned `uses` steps, got %d", len(res.UsesRefs))
	}

	// The consumer checkout is the working tree being reviewed. The engine must
	// be built from the independently pinned Action checkout, otherwise a
	// consumer can replace ./cmd/graphi with arbitrary code executed under the
	// Action token.
	action := string(b)
	if !strings.Contains(action, `GITHUB_ACTION_PATH}/../..`) {
		t.Fatal("engine build must resolve source from GITHUB_ACTION_PATH")
	}
	if strings.Contains(action, `go build -o "${RUNNER_TEMP:-/tmp}/graphi" ./cmd/graphi`) {
		t.Fatal("engine build must not resolve ./cmd/graphi from the consumer workspace")
	}
	if !strings.Contains(action, `go -C "${GRAPHI_SOURCE_ROOT}" build`) {
		t.Fatal("engine build must execute from the pinned Action source root")
	}
	if !strings.Contains(action, `go-version: "1.26.5"`) {
		t.Fatal("shipped Action must use the CVE-fixed Go 1.26.5 toolchain, not a floating or vulnerable 1.26.x selector")
	}
}

// TestRealEntrypointSatisfiesContract is the AC1/AC2/S1 gate: the shipped
// entrypoint drives the real subcommands in order, requests a real publish, and
// never puts the token on argv.
func TestRealEntrypointSatisfiesContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoActionDir(t), "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read entrypoint.sh: %v", err)
	}
	if errs := ValidateEntrypoint(string(b)); len(errs) != 0 {
		t.Fatalf("entrypoint contract violations:\n  - %s", strings.Join(errs, "\n  - "))
	}
}

// TestUnpinnedUsesIsRejected proves the validator FAILS on a floating tag ref.
func TestUnpinnedUsesIsRejected(t *testing.T) {
	yml := `name: x
inputs:
  github-token:
    type: string
    required: true
  base-ref:
    type: string
    required: false
    default: ""
  head-ref:
    type: string
    required: false
    default: ""
  merge-gate:
    type: boolean
    required: false
    default: false
  gate-threshold:
    type: number
    required: false
    default: 700
  comment-marker:
    type: string
    required: false
    default: "m"
outputs:
  risk-score:
    description: "d"
    value: "${{ steps.x.outputs.risk-score }}"
  gate-status:
    description: "d"
    value: "${{ steps.x.outputs.gate-status }}"
  comment-url:
    description: "d"
    value: "${{ steps.x.outputs.comment-url }}"
runs:
  using: "composite"
  steps:
    - uses: actions/checkout@v4
`
	res := ValidateActionYML(yml)
	if len(res.Errors) == 0 {
		t.Fatal("expected an error for the floating @v4 tag, got none")
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "not pinned by a full 40-hex") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a pinning error, got: %v", res.Errors)
	}
}

// TestUndocumentedInputIsRejected proves the validator FAILS when an input lacks
// type/required documentation.
func TestUndocumentedInputIsRejected(t *testing.T) {
	yml := `name: x
inputs:
  github-token:
    description: "no type, no required"
outputs:
  risk-score:
    description: "d"
    value: "v"
runs:
  using: "composite"
  steps:
    - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683
`
	res := ValidateActionYML(yml)
	if len(res.Errors) == 0 {
		t.Fatal("expected errors for the undocumented input, got none")
	}
}

// TestMissingUsingIsRejected proves a non-composite (or docker-floating) runtime
// is rejected.
func TestMissingUsingIsRejected(t *testing.T) {
	yml := `name: x
runs:
  using: "docker"
  image: "docker://ghcr.io/x/y:latest"
`
	res := ValidateActionYML(yml)
	composite := false
	for _, e := range res.Errors {
		if strings.Contains(e, "pinned composite runtime") {
			composite = true
		}
	}
	if !composite {
		t.Fatalf("expected a composite-runtime error, got: %v", res.Errors)
	}
}

// TestTokenOnArgvIsRejected proves the entrypoint validator FAILS if the token is
// passed as a flag.
func TestTokenOnArgvIsRejected(t *testing.T) {
	script := `#!/usr/bin/env bash
graphi analyze pr-risk -diff-path d
graphi analyze pr-signals -diff-path d
graphi analyze pr-questions -diff-path d
graphi pr-comment -diff-path d -token "$GITHUB_TOKEN" -publish
`
	errs := ValidateEntrypoint(script)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "command line (argv)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a token-on-argv error, got: %v", errs)
	}
}

// TestOutOfOrderSubcommandsRejected proves the ordering check works.
func TestOutOfOrderSubcommandsRejected(t *testing.T) {
	script := `#!/usr/bin/env bash
graphi pr-comment -diff-path d -publish
graphi analyze pr-risk -diff-path d
graphi analyze pr-signals -diff-path d
graphi analyze pr-questions -diff-path d
`
	errs := ValidateEntrypoint(script)
	if len(errs) == 0 {
		t.Fatal("expected an out-of-order error, got none")
	}
}
