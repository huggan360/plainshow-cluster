package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
)

// cmdWorkspace moves only repositories; device keys remain in the service root.
// Copy first, retain the old directory, and switch the path only after success.
func cmdWorkspace(args []string) error {
	f := parseFlags(args)
	l, err := layoutFrom(f)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(f.get("user", os.Getenv("SUDO_USER")))
	if username == "" {
		return fmt.Errorf("choose the desktop user: sudo pscluster workspace --user YOUR_USERNAME --root %s", l.Root)
	}
	account, err := user.Lookup(username)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if uid < 1000 || !filepath.IsAbs(account.HomeDir) || account.HomeDir == "/" {
		return fmt.Errorf("choose a regular desktop user with a home directory")
	}
	if os.Geteuid() != 0 && os.Geteuid() != uid {
		return fmt.Errorf("run as %s or with sudo", username)
	}
	source := filepath.Join(l.Root, "projects")
	target := filepath.Join(account.HomeDir, "Plainshow", "Projects")
	if linked, err := filepath.EvalSymlinks(source); err == nil && linked == target {
		fmt.Println("Project folder:", target)
		return nil
	}
	if runtime, err := config.LoadRuntime(l); err == nil && nodeAnswering(runtime.URL) {
		return fmt.Errorf("stop the node first: sudo systemctl stop plainshow-cluster; then repeat this command")
	}
	if info, err := os.Lstat(source); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("projects already links to another location; keep it or move it manually")
	}
	backup := source + ".before-home"
	for _, path := range []string{target, backup} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s already exists; no files were replaced", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return err
	}
	if err := os.Chown(filepath.Dir(target), uid, gid); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0750); err != nil {
		return err
	}
	if err := os.Chown(target, uid, gid); err != nil {
		return err
	}
	if _, err := os.Stat(source); err == nil {
		// cp -a preserves repository symlinks and executable bits across disks.
		cmd := exec.Command("cp", "-a", "--", source+"/.", target)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("copy failed; original untouched, partial copy at %s: %s: %w", target, output, err)
		}
		if err := projectfs.OwnTree(target, account.HomeDir); err != nil {
			return err
		}
		if err := os.Rename(source, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, source); err != nil {
		_ = os.Rename(backup, source)
		return err
	}
	fmt.Printf("Projects: %s/<network-id>/<repository-name>\nPrevious files retained at %s\n", target, backup)
	return nil
}
