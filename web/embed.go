// Package web carries the built interface inside the binary.
//
// The interface is plain ES modules and CSS with no build step, so what is
// served is exactly what is in this directory. Embedding it means a node is one
// file to copy and needs no asset directory next to it.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.css app.js fonts.css boxicons.css lib views fonts images
var files embed.FS

// Assets is the interface, rooted at index.html.
var Assets fs.FS = files
