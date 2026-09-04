//go:build desktop && linux

// Plainshow Cluster Desktop is the native Linux window for the local node.
//
// The node deliberately remains a small static service binary. This program
// uses the operating system's GTK/WebKit runtime and navigates to that service,
// so live WebSockets, downloads, uploads and authentication behave exactly as
// they do in a normal browser without looking or feeling like one.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"github.com/huggan360/plainshow-cluster/internal/brand"
)

const programName = "plainshow-cluster"

func main() {
	nodeURL, detail := findNodeURL(os.Args[1:])
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if nodeURL == "" {
			_, _ = io.WriteString(w, unavailablePage(detail))
			return
		}
		target, _ := json.Marshal(nodeURL)
		page := "<!doctype html><html><head><meta charset=\"utf-8\">" +
			"<meta name=\"color-scheme\" content=\"dark\"><title>Plainshow Cluster</title>" +
			"<style>html,body{background:#07090d;color:#dce5ee;font:14px system-ui;margin:0}" +
			"body{display:grid;place-items:center;height:100vh}.brand{display:flex;align-items:center;gap:12px}" +
			".brand img{width:42px;height:42px}.word{font-size:19px;font-weight:700}.show{color:#55d6be}" +
			"small{display:block;color:#536171;font-size:9px;letter-spacing:.2em;text-transform:uppercase}" +
			".muted{color:#73808e;margin-top:8px}</style></head><body>" +
			"<div>" + desktopBrand() +
			"<div class=\"muted\">Opening your node…</div></div>" +
			"<script>location.replace(" + string(target) + ")</script></body></html>"
		_, _ = io.WriteString(w, page)
	})

	if err := wails.Run(&options.App{
		Title:                    "Plainshow Cluster",
		Width:                    1360,
		Height:                   860,
		MinWidth:                 900,
		MinHeight:                620,
		BackgroundColour:         &options.RGBA{R: 7, G: 9, B: 13, A: 1},
		AssetServer:              &assetserver.Options{Handler: handler},
		WindowStartState:         options.Normal,
		EnableDefaultContextMenu: false,
		Linux: &linux.Options{
			Icon:             brand.IconWebP,
			ProgramName:      programName,
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, "plainshow-cluster:", err)
		os.Exit(1)
	}
}

func unavailablePage(detail string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\">" +
		"<meta name=\"color-scheme\" content=\"dark\"><title>Plainshow Cluster</title>" +
		"<style>*{box-sizing:border-box}html,body{background:#07090d;color:#dce5ee;font:14px system-ui;margin:0}" +
		"body{display:grid;place-items:center;min-height:100vh}.card{width:min(520px,calc(100% - 40px));" +
		"border:1px solid #252c35;border-radius:18px;padding:30px;background:#0d1117}" +
		".brand{display:flex;align-items:center;gap:12px}.brand img{width:42px;height:42px}" +
		".word{font-size:19px;font-weight:700}.show{color:#55d6be}" +
		"small{display:block;color:#536171;font-size:9px;letter-spacing:.2em;text-transform:uppercase}" +
		"h1{font-size:25px;margin:18px 0 10px}" +
		"p{color:#8a98a8;line-height:1.6}.cmd{font:12px ui-monospace,monospace;color:#b9c7d5;" +
		"background:#07090d;border:1px solid #252c35;padding:12px;border-radius:9px;margin-top:18px}</style>" +
		"</head><body><main class=\"card\">" + desktopBrand() +
		"<h1>The node is offline</h1><p>" + html.EscapeString(detail) + "</p>" +
		"<p>Start the installed service, then reopen this program.</p>" +
		"<div class=\"cmd\">sudo systemctl start plainshow-cluster</div></main></body></html>"
}

func desktopBrand() string {
	icon := base64.StdEncoding.EncodeToString(brand.IconWebP)
	return "<div class=\"brand\"><img src=\"data:image/webp;base64," + icon + "\" alt=\"\">" +
		"<span><span class=\"word\">plain<span class=\"show\">show</span></span>" +
		"<small>cluster</small></span></div>"
}
