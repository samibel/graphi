package webui

import (
	"strings"
	"testing"
)

// TestNoticeHTML asserts the notice page is non-empty and tells the operator the
// build tag to use, so it is actionable. It holds in both build flavors.
func TestNoticeHTML(t *testing.T) {
	if NoticeHTML == "" {
		t.Fatal("NoticeHTML is empty")
	}
	if !strings.Contains(NoticeHTML, "webui_embed") {
		t.Fatal("NoticeHTML does not mention the webui_embed build tag")
	}
}
