package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

// TestAssetNameMatchesTheBuild locks the contract between the release build and
// the updater. `make dist` writes pscluster-<os>-<arch>; if these two ever
// disagree, a release simply looks like no release at all to every node on that
// platform, and nothing reports an error.
func TestAssetNameMatchesTheBuild(t *testing.T) {
	want := fmt.Sprintf("pscluster-%s-%s", runtime.GOOS, runtime.GOARCH)
	if got := AssetName(); got != want {
		t.Errorf("AssetName() = %q, want %q", got, want)
	}
}

func TestPrivateChecksumDownloadUsesGitHubToken(t *testing.T) {
	const token = "private-release-token"
	const hash = "63d360e5da2f95842ce4255b7157e92f8b0fcbe559e533116a2e818ee41c8a02"
	asset := AssetName()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "%s  %s\n", hash, asset)
	}))
	defer server.Close()

	layout, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Keys(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.GitHubToken(), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := &Updater{layout: layout, http: server.Client()}
	got, err := u.expectedChecksum(context.Background(), &Release{
		AssetName: asset, ChecksumURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Fatalf("checksum = %q, want %q", got, hash)
	}
}

// TestMakefileBuildsTheAssetsTheUpdaterLooksFor reads the Makefile rather than
// trusting that two files agree. A rename in one place is exactly the change
// that would go unnoticed.
func TestMakefileBuildsTheAssetsTheUpdaterLooksFor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Skipf("Makefile unavailable: %v", err)
	}
	// The Makefile writes the binary name through a variable, so resolve it the
	// way make would before comparing.
	makefile := string(raw)
	binary := ""
	for _, line := range strings.Split(makefile, "\n") {
		if name, value, ok := strings.Cut(line, ":="); ok && strings.TrimSpace(name) == "BINARY" {
			binary = strings.TrimSpace(value)
			break
		}
	}
	if binary == "" {
		t.Fatal("the Makefile does not define BINARY")
	}
	if binary != "pscluster" {
		t.Errorf("BINARY = %q, but the updater looks for assets named pscluster-*", binary)
	}
	resolved := strings.ReplaceAll(makefile, "$(BINARY)", binary)

	for _, platform := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
	} {
		asset := fmt.Sprintf("pscluster-%s-%s", platform.goos, platform.goarch)
		if !strings.Contains(resolved, asset) {
			t.Errorf("the Makefile does not build %q, so nodes on %s/%s can never update",
				asset, platform.goos, platform.goarch)
		}
	}
	if !strings.Contains(makefile, "checksums.txt") {
		t.Error("the Makefile does not write checksums.txt, so downloads go unverified")
	}
}

// TestChecksumParsingAcceptsSha256sumOutput is the important one: a parse
// failure here is silent. expectedChecksum returns an empty string when it
// cannot find a match, and the caller treats that as "no checksum published"
// and installs anyway. So a format it cannot read means downloads stop being
// verified without anybody being told.
func TestChecksumParsingAcceptsSha256sumOutput(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum unavailable")
	}
	dir := t.TempDir()
	asset := AssetName()
	if err := os.WriteFile(filepath.Join(dir, asset), []byte("pretend binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Exactly what the Makefile runs.
	cmd := exec.Command("sh", "-c", "sha256sum pscluster-* > checksums.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sha256sum: %v: %s", err, out)
	}
	published, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}

	got := parseChecksums(string(published), asset)
	if got == "" {
		t.Fatalf("could not find %q in real sha256sum output:\n%s", asset, published)
	}

	expected := strings.Fields(string(published))[0]
	if got != expected {
		t.Errorf("parsed %q, want %q", got, expected)
	}
}

func TestChecksumParsingHandlesTheOtherShapes(t *testing.T) {
	asset := AssetName()
	const hash = "63d360e5da2f95842ce4255b7157e92f8b0fcbe559e533116a2e818ee41c8a02"

	cases := []struct {
		name, body, want string
	}{
		{"bare hash", hash, hash},
		{"bare hash with newline", hash + "\n", hash},
		{"binary marker", hash + " *" + asset + "\n", hash},
		{"several assets", "aa  pscluster-other-arch\n" + hash + "  " + asset + "\n", hash},
		{"no match", "aa  pscluster-other-arch\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseChecksums(tc.body, asset); got != tc.want {
				t.Errorf("parseChecksums(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}
