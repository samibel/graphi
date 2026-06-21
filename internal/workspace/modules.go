// Package workspace reads go.work use directives so CI can auto-discover the
// workspace's modules (story SW-013). New modules added to go.work's use list
// are picked up with no pipeline edit — the build/test matrix derives from here.
package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ModulesFromData parses go.work content and returns the module directories
// listed in `use` directives. Both the single-line (`use .`) and block
// (`use (\n  .\n  ./mod\n)`) forms are supported. Blank/comment lines are
// ignored.
func ModulesFromData(data []byte) ([]string, error) {
	var mods []string
	inBlock := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if inBlock {
			if line == ")" {
				inBlock = false
				continue
			}
			mods = append(mods, strings.Trim(line, `"`))
			continue
		}
		if line == "use (" {
			inBlock = true
			continue
		}
		if strings.HasPrefix(line, "use ") {
			mods = append(mods, strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "use")), `"`))
		}
	}
	if len(mods) == 0 {
		return nil, fmt.Errorf("workspace: no `use` directives found in go.work")
	}
	return mods, nil
}

// Modules reads go.work at the module root and returns the use-directive module
// directories (resolved absolute against the module root).
func Modules(ctx context.Context) ([]string, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, fmt.Errorf("workspace: read go.work: %w", err)
	}
	mods, err := ModulesFromData(data)
	if err != nil {
		return nil, err
	}
	abs := make([]string, 0, len(mods))
	for _, m := range mods {
		if filepath.IsAbs(m) {
			abs = append(abs, m)
		} else {
			abs = append(abs, filepath.Join(root, m))
		}
	}
	return abs, nil
}

var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})
