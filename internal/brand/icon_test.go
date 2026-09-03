package brand

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestIconMatchesProductionAsset(t *testing.T) {
	if len(IconWebP) != 12510 {
		t.Fatalf("icon is %d bytes, want 12510", len(IconWebP))
	}
	if string(IconWebP[:4]) != "RIFF" || string(IconWebP[8:12]) != "WEBP" {
		t.Fatal("icon is not a WebP image")
	}
	got := fmt.Sprintf("%x", sha256.Sum256(IconWebP))
	const want = "905914c516971003c65d8d6d09863a6995d6938a64c78c1a4a2b6aa313573aa5"
	if got != want {
		t.Fatalf("icon hash = %s, want production hash %s", got, want)
	}
}
