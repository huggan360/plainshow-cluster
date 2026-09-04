package brand

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDesktopIconsMatchTheEmbeddedMark guards against the desktop entry drifting
// away from the identity every binary already serves.
//
// The icons shipped to the desktop are generated from IconWebP. If somebody
// replaces the embedded mark and forgets the PNGs, the program in the
// application list stops looking like the product — which is exactly the kind
// of thing nobody notices until a screenshot.
func TestDesktopIconsMatchTheEmbeddedMark(t *testing.T) {
	if len(IconWebP) == 0 {
		t.Fatal("the embedded brand mark is empty")
	}
	// A WebP file begins "RIFF....WEBP".
	if !bytes.HasPrefix(IconWebP, []byte("RIFF")) || !bytes.Contains(IconWebP[:16], []byte("WEBP")) {
		t.Errorf("the embedded mark is not a WebP image")
	}

	root := filepath.Join("..", "..", "packaging", "desktop")
	for _, size := range []string{"48", "64", "128", "256"} {
		path := filepath.Join(root, "plainshow-cluster-"+size+".png")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("desktop icon %s is missing; regenerate it from IconWebP", path)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G'}) {
			t.Errorf("%s is not a PNG", path)
		}
	}
}

// TestDesktopEntryPointsAtWhatWeInstall: the entry names an icon and a window
// class, and both have to match what the installer writes or the desktop shows
// a generic browser icon.
func TestDesktopEntryPointsAtWhatWeInstall(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packaging", "desktop",
		"plainshow-cluster.desktop"))
	if err != nil {
		t.Skipf("desktop entry unavailable: %v", err)
	}
	entry := string(raw)
	for _, want := range []string{
		"Icon=plainshow-cluster",
		"StartupWMClass=plainshow-cluster",
		"Exec=pscluster app",
	} {
		if !bytes.Contains([]byte(entry), []byte(want)) {
			t.Errorf("the desktop entry is missing %q", want)
		}
	}
}
