package adminweb

import (
	"io/fs"
	"testing"
)

func TestAssetsIncludeAdminAndSharedFonts(t *testing.T) {
	for _, name := range []string{"index.html", "app.css", "app.js", "fonts.css", "fonts/space-grotesk-400.woff2"} {
		file, err := Assets.Open(name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		info, err := file.Stat()
		file.Close()
		if err != nil || info.IsDir() || info.Size() == 0 {
			t.Fatalf("asset %s is not a populated file: %+v, %v", name, info, err)
		}
	}
	if _, err := fs.Stat(Assets, "missing.css"); err == nil {
		t.Fatal("missing asset unexpectedly exists")
	}
}
