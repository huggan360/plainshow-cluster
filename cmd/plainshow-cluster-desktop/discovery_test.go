package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

func TestFindNodeURLUsesPublishedRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"enabled":true}`))
	}))
	defer server.Close()
	root := t.TempDir()
	layout, _ := config.NewLayout(root)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveRuntime(layout, config.Runtime{URL: server.URL, PID: 42}); err != nil {
		t.Fatal(err)
	}
	got, detail := findNodeURLWithin([]string{"--root", root}, 0)
	if got != server.URL || detail != "" {
		t.Fatalf("discovery = %q, %q", got, detail)
	}
}

func TestFindNodeURLDoesNotProbeLegacyPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:9999")
	if err != nil {
		t.Skip("legacy port is already in use")
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"enabled":true}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	root := t.TempDir()
	t.Setenv("HOME", root)
	got, _ := findNodeURLWithin([]string{"--root", root}, 0)
	if got != "" {
		t.Fatalf("desktop discovered an unpublished address: %q", got)
	}
}

func TestSystemRootRemainsDiscoveryFallback(t *testing.T) {
	roots := uniqueRoots("/home/alice/.plainshow-cluster", "/opt/plainshow-cluster",
		"/home/alice/.plainshow-cluster", "/home/alice/.pscluster")
	want := []string{"/home/alice/.plainshow-cluster", "/opt/plainshow-cluster", "/home/alice/.pscluster"}
	if len(roots) != len(want) {
		t.Fatalf("roots = %#v; want %#v", roots, want)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("roots = %#v; want %#v", roots, want)
		}
	}
}
