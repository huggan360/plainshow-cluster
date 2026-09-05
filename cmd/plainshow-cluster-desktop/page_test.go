package main

import (
	"strings"
	"testing"
)

func TestWaitingPageIsMinimalAndBranded(t *testing.T) {
	page := unavailablePage()
	for _, want := range []string{
		"Connecting to your node",
		"class=\"spinner\"",
		"sudo systemctl start plainshow-cluster",
		"linear-gradient(100deg,#ff3bf4",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("waiting page does not contain %q", want)
		}
	}
	for _, unwanted := range []string{
		"not responding",
		"reconnects automatically",
		"sudo systemctl restart",
	} {
		if strings.Contains(page, unwanted) {
			t.Fatalf("waiting page unexpectedly contains %q", unwanted)
		}
	}
}
