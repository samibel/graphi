//go:build !webui_embed

// This file is the DEFAULT (UI-free) build: no embed directive, FS stays nil,
// so Enabled() is false and the server serves NoticeHTML. The default
// `go build ./...` therefore needs no web/dist and the size budget is unchanged.
package webui
