//go:build webui_embed

package webui

import (
	"io/fs"
	"testing"
)

// TestEmbeddedEnabled asserts the bundled build actually embedded the UI: FS is
// populated, Enabled() is true, and index.html resolves at the FS root.
func TestEmbeddedEnabled(t *testing.T) {
	if !Enabled() {
		t.Fatal("webui.Enabled() = false under -tags webui_embed; want true (UI embedded)")
	}
	if _, err := fs.Stat(FS, "index.html"); err != nil {
		t.Fatalf("fs.Stat(FS, \"index.html\"): %v; want the embedded SPA entrypoint", err)
	}
}
