// Command gen-packaging renders the Homebrew formula
// (packaging/homebrew/graphi.rb) and the Scoop manifest
// (packaging/scoop/graphi.json) from the single source of truth for the release
// matrix — internal/release.ReleaseTargets + release.AssetName (EP-010 Task H,
// SW-071).
//
// The os/arch→asset mapping is NEVER hand-maintained here: every URL is derived
// from release.AssetName(p) for each release.ReleaseTargets entry, so the
// manifests cannot drift from the budgeted release matrix. The release version
// and per-asset sha256 are injected at release time (-version, -sums); absent a
// sums file each hash is a 64-zero placeholder, which is exactly what the
// committed manifests carry (and what `-check` asserts).
//
// Usage:
//
//	go run ./cmd/gen-packaging                                  # write both manifests (placeholder render)
//	go run ./cmd/gen-packaging -version v1.2.3 -sums SHA256SUMS # release render
//	go run ./cmd/gen-packaging -check                           # exit 1 with a diff if a committed file drifts
//
// The templates are embedded (//go:embed), so the generator is self-contained
// and the render is byte-deterministic (no wall-clock, no rand).
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/samibel/graphi/internal/release"
)

//go:embed templates/graphi.rb.tmpl
var formulaTmpl string

//go:embed templates/graphi.json.tmpl
var scoopTmpl string

const (
	// releaseDownloadBase is the URL base for a published release asset:
	// <base>/<version>/<asset>.
	releaseDownloadBase = "https://github.com/samibel/graphi/releases/download"
	// zeroSHA is the placeholder sha256 used when no -sums file is supplied. It is
	// what the committed manifests carry and what `-check` re-renders against.
	zeroSHA = "0000000000000000000000000000000000000000000000000000000000000000"

	formulaPath = "packaging/homebrew/graphi.rb"
	scoopPath   = "packaging/scoop/graphi.json"
)

// asset is one resolved release asset: its file name, download URL, and sha256.
type asset struct {
	Name string
	URL  string
	SHA  string
}

// tmplData is the injected, deterministic template input. Version is the BARE
// semver ("0.2.0") — the form Homebrew's `version` field and Scoop's
// `version`/`autoupdate` math expect; download URLs are built from the TAG
// form ("v0.2.0") inside render, because that is the path the release assets
// actually live under. ByKey maps an "<os>/<arch>" key to its resolved asset
// so the templates can address each platform explicitly (e.g.
// {{ (index .ByKey "darwin/arm64").URL }}).
type tmplData struct {
	Version string
	ByKey   map[string]asset
}

// renderedFile pairs a committed file name with its freshly-rendered content.
type renderedFile struct {
	name    string
	content string
}

func main() {
	version := flag.String("version", "0.0.0", "release version stamped into the manifests")
	sums := flag.String("sums", "", "optional SHA256SUMS file ('<hex>  <name>' lines) → per-asset hashes")
	check := flag.Bool("check", false, "re-render both manifests with the canonical placeholder (version 0.0.0, no sums) and exit 1 with a diff if a committed file's structure differs")
	flag.Parse()

	root := moduleRoot()

	if *check {
		// Structural drift: re-render with the canonical placeholder (the values
		// the committed manifests carry) and compare. Version/sums injected at
		// release time are deliberately NOT part of the committed structure.
		files, err := render("0.0.0", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-packaging -check: %v\n", err)
			os.Exit(2)
		}
		drift := false
		for _, f := range files {
			path := filepath.Join(root, f.name)
			got, rerr := os.ReadFile(path)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "gen-packaging -check: cannot read %s: %v\n", f.name, rerr)
				drift = true
				continue
			}
			if string(got) != f.content {
				fmt.Fprintf(os.Stderr, "gen-packaging -check: %s is stale — run `go run ./cmd/gen-packaging`\n", f.name)
				fmt.Fprint(os.Stderr, unifiedDiff(f.name, string(got), f.content))
				drift = true
			}
		}
		if drift {
			os.Exit(1)
		}
		return
	}

	var hashes map[string]string
	if *sums != "" {
		h, err := parseSums(*sums)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-packaging: %v\n", err)
			os.Exit(2)
		}
		hashes = h
	}

	files, err := render(*version, hashes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-packaging: %v\n", err)
		os.Exit(2)
	}
	for _, f := range files {
		path := filepath.Join(root, f.name)
		if werr := os.MkdirAll(filepath.Dir(path), 0o755); werr != nil {
			fmt.Fprintf(os.Stderr, "gen-packaging: mkdir %s: %v\n", filepath.Dir(path), werr)
			os.Exit(2)
		}
		if werr := os.WriteFile(path, []byte(f.content), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "gen-packaging: write %s: %v\n", f.name, werr)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "gen-packaging: wrote %s\n", path)
	}
}

// render produces both manifests from the embedded templates + the release
// source of truth. version is accepted in either the tag form ("v0.2.0") or
// the bare form ("0.2.0") and split into both: the manifests stamp the BARE
// semver (Homebrew's `version` field and Scoop's `v$version` autoupdate both
// require it), while download URLs use the TAG (release assets live under
// releases/download/vX.Y.Z/). Passing either form yields byte-identical
// output. hashes (keyed by asset name) supplies the sha256 per asset,
// defaulting to zeroSHA when absent. It is pure and deterministic.
func render(version string, hashes map[string]string) ([]renderedFile, error) {
	// Case-insensitive prefix strip: the auto-release tags are always
	// lowercase, but a manual workflow_dispatch input may arrive as "V0.2.0".
	bare := version
	if len(bare) > 0 && (bare[0] == 'v' || bare[0] == 'V') {
		bare = bare[1:]
	}
	tag := "v" + bare
	byKey := make(map[string]asset, len(release.ReleaseTargets))
	for _, p := range release.ReleaseTargets {
		name := release.AssetName(p)
		sha := zeroSHA
		if hashes != nil {
			if h, ok := hashes[name]; ok {
				sha = h
			}
		}
		byKey[p.OS+"/"+p.Arch] = asset{
			Name: name,
			URL:  releaseDownloadBase + "/" + tag + "/" + name,
			SHA:  sha,
		}
	}
	data := tmplData{Version: bare, ByKey: byKey}

	rb, err := exec1("graphi.rb", formulaTmpl, data)
	if err != nil {
		return nil, err
	}
	js, err := exec1("graphi.json", scoopTmpl, data)
	if err != nil {
		return nil, err
	}
	return []renderedFile{
		{name: formulaPath, content: rb},
		{name: scoopPath, content: js},
	}, nil
}

// exec1 renders one template with the given data.
func exec1(name, tmpl string, data tmplData) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
}

// parseSums reads a SHA256SUMS file ('<hex>  <name>' lines, two spaces in the
// canonical sha256sum format but any run of whitespace is tolerated) into a
// name→hex map.
func parseSums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open sums %s: %w", path, err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Last field is the file name; first is the hex digest. The middle (a
		// leading '*' binary marker) is harmless.
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = fields[0]
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read sums %s: %w", path, err)
	}
	return out, nil
}

// moduleRoot resolves the module root via `go env GOMOD`, falling back to the
// current working directory.
func moduleRoot() string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err == nil {
		gomod := strings.TrimSpace(string(out))
		if gomod != "" && gomod != "/dev/null" {
			return filepath.Dir(gomod)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// unifiedDiff renders a minimal line-oriented unified diff (committed → fresh)
// for the -check failure message. It avoids any external dependency.
func unifiedDiff(name, oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (committed)\n+++ %s (fresh render)\n", name, name)
	max := len(oldLines)
	if len(newLines) > max {
		max = len(newLines)
	}
	for i := 0; i < max; i++ {
		var o, n string
		if i < len(oldLines) {
			o = oldLines[i]
		}
		if i < len(newLines) {
			n = newLines[i]
		}
		if o != n {
			if i < len(oldLines) {
				fmt.Fprintf(&b, "-%s\n", o)
			}
			if i < len(newLines) {
				fmt.Fprintf(&b, "+%s\n", n)
			}
		}
	}
	return b.String()
}
