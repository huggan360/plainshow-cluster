package projectfs

import (
	"os"
	"path/filepath"
)

// MkdirOwned creates only missing directories and inherits the existing
// ancestor's owner, including when the workspace is linked into a user's home.
func MkdirOwned(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := MkdirOwned(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0750); err != nil && !os.IsExist(err) {
		return err
	}
	return InheritOwner(dir, parent)
}
