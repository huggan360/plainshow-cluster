//go:build !linux

package projectfs

import "os/exec"

func InheritOwner(target, reference string) error { return nil }
func OwnTree(root, reference string) error        { return nil }
func AsOwner(cmd *exec.Cmd, directory string)     {}
