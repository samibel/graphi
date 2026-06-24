// Command gen-install renders the repo-root one-line installer scripts
// (install.sh and install.ps1) from the single source of truth for the release
// matrix — internal/release.ReleaseTargets + release.AssetName (EP-010 Task B,
// SW-065).
//
// The os/arch detection logic lives STATICALLY in the templates
// (templates/install.sh.tmpl, templates/install.ps1.tmpl); only the *valid asset
// name list* + repo/URL constants are injected. This makes the installer's
// accepted `graphi-<os>-<arch>` names provably the same set release.AssetName
// produces — a drift between the two is a CI failure, never a silent skew.
//
// Usage:
//
//	go run ./cmd/gen-install            # write install.sh + install.ps1 to module root
//	go run ./cmd/gen-install -check     # exit 1 with a unified diff if either file is stale
//
// The templates are embedded (//go:embed), so the generator is self-contained
// and the render is byte-deterministic (no wall-clock, no rand).
package main

import (
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

//go:embed templates/install.sh.tmpl
var installShTmpl string

//go:embed templates/install.ps1.tmpl
var installPs1Tmpl string

const (
	repo            = "github.com/samibel/graphi"
	releasesURL     = "https://github.com/samibel/graphi/releases"
	rawInstallURL   = "https://raw.githubusercontent.com/samibel/graphi/main/install.sh"
	rawInstallPS1   = "https://raw.githubusercontent.com/samibel/graphi/main/install.ps1"
	generatedHeader = "GENERATED — do not edit; edit cmd/gen-install/templates/install.sh.tmpl + run `go run ./cmd/gen-install`"
	generatedHdrPS1 = "GENERATED — do not edit; edit cmd/gen-install/templates/install.ps1.tmpl + run `go run ./cmd/gen-install`"
)

// tmplData is the injected, deterministic template input.
type tmplData struct {
	Header          string
	Repo            string
	ReleasesURL     string
	RawInstallURL   string
	RawInstallPS1URL string
	Assets          []string // release.AssetName for each release.ReleaseTargets entry, in matrix order
}

// renderedFile pairs a committed file name with its freshly-rendered content.
type renderedFile struct {
	name    string
	content string
}

func main() {
	check := flag.Bool("check", false, "render both scripts in-memory and exit 1 with a unified diff if either committed file differs")
	flag.Parse()

	files, err := render()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-install: %v\n", err)
		os.Exit(2)
	}

	root := moduleRoot()

	if *check {
		drift := false
		for _, f := range files {
			path := filepath.Join(root, f.name)
			got, rerr := os.ReadFile(path)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "gen-install -check: cannot read %s: %v\n", f.name, rerr)
				drift = true
				continue
			}
			if string(got) != f.content {
				fmt.Fprintf(os.Stderr, "gen-install -check: %s is stale — run `go run ./cmd/gen-install`\n", f.name)
				fmt.Fprint(os.Stderr, unifiedDiff(f.name, string(got), f.content))
				drift = true
			}
		}
		if drift {
			os.Exit(1)
		}
		return
	}

	for _, f := range files {
		path := filepath.Join(root, f.name)
		// Scripts are executable; .ps1 is read on Windows where the bit is moot.
		mode := os.FileMode(0o644)
		if strings.HasSuffix(f.name, ".sh") {
			mode = 0o755
		}
		if werr := os.WriteFile(path, []byte(f.content), mode); werr != nil {
			fmt.Fprintf(os.Stderr, "gen-install: write %s: %v\n", f.name, werr)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "gen-install: wrote %s\n", path)
	}
}

// render produces both installer scripts from the embedded templates + the
// release source of truth. It is pure and deterministic.
func render() ([]renderedFile, error) {
	assets := make([]string, 0, len(release.ReleaseTargets))
	for _, p := range release.ReleaseTargets {
		assets = append(assets, release.AssetName(p))
	}

	sh, err := exec1("install.sh", installShTmpl, tmplData{
		Header:        generatedHeader,
		Repo:          repo,
		ReleasesURL:   releasesURL,
		RawInstallURL: rawInstallURL,
		Assets:        assets,
	})
	if err != nil {
		return nil, err
	}
	ps1, err := exec1("install.ps1", installPs1Tmpl, tmplData{
		Header:           generatedHdrPS1,
		Repo:             repo,
		ReleasesURL:      releasesURL,
		RawInstallPS1URL: rawInstallPS1,
		Assets:           assets,
	})
	if err != nil {
		return nil, err
	}
	return []renderedFile{
		{name: "install.sh", content: sh},
		{name: "install.ps1", content: ps1},
	}, nil
}

// exec1 renders one template with the given data.
func exec1(name, tmpl string, data tmplData) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
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
