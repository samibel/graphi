package seamready

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/divergence"
	"github.com/samibel/graphi/internal/seamreach"
	"github.com/samibel/graphi/surfaces/client"
)

// LiveSources reads the real inputs: the checkout at root (git, symbols), the
// divergence record under stateDir, and the seam as surfaces/client compiles
// it. It never applies the operator's GRAPHI_CANARY_* environment — the
// question is about what ships, not about the shell it is asked from.
//
// The kill-switch variable name is cmd/internal/runtime.EnvCanaryModeFor, which
// this package cannot import (Go's internal rule: only cmd/... may). The
// binary in cmd/seamready supplies it through WithRuntime; without it c6 reads
// UNKNOWN rather than guessing at the spelling.
func LiveSources(root, stateDir string) Sources {
	src := Sources{
		Migrated:       client.MigratedOperations(),
		Git:            RepoGit(root),
		Symbols:        TreeSymbols(root),
		LegacyAccepted: func() error { _, err := client.ParseCanaryMode("legacy"); return err },
	}
	rep, err := divergence.Read(stateDir)
	if err != nil {
		src.RecordErr = err
	}
	src.Record = divergence.Assess(rep, src.Migrated, seamProfiles())
	return src
}

// WithRuntime installs the kill-switch naming function the composition root
// owns.
func (s Sources) WithRuntime(envVarFor func(operation string) string) Sources {
	s.EnvVarFor = envVarFor
	return s
}

// seamProfiles reduces the shipped MCP profiles to the divergence record's
// vocabulary, through the same seamreach.Live() picture the reachability gate
// judges, so the two tools cannot disagree about who reaches what.
func seamProfiles() []divergence.Profile {
	seam := seamreach.Live()
	out := make([]divergence.Profile, 0, len(seam.Profiles))
	for _, p := range seam.Profiles {
		reaches := make([]string, 0, len(p.Reaches))
		for _, op := range seam.Operations {
			if p.Reaches[op.ID] {
				reaches = append(reaches, op.ID)
			}
		}
		out = append(out, divergence.Profile{ID: p.ID, Invocation: p.Invocation, Default: p.Default, Reaches: reaches})
	}
	return out
}

// TreeSymbols returns a SymbolLookup over the checkout at root. It parses one
// file per call with go/parser and looks for a top-level function declaration
// of that name — no type-checking, no build, and a method of the same name
// does not count.
func TreeSymbols(root string) SymbolLookup {
	return func(file, symbol string) (bool, error) {
		path := filepath.Join(root, filepath.FromSlash(file))
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, raw, parser.SkipObjectResolution)
		if err != nil {
			return false, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == symbol {
				return true, nil
			}
		}
		return false, nil
	}
}

// repoGit is Git over the `git` binary in a checkout.
type repoGit struct{ root string }

// RepoGit returns a Git for the checkout at root, or nil when root is not
// inside a git work tree or git is not installed — in which case every
// git-backed criterion reads UNKNOWN.
func RepoGit(root string) Git {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil
	}
	return repoGit{root: root}
}

func (g repoGit) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.root
	return cmd.Output()
}

func (g repoGit) HasAnyTag() (bool, error) {
	out, err := g.run("tag", "--list")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (g repoGit) TagExists(tag string) (bool, error) {
	out, err := g.run("tag", "--list", "--", tag)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == tag {
			return true, nil
		}
	}
	return false, nil
}

func (g repoGit) FileAtTag(tag, path string) ([]byte, error) {
	return g.run("show", tag+":"+path)
}

func (g repoGit) CommitExists(sha string) (bool, error) {
	_, err := g.run("cat-file", "-e", sha+"^{commit}")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return err == nil, err
}
