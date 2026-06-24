//go:build webui_embed

// This file is the BUNDLED build (-tags webui_embed): it embeds the dist
// directory (populated by scripts/build-release-webui.sh, which copies web/dist
// into surfaces/http/webui/dist) and exposes it as FS rooted at the dist
// contents. The `all:` prefix ensures dotfiles/underscore files in the Vite
// output are embedded too. dist is gitignored here — it is a build artifact.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

func init() {
	if sub, err := fs.Sub(distFS, "dist"); err == nil {
		FS = sub
	}
}
