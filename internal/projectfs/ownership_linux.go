//go:build linux

package projectfs

import (
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// InheritOwner gives a daemon-created entry to the workspace's Unix owner.
// Lchown never follows a repository symlink into unrelated user files.
func InheritOwner(target, reference string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	info, err := os.Stat(reference)
	if err != nil {
		return err
	}
	owner := info.Sys().(*syscall.Stat_t)
	return os.Lchown(target, int(owner.Uid), int(owner.Gid))
}

// OwnTree is used after cloning/importing a whole repository or migrating it.
func OwnTree(root, reference string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return InheritOwner(path, reference)
	})
}

// AsOwner lets Git create its index, objects and checkout as the workspace user.
func AsOwner(cmd *exec.Cmd, directory string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(directory)
	if err != nil {
		return
	}
	owner := info.Sys().(*syscall.Stat_t)
	if owner.Uid == 0 {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: owner.Uid, Gid: owner.Gid}}
	if account, err := user.LookupId(strconv.FormatUint(uint64(owner.Uid), 10)); err == nil {
		cmd.Env = append(cmd.Env, "HOME="+account.HomeDir, "USER="+account.Username, "LOGNAME="+account.Username, "XDG_CONFIG_HOME="+filepath.Join(account.HomeDir, ".config"))
	}
}
