// Package gitrepo wraps the git plumbing a project needs.
//
// Git is not the real-time collaboration transport — that is a CRDT over a
// socket, and per-keystroke commits would be unusable as both a sync protocol
// and a history. Git is what carries a project between machines that were not
// online at the same time: every node keeps a full clone, work continues while
// the master is unreachable, and divergence is reconciled by a real three-way
// merge instead of a last-writer-wins guess.
//
// This package is that foundation. It shells out to the git binary rather than
// linking a library: git is already required on any machine running code, its
// CLI is a stable contract, and its merge is the part we most want to inherit.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoGit is returned when the git binary is not installed.
var ErrNoGit = errors.New("git is not installed on this machine")

// Available reports whether git can be used at all.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Repo is a git working tree.
type Repo struct{ Dir string }

// Open returns a handle on the repository at dir.
func Open(dir string) Repo { return Repo{Dir: dir} }

// run executes a git command in the repository and returns its stdout.
func (r Repo) run(args ...string) (string, error) {
	if !Available() {
		return "", ErrNoGit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	// Keep git non-interactive: a prompt in a daemon hangs forever.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// IsRepo reports whether dir is already a git working tree.
func (r Repo) IsRepo() bool {
	_, err := os.Stat(filepath.Join(r.Dir, ".git"))
	return err == nil
}

// Init creates a repository with an initial commit, so that every project has a
// history from its first moment and never needs a special "first commit" path.
func (r Repo) Init() error {
	if r.IsRepo() {
		return nil
	}
	if _, err := r.run("init", "-b", "main"); err != nil {
		return err
	}
	// Identity is set per-repository so the daemon never depends on, or
	// modifies, the host user's global git configuration.
	if _, err := r.run("config", "user.name", "Plainshow Cluster"); err != nil {
		return err
	}
	if _, err := r.run("config", "user.email", "cluster@plainshow.local"); err != nil {
		return err
	}
	return nil
}

// Change is one path reported by git status.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// Status lists the working tree changes in porcelain v1 format.
func (r Repo) Status() ([]Change, error) {
	out, err := r.run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		code := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		// Renames read "old -> new"; the new name is the useful one.
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		changes = append(changes, Change{Path: path, Status: describe(code)})
	}
	return changes, nil
}

func describe(code string) string {
	switch code {
	case "??":
		return "new"
	case "A":
		return "added"
	case "M", "MM", "AM":
		return "modified"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "UU", "AA", "DD":
		return "conflict"
	default:
		return "modified"
	}
}

// Commit stages everything and records a commit. It reports false when there
// was nothing to commit, which is a normal outcome and not an error.
func (r Repo) Commit(message string) (bool, error) {
	changes, err := r.Status()
	if err != nil {
		return false, err
	}
	if len(changes) == 0 {
		return false, nil
	}
	if _, err := r.run("add", "-A"); err != nil {
		return false, err
	}
	if strings.TrimSpace(message) == "" {
		message = "Update from Plainshow Cluster"
	}
	if _, err := r.run("commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// LogEntry is one commit.
type LogEntry struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	When    string `json:"when"`
	Subject string `json:"subject"`
}

// Log returns the most recent commits, newest first.
func (r Repo) Log(limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	const sep = "\x1f"
	out, err := r.run("log",
		fmt.Sprintf("-%d", limit),
		"--date=iso-strict",
		"--pretty=format:%H"+sep+"%h"+sep+"%an"+sep+"%ad"+sep+"%s")
	if err != nil {
		// A repository with no commits yet is empty, not broken.
		if strings.Contains(err.Error(), "does not have any commits") {
			return []LogEntry{}, nil
		}
		return nil, err
	}
	entries := []LogEntry{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, sep)
		if len(parts) < 5 {
			continue
		}
		entries = append(entries, LogEntry{
			Hash: parts[0], Short: parts[1], Author: parts[2],
			When: parts[3], Subject: parts[4],
		})
	}
	return entries, nil
}

// Branch returns the current branch name.
func (r Repo) Branch() string {
	out, err := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(out)
}
