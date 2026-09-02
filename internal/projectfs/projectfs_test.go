package projectfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newProject builds a project in a temporary directory.
func newProject(t *testing.T) (Project, string) {
	t.Helper()
	root := t.TempDir()
	return New(root), root
}

// TestResolveContainsEscapes is the most important test in the package: every
// one of these inputs is something a browser can send, and each must resolve
// inside the project or be refused outright.
func TestResolveContainsEscapes(t *testing.T) {
	p, root := newProject(t)
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	cases := []string{
		"../escape.txt",
		"../../../../etc/passwd",
		"a/../../../etc/passwd",
		"./../../out",
		"/etc/passwd",
		"//etc/passwd",
		`..\..\windows`,
		"a/b/../../../../out",
	}
	for _, in := range cases {
		got, err := p.Resolve(in)
		if err != nil {
			continue // refusing outright is a valid outcome
		}
		if got != rootEval && !strings.HasPrefix(got, rootEval+string(os.PathSeparator)) {
			t.Errorf("Resolve(%q) = %q, which escapes %q", in, got, rootEval)
		}
	}
}

// TestResolveRefusesSymlinkEscape covers the case a purely lexical check
// misses: a link inside the project pointing out of it.
func TestResolveRefusesSymlinkEscape(t *testing.T) {
	p, root := newProject(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := p.Resolve("link/secret.txt"); err == nil {
		t.Fatal("Resolve followed a symlink out of the project")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	p, _ := newProject(t)
	const body = "import torch\nprint(torch.__version__)\n"

	if err := p.WriteFile("src/train.py", body); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := p.ReadFile("src/train.py")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != body {
		t.Errorf("round trip changed the file:\n got %q\nwant %q", got, body)
	}
}

// TestWriteIsAtomic checks that no temporary file survives a successful save.
func TestWriteIsAtomic(t *testing.T) {
	p, root := newProject(t)
	if err := p.WriteFile("a.py", "x"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "pscluster-tmp") {
			t.Errorf("temporary file %q left behind", e.Name())
		}
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	p, root := newProject(t)
	if err := os.WriteFile(filepath.Join(root, "model.bin"),
		[]byte{0x1f, 0x00, 0x8b, 0x00}, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile("model.bin"); err == nil {
		t.Fatal("ReadFile accepted a binary file")
	}
}

func TestReadFileRejectsOversize(t *testing.T) {
	p, root := newProject(t)
	big := make([]byte, MaxEditableBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), big, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile("big.txt"); err != ErrTooLarge {
		t.Fatalf("ReadFile(big) error = %v, want ErrTooLarge", err)
	}
}

func TestRemoveRefusesProjectRoot(t *testing.T) {
	p, _ := newProject(t)
	for _, in := range []string{"", ".", "/", "  "} {
		if err := p.Remove(in); err == nil {
			t.Errorf("Remove(%q) was allowed; it would delete the project", in)
		}
	}
}

func TestTreeSkipsNoiseAndSorts(t *testing.T) {
	p, root := newProject(t)
	for _, dir := range []string{"node_modules", "__pycache__", ".git", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"zeta.py", "alpha.py"} {
		if err := p.WriteFile(f, ""); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := p.Tree(4)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	var names []string
	for _, e := range tree {
		names = append(names, e.Name)
	}
	want := []string{"src", "alpha.py", "zeta.py"} // directories first, then A-Z
	if len(names) != len(want) {
		t.Fatalf("Tree = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Tree = %v, want %v", names, want)
		}
	}
}

func TestValidName(t *testing.T) {
	good := []string{"vision-model", "a", "train_v2", "model.v1"}
	bad := []string{"", ".", "..", ".hidden", "a/b", "a\\b", "with space",
		strings.Repeat("x", 65), "naïve"}

	for _, s := range good {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}
