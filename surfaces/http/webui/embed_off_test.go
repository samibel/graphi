//go:build !webui_embed

package webui

import "testing"

// TestDefaultDisabled asserts the DEFAULT (no-tag) build is UI-free: FS is nil,
// so Enabled() reports false and the server falls back to the notice page. It is
// tagged !webui_embed so it never runs under the bundled build (where Enabled()
// is true by design — see embed_test.go for that flavor's assertion).
func TestDefaultDisabled(t *testing.T) {
	if Enabled() {
		t.Fatal("webui.Enabled() = true in the default build; want false (UI-free)")
	}
	if FS != nil {
		t.Fatal("webui.FS != nil in the default build; want nil")
	}
}
