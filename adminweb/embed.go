// Package adminweb embeds the lightweight global account dashboard.
package adminweb

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/huggan360/plainshow-cluster/web"
)

// Assets contains the complete dashboard; there is no frontend build step.
//
//go:embed index.html app.css app.js
var files embed.FS

type assets struct{}

func (assets) Open(name string) (fs.File, error) {
	if name == "fonts.css" || strings.HasPrefix(name, "fonts/") {
		return web.Assets.Open(name)
	}
	return files.Open(name)
}

// Assets combines the small admin application with the already bundled
// PlainShow typefaces, keeping both programs visually identical offline.
var Assets fs.FS = assets{}
