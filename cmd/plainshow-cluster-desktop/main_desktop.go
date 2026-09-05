//go:build desktop && linux

// Plainshow Cluster Desktop is the native Linux window for the local node.
//
// The node deliberately remains a small static service binary. This program
// uses the operating system's GTK/WebKit runtime and navigates to that service,
// so live WebSockets, downloads, uploads and authentication behave exactly as
// they do in a normal browser without looking or feeling like one.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"github.com/huggan360/plainshow-cluster/internal/brand"
)

const programName = "plainshow-cluster"

func main() {
	args := append([]string(nil), os.Args[1:]...)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/node" {
			// The service can still be starting when the window opens. Keep this
			// endpoint live instead of freezing the result of one startup probe.
			nodeURL, detail := findNodeURLWithin(args, 350*time.Millisecond)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": nodeURL, "detail": detail})
			return
		}

		nodeURL, _ := findNodeURLWithin(args, 0)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if nodeURL == "" {
			_, _ = io.WriteString(w, unavailablePage())
			return
		}
		target, _ := json.Marshal(nodeURL)
		page := "<!doctype html><html><head><meta charset=\"utf-8\">" +
			"<meta name=\"color-scheme\" content=\"dark\"><title>Plainshow Cluster</title>" +
			"<style>html,body{background:#07090d;color:#dce5ee;font:14px system-ui;margin:0}" +
			"body{display:grid;place-items:center;height:100vh}.brand{display:flex;align-items:center;gap:12px}" +
			".brand img{width:42px;height:42px}.word{font-size:19px;font-weight:700}" +
			".show{color:transparent;background:linear-gradient(100deg,#ff3bf4 0%,#7c3cff 29%,#176bff 61%,#18e5e5 100%);" +
			"background-clip:text;-webkit-background-clip:text;-webkit-text-fill-color:transparent}" +
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
