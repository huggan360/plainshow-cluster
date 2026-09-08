// Package projectfs is the filesystem side of a project.
//
// The filesystem is the source of truth for project files: there is no database
// index over a directory to drift out of step with it. Every path that arrives
// from a browser is resolved against the project root and rejected if it
// escapes, which is the single most important check in this package.
package projectfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ErrEscapes is returned when a requested path resolves outside its project.
var ErrEscapes = errors.New("path escapes the project")

// ErrTooLarge is returned when a file is too big to edit in the browser.
var ErrTooLarge = errors.New("file is too large to open in the editor")

// MaxEditableBytes caps what the editor will load. Beyond this the browser is
// the wrong tool and streaming it would only stall the tab.
const MaxEditableBytes = 4 << 20 // 4 MiB

// skipDirs are never walked. They are large, machine-generated, and listing
// them makes the tree useless.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".mypy_cache": true,
	".pytest_cache": true, ".ipynb_checkpoints": true,
}

// Project is a rooted view of one project's files.
type Project struct{ Root string }

// New returns a Project rooted at dir.
func New(dir string) Project { return Project{Root: filepath.Clean(dir)} }

// Resolve turns a project-relative path into an absolute one, refusing anything
// that escapes the root. Symlinks are resolved where they exist, so a link
// pointing out of the project is caught rather than followed.
func (p Project) Resolve(rel string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	abs := filepath.Join(p.Root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	rootEval, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		rootEval = p.Root
	}
	// Evaluate the deepest existing ancestor: the target itself may not exist
	// yet (a file about to be created), but its parent chain must stay inside.
	probe := abs
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	probeEval, err := filepath.EvalSymlinks(probe)
	if err != nil {
		probeEval = probe
	}
	rest := strings.TrimPrefix(abs, probe)
	final := filepath.Join(probeEval, rest)

	if final != rootEval && !strings.HasPrefix(final, rootEval+string(os.PathSeparator)) {
		return "", ErrEscapes
	}
	return final, nil
}

// Entry is one node in the file tree.
type Entry struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Dir      bool    `json:"dir"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
	Children []Entry `json:"children,omitempty"`
}

// Tree walks the project up to maxDepth levels deep, directories first and
// alphabetical within each level.
func (p Project) Tree(maxDepth int) ([]Entry, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	return p.walk(p.Root, "", maxDepth)
}

func (p Project) walk(dir, rel string, depth int) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		name := it.Name()
		if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
			if !it.IsDir() {
				continue
			}
		}
		if it.IsDir() && skipDirs[name] {
			continue
		}
		childRel := path.Join(rel, name)
		e := Entry{Name: name, Path: childRel, Dir: it.IsDir()}
		if info, err := it.Info(); err == nil {
			e.Size = info.Size()
			e.Modified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		if it.IsDir() && depth > 1 {
			kids, err := p.walk(filepath.Join(dir, name), childRel, depth-1)
			if err == nil {
				e.Children = kids
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ReadFile returns a file's contents, refusing directories, oversized files and
// anything that is not text.
func (p Project) ReadFile(rel string) (string, error) {
	abs, err := p.Resolve(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", rel)
	}
	if info.Size() > MaxEditableBytes {
		return "", ErrTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if isBinary(b) {
		return "", fmt.Errorf("%s looks like a binary file", rel)
	}
	return string(b), nil
}

// isBinary treats a NUL byte in the first 8 KiB as the signal, which is what
// git and every editor worth copying do.
func isBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// WriteFile saves a file, creating parent directories as needed. The write is
// atomic: a crash mid-save leaves the previous version intact rather than a
// truncated file.
func (p Project) WriteFile(rel, content string) error {
	abs, err := p.Resolve(rel)
	if err != nil {
		return err
	}
	if err := MkdirOwned(filepath.Dir(abs)); err != nil {
		return err
	}
	tmp := abs + ".pscluster-tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o640); err != nil {
		return err
	}
	if err := InheritOwner(tmp, p.Root); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CreateDir makes a directory inside the project.
func (p Project) CreateDir(rel string) error {
	abs, err := p.Resolve(rel)
	if err != nil {
		return err
	}
	return MkdirOwned(abs)
}

// Remove deletes a file, or a directory and everything under it.
func (p Project) Remove(rel string) error {
	if strings.TrimSpace(rel) == "" || rel == "/" || rel == "." {
		return errors.New("refusing to remove the project root")
	}
	abs, err := p.Resolve(rel)
	if err != nil {
		return err
	}
	if abs == filepath.Clean(p.Root) {
		return errors.New("refusing to remove the project root")
	}
	return os.RemoveAll(abs)
}

// Rename moves a file or directory within the project. Both ends are resolved,
// so neither the source nor the destination can point outside it.
func (p Project) Rename(from, to string) error {
	src, err := p.Resolve(from)
	if err != nil {
		return err
	}
	dst, err := p.Resolve(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if err := MkdirOwned(filepath.Dir(dst)); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Upload writes a streamed file into the project, capped at limit bytes.
func (p Project) Upload(rel string, r io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, ErrTooLarge
	}
	abs, err := p.Resolve(rel)
	if err != nil {
		return 0, err
	}
	if err := MkdirOwned(filepath.Dir(abs)); err != nil {
		return 0, err
	}
	// A unique sibling makes concurrent uploads safe while still allowing the
	// final rename to be atomic on every supported filesystem.
	f, err := os.CreateTemp(filepath.Dir(abs), ".pscluster-upload-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	if err := f.Chmod(0o640); err != nil {
		f.Close()
		os.Remove(tmp)
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && n > limit {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := InheritOwner(abs, p.Root); err != nil {
		return n, err
	}
	return n, nil
}

// ValidName reports whether s is safe to use as a project or file name.
func ValidName(s string) bool {
	if s == "" || len(s) > 64 || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\\x00") || strings.HasPrefix(s, ".") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
