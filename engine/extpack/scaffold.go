package extpack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"
)

// SW-230 (AX-10) — `graphi extension init`, the offline pack scaffold.
//
// # What it produces and why that exact set
//
// Four files, and each closes one way an author's first pack fails:
//
//	pack.yaml      the manifest, complete and already pinned. The scaffold hashes
//	               the artifact it just rendered and writes that hash in, so the
//	               output is `validate`-clean on the first run rather than after
//	               the author discovers they have to compute a SHA-256 by hand.
//	<artifact>     one real, minimal, schema-valid rule for the chosen kind —
//	               not an empty list, because the commonest first mistake is a
//	               manifest whose capabilities.provides and artifact disagree, and
//	               an example that already agrees shows the shape of the contract.
//	pack_test.go   the conformance test, three lines, using the harness the host
//	               proves ITS OWN contributions with. A pack an author cannot test
//	               is a pack they can only find out about in production.
//	README.md      the commands, in order, plus the one sentence about what a pack
//	               is allowed to be.
//
// # Deterministic by construction
//
// Rendering is a pure function of ScaffoldOptions: fixed templates, sorted
// capability keys, no clock, no randomness, no host paths. Scaffold returns
// bytes; ScaffoldInto is the only part that touches a filesystem. That split is
// what lets the output be golden-tested byte for byte — a template that reformats
// itself between releases would silently change what every new pack looks like.
//
// # Offline, and not by policy
//
// There is no template registry, no download and no cache. The templates are Go
// string constants compiled into the binary. `init` cannot reach the network
// because there is nothing in it that could.

// ScaffoldVersion is the pack version `init` writes. A scaffolded pack is
// pre-1.0 on purpose: it is a starting point, and a fresh pack claiming 1.0.0
// would be the scaffold making a stability claim on the author's behalf.
const ScaffoldVersion = "0.1.0"

// ScaffoldLimit is the limits.max_output_bytes a scaffolded pack declares. It is
// far above the stub artifact and far below MaxArtifactBytes, so an author can
// grow the pack without touching the field and still cannot declare their way
// past the host ceiling.
const ScaffoldLimit = 64 << 10

// ScaffoldOptions parameterises the scaffold.
type ScaffoldOptions struct {
	// Kind is the pack kind. Empty defaults to KindArchitectureRules — the kind
	// whose artifact schema is smallest, so a first pack is a shorter read.
	Kind Kind
	// ID is the pack id. Empty defaults to the kind's example id.
	ID string
}

// ScaffoldFile is one rendered file: a bare name, its bytes, and the mode it is
// written with.
type ScaffoldFile struct {
	Name string
	Data []byte
}

// scaffoldKind holds one kind's template data.
type scaffoldKind struct {
	defaultID    string
	artifactName string
	artifact     string
	provides     []string
	summary      string
}

var scaffoldKinds = map[Kind]scaffoldKind{
	KindArchitectureRules: {
		defaultID:    "example.arch-rules",
		artifactName: "rules.yaml",
		artifact:     scaffoldArchArtifact,
		provides:     []string{NSArchitectureRule + ":ui-must-not-reach-storage"},
		summary:      "one forbidden dependency direction between two architecture units",
	},
	KindTaintRules: {
		defaultID:    "example.taint-rules",
		artifactName: "taint.yaml",
		artifact:     scaffoldTaintArtifact,
		provides: []string{
			NSTaintSanitizer + ":example-shell-quote",
			NSTaintSink + ":example-shell-exec",
			NSTaintSource + ":example-request-body",
		},
		summary: "one taint source, one sink and one sanitizer",
	},
}

// ScaffoldKinds lists the kinds `init` can scaffold, in canonical order.
func ScaffoldKinds() []Kind { return SupportedKinds() }

// Scaffold renders a complete, immediately-valid pack as bytes.
//
// It performs no I/O at all, which is what makes it testable as a pure
// byte-for-byte golden and what makes "init is offline" a property of the code
// rather than a claim in the help text.
func Scaffold(opts ScaffoldOptions) ([]ScaffoldFile, error) {
	kind := opts.Kind
	if kind == "" {
		kind = KindArchitectureRules
	}
	spec, ok := scaffoldKinds[kind]
	if !ok {
		return nil, fmt.Errorf("extpack: cannot scaffold pack kind %q: this build scaffolds %s",
			Bound(string(kind)), kindList())
	}
	id := opts.ID
	if id == "" {
		id = spec.defaultID
	}
	if err := ValidateID(id); err != nil {
		return nil, err
	}

	artifact := []byte(spec.artifact)
	provides := append([]string(nil), spec.provides...)
	sort.Strings(provides)

	manifest, err := renderTemplate("pack.yaml", scaffoldManifest, map[string]any{
		"SchemaVersion": SchemaVersion,
		"ID":            id,
		"Version":       ScaffoldVersion,
		"Kind":          string(kind),
		"API":           APIVersion,
		"ArtifactName":  spec.artifactName,
		"ArtifactHash":  HashBytes(artifact),
		"Provides":      provides,
		"Permission":    string(PermissionGraphRead),
		"Determinism":   string(DeterminismDeterministic),
		"Limit":         ScaffoldLimit,
	})
	if err != nil {
		return nil, err
	}
	readme, err := renderTemplate("README.md", scaffoldReadme, map[string]any{
		"ID":            id,
		"Kind":          string(kind),
		"ArtifactName":  spec.artifactName,
		"ManifestName":  PackManifestName,
		"Summary":       spec.summary,
		"SchemaVersion": SchemaVersion,
	})
	if err != nil {
		return nil, err
	}
	test, err := renderTemplate("pack_test.go", scaffoldTest, map[string]any{"ID": id})
	if err != nil {
		return nil, err
	}

	// Canonical file order: the manifest first because it is what every verb is
	// pointed at, then the artifact it pins, then the test, then the prose.
	return []ScaffoldFile{
		{Name: PackManifestName, Data: manifest},
		{Name: spec.artifactName, Data: artifact},
		{Name: "pack_test.go", Data: test},
		{Name: "README.md", Data: readme},
	}, nil
}

// ScaffoldInto renders a pack and writes it into dir, creating dir if needed.
//
// It REFUSES to overwrite an existing file. `init` is a scaffold, not a reset:
// silently replacing a manifest an author has been editing would destroy work no
// version control necessarily holds yet.
func ScaffoldInto(dir string, opts ScaffoldOptions) ([]string, error) {
	files, err := Scaffold(opts)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if _, statErr := os.Stat(filepath.Join(dir, f.Name)); statErr == nil {
			return nil, fmt.Errorf("extpack: %s already exists: `extension init` never overwrites, "+
				"scaffold into an empty directory", filepath.Join(dir, f.Name))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("extpack: create %s: %w", dir, err)
	}
	written := make([]string, 0, len(files))
	for _, f := range files {
		path := filepath.Join(dir, f.Name)
		if err := os.WriteFile(path, f.Data, 0o644); err != nil {
			return nil, fmt.Errorf("extpack: write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

func renderTemplate(name, text string, data any) ([]byte, error) {
	tpl, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("extpack: scaffold template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("extpack: render %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

const scaffoldManifest = `# graphi extension pack manifest — schema {{.SchemaVersion}}
#
# A pack is DATA. graphi compiles, links, evaluates and executes nothing a pack
# ships, and follows no file path or URL a pack names. Everything below is
# checked before a byte is installed: run ` + "`graphi extension lint .`" + ` to see
# every problem at once, with line numbers.
schema_version: {{.SchemaVersion}}

# The pack id doubles as its directory name in the pack store, so the grammar is
# dot-separated [a-z0-9-] segments starting and ending alphanumeric.
id: {{.ID}}
version: "{{.Version}}"
kind: {{.Kind}}

# The closed range of host rule-pack API versions this pack was written for.
# This graphi speaks {{.API}}; a pack outside the range is refused, not guessed at.
api:
  min: "{{.API}}"
  max: "{{.API}}"

# The data file, pinned. artifact.path is a BARE FILE NAME next to this manifest.
# Re-hash it after every edit:  shasum -a 256 {{.ArtifactName}}
artifact:
  path: {{.ArtifactName}}
  sha256: {{.ArtifactHash}}

# Every capability key the artifact defines — no more, no less. graphi checks
# this against the artifact in both directions, so the field cannot become
# decoration.
capabilities:
  provides:
{{- range .Provides}}
    - {{.}}
{{- end}}

# {{.Permission}} is the only permission a declarative pack can hold. There is no
# network, filesystem or exec permission to ask for (ADR 0013).
permissions:
  - {{.Permission}}

determinism: {{.Determinism}}

limits:
  max_output_bytes: {{.Limit}}
`

const scaffoldArchArtifact = `# graphi architecture-rules artifact.
#
# Each rule forbids ONE dependency direction. ` + "`from`" + ` and ` + "`to`" + ` are matched
# against an architecture unit's label, which in graphi's community view is the
# unit's dominant path prefix.
version: "1"
rules:
  - id: ui-must-not-reach-storage
    from: ui
    to: storage
    description: The UI layer must reach storage through the service layer, never directly.
`

const scaffoldTaintArtifact = `# graphi taint-rules artifact.
#
# Sources introduce a label, sinks are where a labelled value must not arrive,
# and sanitizers remove the labels they name. A sanitizer must name its labels:
# a pack may not ship one that strips everything.
version: "1"
sources:
  - id: example-request-body
    label: user-input
    node_kinds:
      - function
    name_patterns:
      - ReadRequestBody
sinks:
  - id: example-shell-exec
    category: command-injection
    name_patterns:
      - RunShellCommand
sanitizers:
  - id: example-shell-quote
    name_patterns:
      - ShellQuote
    remove_labels:
      - user-input
`

const scaffoldTest = `// Contract test for the {{.ID}} rule pack.
//
// It runs graphi's own conformance harness — the same one graphi proves its
// built-in contributions with. Add this package to a Go module that requires
// github.com/samibel/graphi and run ` + "`go test ./...`" + `.
package pack_test

import (
	"testing"

	"github.com/samibel/graphi/engine/extpack/conformance"
)

func TestPackPassesTheGraphiConformanceHarness(t *testing.T) {
	if err := conformance.VerifyPack(".").Err(); err != nil {
		t.Fatalf("{{.ID}} is not conformant:\n%v", err)
	}
}
`

const scaffoldReadme = `# {{.ID}}

A graphi rule pack of kind ` + "`{{.Kind}}`" + `, scaffolded by ` + "`graphi extension init`" + `.
It declares {{.Summary}} — edit ` + "`{{.ArtifactName}}`" + ` to make it yours.

**A pack is data.** graphi executes nothing this pack ships and follows no path
or URL it names. The only permission a declarative pack can hold is
` + "`graph:read`" + `, and the schema cannot express any other.

## The loop

` + "```" + `console
# 1. Edit the artifact.
$ $EDITOR {{.ArtifactName}}

# 2. Re-pin it — every edit changes the hash the manifest carries.
$ shasum -a 256 {{.ArtifactName}}

# 3. See every problem at once, with line numbers.
$ graphi extension lint .

# 4. Prove the contract: schema, api compatibility, deterministic merge,
#    provenance on every merged rule.
$ graphi extension conform .

# 5. Check what would install, and print the hash to pin it with.
$ graphi extension validate {{.ManifestName}}

# 6. Install into a repository. Offline, and --sha256 is mandatory.
$ graphi extension install --sha256 <hash-from-step-5> {{.ManifestName}}
` + "```" + `

` + "`graphi extension disable {{.ID}}`" + ` restores byte-identical pre-pack
behaviour; ` + "`graphi extension remove {{.ID}}`" + ` deletes it.

## Files

| file | what it is |
|---|---|
| ` + "`{{.ManifestName}}`" + ` | the manifest, schema ` + "`{{.SchemaVersion}}`" + ` |
| ` + "`{{.ArtifactName}}`" + ` | the data this pack contributes |
| ` + "`pack_test.go`" + ` | the conformance harness, as a Go test |
| ` + "`README.md`" + ` | this file |
`
