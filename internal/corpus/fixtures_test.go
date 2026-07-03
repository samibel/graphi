package corpus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var languages = []string{"go", "ts", "python", "rust", "cs", "kotlin", "ruby"}

// TestFixtures_PresentAndParityDeclared proves every required language has a
// sample file and a PARITY.md declaring analyzer status honestly.
func TestFixtures_PresentAndParityDeclared(t *testing.T) {
	root, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD unavailable: %v", err)
	}
	mod := filepath.Dir(strings.TrimSpace(string(root)))
	fixtures := filepath.Join(mod, "corpus", "fixtures")

	for _, lang := range languages {
		langDir := filepath.Join(fixtures, lang)
		if _, err := os.Stat(langDir); err != nil {
			t.Errorf("language %q missing fixture directory: %v", lang, err)
			continue
		}
		sample := filepath.Join(langDir, "sample."+extForLang(lang))
		if _, err := os.Stat(sample); err != nil {
			t.Errorf("language %q missing sample file: %v", lang, err)
		}
		parity := filepath.Join(langDir, "PARITY.md")
		data, err := os.ReadFile(parity)
		if err != nil {
			t.Errorf("language %q missing PARITY.md: %v", lang, err)
			continue
		}
		// Require at least one Supported/Partial/Unsupported declaration.
		if !strings.Contains(string(data), "Supported") && !strings.Contains(string(data), "Partial") && !strings.Contains(string(data), "Unsupported") {
			t.Errorf("language %q PARITY.md does not declare analyzer status", lang)
		}
	}
}

func extForLang(lang string) string {
	switch lang {
	case "ts":
		return "ts"
	case "python":
		return "py"
	case "cs":
		return "cs"
	case "kotlin":
		return "kt"
	case "ruby":
		return "rb"
	case "go":
		return "go"
	case "rust":
		return "rs"
	default:
		return "txt"
	}
}
