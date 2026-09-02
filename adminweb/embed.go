// Package adminweb embeds the lightweight global account dashboard.
package adminweb

import "embed"

// Assets contains the complete dashboard; there is no frontend build step.
//
//go:embed index.html app.css app.js
var Assets embed.FS
