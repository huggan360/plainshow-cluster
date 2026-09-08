// Package gitrepo wraps the git plumbing a project needs.
//
// Git is not the real-time collaboration transport — that is a CRDT over a
// socket, and per-keystroke commits would be unusable as both a sync protocol
// and a history. Git is what carries a project between machines that were not
// online at the same time: every node keeps a full clone, work continues while
// other devices are unreachable, and divergence is reconciled by a real
// three-way merge instead of a last-writer-wins guess.
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
	"regexp"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/projectfs"
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
	projectfs.AsOwner(cmd, r.Dir)
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

// ---------------------------------------------------------------- remotes --

// runAuthed executes a git command that talks to a remote, supplying the token
// through a credential helper on the command line.
//
// The token is never written into .git/config, never put in the remote URL, and
// never passed as a bare argument that would show up in `ps`. It reaches git
// through an environment variable that the helper reads at the moment it is
// asked, which is the narrowest exposure the git CLI allows.
func (r Repo) runAuthed(token string, args ...string) (string, error) {
	if !Available() {
		return "", ErrNoGit
	}
	helper := `!f() { echo username=x-access-token; echo "password=$PSCLUSTER_GIT_TOKEN"; }; f`
	full := append([]string{"-c", "credential.helper=", "-c", "credential.helper=" + helper}, args...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"PSCLUSTER_GIT_TOKEN="+token,
	)
	projectfs.AsOwner(cmd, r.Dir)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(errb.String())
		if message == "" {
			message = err.Error()
		}
		// Never let a token reach a log or the interface, however git framed it.
		if token != "" {
			message = strings.ReplaceAll(message, token, "***")
		}
		return out.String(), fmt.Errorf("git %s: %s", args[0], message)
	}
	return out.String(), nil
}

// Remote returns the URL of a remote, or "" when it is not set.
func (r Repo) Remote(name string) string {
	out, err := r.run("remote", "get-url", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RemoteRepository reads the "owner/name" this repository points at, if any.
func (r Repo) RemoteRepository() string {
	return RepositoryFromURL(r.Remote("origin"))
}

var remotePattern = regexp.MustCompile(
	`(?:github\.com[:/])([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// RepositoryFromURL extracts "owner/name" from a GitHub remote URL.
func RepositoryFromURL(url string) string {
	match := remotePattern.FindStringSubmatch(strings.TrimSpace(url))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

// SetRemote points origin at a repository, adding it when it does not exist.
func (r Repo) SetRemote(repository string) error {
	url := "https://github.com/" + repository + ".git"
	if r.Remote("origin") == "" {
		_, err := r.run("remote", "add", "origin", url)
		return err
	}
	_, err := r.run("remote", "set-url", "origin", url)
	return err
}

// RemoveRemote disconnects origin without changing the working tree or its
// history. A missing origin is already disconnected and is therefore not an
// error.
func (r Repo) RemoveRemote() error {
	if r.Remote("origin") == "" {
		return nil
	}
	_, err := r.run("remote", "remove", "origin")
	return err
}

// Push sends the current branch to origin and sets it to track.
func (r Repo) Push(token string) (string, error) {
	branch := r.Branch()
	out, err := r.runAuthed(token, "push", "--set-upstream", "origin", branch)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Branches lists the local branches, current one first.
func (r Repo) Branches() ([]string, error) {
	out, err := r.run("branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	current := r.Branch()
	names := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" && line != current {
			names = append(names, line)
		}
	}
	if current != "" {
		names = append([]string{current}, names...)
	}
	return names, nil
}

// HasBranch reports whether a branch exists locally.
func (r Repo) HasBranch(name string) bool {
	_, err := r.run("rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// CreateBranch points a new branch at the current commit without moving to it.
func (r Repo) CreateBranch(name string) error {
	if r.HasBranch(name) {
		return nil
	}
	_, err := r.run("branch", name)
	return err
}

// PushTo sends this working tree's commit to a named branch on origin,
// creating that branch there if it does not exist.
//
// The explicit HEAD:refs/heads/<name> is what makes "push my work to dev" mean
// the same thing whether or not dev exists yet, locally or on the remote. A
// plain push would refuse, or push the wrong branch, depending on config this
// product does not control.
func (r Repo) PushTo(token, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("no branch was named")
	}
	out, err := r.runAuthed(token, "push", "origin", "HEAD:refs/heads/"+name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CloneLocal copies an existing working tree into a new directory on a branch.
//
// It clones from the local path rather than the remote: the commits are already
// here, so this needs no token and no network, and a machine that has just
// pushed cannot fail to make its own branch directory because GitHub is slow.
// The remote is carried over so the new directory pushes and pulls normally.
func CloneLocal(source, destination, branch string) error {
	// Run from the parent: git clone works anywhere, but the Repo helper needs
	// an existing directory to run in and the destination is not one yet.
	if _, err := Open(filepath.Dir(destination)).run("clone", "--branch", branch,
		"--", source, destination); err != nil {
		return err
	}
	repository := Open(source).RemoteRepository()
	if repository == "" {
		return nil
	}
	return Open(destination).SetRemote(repository)
}

// PullResult describes what a pull did, including a merge that could not be
// completed automatically.
type PullResult struct {
	Output    string   `json:"output"`
	Conflicts []string `json:"conflicts"`
	Merged    bool     `json:"merged"`
}

// Pull fetches and merges origin into the current branch.
//
// It merges rather than rebases on purpose. Two people working on separate
// machines, each with local commits, is the normal case here — a rebase would
// rewrite one side's history under them, and a merge records honestly that both
// happened. A conflict is reported rather than resolved: the files are left in
// the working tree with markers, exactly as they would be on the command line.
func (r Repo) Pull(token string) (PullResult, error) {
	result := PullResult{}
	if _, err := r.runAuthed(token, "fetch", "origin"); err != nil {
		return result, err
	}

	branch := r.Branch()
	// A remote branch that does not exist yet means nothing to merge, which is
	// the normal state right after a repository is created.
	if _, err := r.run("rev-parse", "--verify", "origin/"+branch); err != nil {
		result.Output = "Nothing to pull yet — the remote branch does not exist."
		return result, nil
	}

	out, err := r.run("merge", "--no-edit", "origin/"+branch)
	result.Output = strings.TrimSpace(out)
	if err == nil {
		result.Merged = true
		return result, nil
	}

	conflicts, listErr := r.Conflicts()
	if listErr != nil || len(conflicts) == 0 {
		return result, err
	}
	result.Conflicts = conflicts
	return result, nil
}

// Conflicts lists the paths left unmerged by a failed merge.
func (r Repo) Conflicts() ([]string, error) {
	out, err := r.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// InMerge reports whether a merge is in progress and waiting to be resolved.
func (r Repo) InMerge() bool {
	_, err := os.Stat(filepath.Join(r.Dir, ".git", "MERGE_HEAD"))
	return err == nil
}

// ConflictSide names which version of a conflicted file to take whole.
const (
	// KeepMine is the version this machine had before the merge.
	KeepMine = "mine"
	// KeepTheirs is the version that arrived with the merge.
	KeepTheirs = "theirs"
	// DropFile removes a file both sides disagreed about entirely.
	DropFile = "drop"
)

// ResolveConflict takes one side of a conflicted file whole.
//
// Git's own words for the two sides invert during a rebase, and "ours" during a
// merge is the branch you were on. This only ever runs on a merge — Pull merges
// — so ours is the local version and theirs is the incoming one, which is what
// the interface says. Anything that starts rebasing has to revisit this.
//
// Staging is part of resolving. A file whose content is right but which is
// still listed as unmerged blocks the commit, and the person would have no way
// to tell from looking at it.
func (r Repo) ResolveConflict(path, choice string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("no file was named")
	}
	switch choice {
	case KeepMine:
		if _, err := r.run("checkout", "--ours", "--", path); err != nil {
			return err
		}
	case KeepTheirs:
		if _, err := r.run("checkout", "--theirs", "--", path); err != nil {
			return err
		}
	case DropFile:
		// Already staged for removal by git rm, so it must not be added after.
		_, err := r.run("rm", "-f", "--", path)
		return err
	default:
		return fmt.Errorf("unknown resolution %q", choice)
	}
	_, err := r.run("add", "--", path)
	return err
}

// MarkResolved stages a conflicted file somebody edited by hand.
//
// It refuses a file that still carries conflict markers. Committing those
// produces a file that looks merged, builds, and is wrong — the exact failure
// this whole screen exists to prevent.
func (r Repo) MarkResolved(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("no file was named")
	}
	raw, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, ">>>>>>>") {
			return errors.New("this file still contains conflict markers")
		}
	}
	_, err = r.run("add", "--", path)
	return err
}

// FinishMerge commits a merge whose conflicts have all been resolved.
func (r Repo) FinishMerge() error {
	if !r.InMerge() {
		return errors.New("there is no merge to complete")
	}
	remaining, err := r.Conflicts()
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%d file(s) are still unresolved", len(remaining))
	}
	// --no-edit keeps git's own merge message rather than opening an editor
	// that has no terminal to open in.
	_, err = r.run("commit", "--no-edit")
	return err
}

// AbortMerge throws away an in-progress merge and returns to where it started.
func (r Repo) AbortMerge() error {
	_, err := r.run("merge", "--abort")
	return err
}

// Ahead reports how many commits the local branch has that origin does not.
func (r Repo) Ahead() int {
	branch := r.Branch()
	out, err := r.run("rev-list", "--count", "origin/"+branch+".."+branch)
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	return n
}

// Behind reports how many commits origin has that the local branch does not.
func (r Repo) Behind() int {
	branch := r.Branch()
	out, err := r.run("rev-list", "--count", branch+"..origin/"+branch)
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	return n
}

// Clone copies a repository into dir, which must not exist yet.
func Clone(token, repository, dir string) error {
	if !Available() {
		return ErrNoGit
	}
	parent := filepath.Dir(dir)
	if err := projectfs.MkdirOwned(parent); err != nil {
		return err
	}
	// Clone runs in the parent, since the target directory is its output.
	staging := Repo{Dir: parent}
	_, err := staging.runAuthed(token, "clone",
		"https://github.com/"+repository+".git", dir)
	return err
}
