package main

import (
	"encoding/base64"

	"github.com/huggan360/plainshow-cluster/internal/brand"
)

func unavailablePage() string {
	return "<!doctype html><html><head><meta charset=\"utf-8\">" +
		"<meta name=\"color-scheme\" content=\"dark\"><title>Plainshow Cluster</title>" +
		"<style>*{box-sizing:border-box}html,body{background:#07090d;color:#dce5ee;font:14px system-ui;margin:0}" +
		"body{display:grid;place-items:center;min-height:100vh}.card{width:min(520px,calc(100% - 40px));" +
		"border:1px solid #252c35;border-radius:18px;padding:34px;background:#0d1117;text-align:center}" +
		".brand{display:flex;align-items:center;gap:12px;justify-content:center}.brand img{width:42px;height:42px}" +
		".word{font-size:19px;font-weight:700}" +
		".show{color:transparent;background:linear-gradient(100deg,#ff3bf4 0%,#7c3cff 29%,#176bff 61%,#18e5e5 100%);" +
		"background-clip:text;-webkit-background-clip:text;-webkit-text-fill-color:transparent}" +
		"small{display:block;color:#536171;font-size:9px;letter-spacing:.2em;text-transform:uppercase}" +
		"h1{font-size:25px;margin:22px 0 18px}.spinner{width:22px;height:22px;margin:0 auto 22px;" +
		"border:2px solid #27303a;border-top-color:#18e5e5;border-radius:50%;animation:spin .8s linear infinite}" +
		"@keyframes spin{to{transform:rotate(360deg)}}.cmd{font:12px ui-monospace,monospace;color:#b9c7d5;" +
		"background:#07090d;border:1px solid #252c35;padding:12px;border-radius:9px}</style>" +
		"</head><body><main class=\"card\">" + desktopBrand() +
		"<h1>Connecting to your node</h1><div class=\"spinner\" aria-label=\"Connecting\"></div>" +
		"<div class=\"cmd\">sudo systemctl start plainshow-cluster</div>" +
		"<script>(function poll(){fetch('/node',{cache:'no-store'}).then(function(r){return r.json()})" +
		".then(function(v){if(v.url){location.replace(v.url)}})" +
		".catch(function(){}).finally(function(){setTimeout(poll,750)})})()</script></main></body></html>"
}

func desktopBrand() string {
	icon := base64.StdEncoding.EncodeToString(brand.IconWebP)
	return "<div class=\"brand\"><img src=\"data:image/webp;base64," + icon + "\" alt=\"\">" +
		"<span><span class=\"word\">plain<span class=\"show\">show</span></span>" +
		"<small>cluster</small></span></div>"
}
