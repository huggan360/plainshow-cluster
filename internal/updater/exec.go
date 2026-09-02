package updater

import (
	"context"
	"os/exec"
)

// execCommand runs a command and returns its combined output. It is a variable
// so tests can stand in for it without spawning anything.
var execCommand = func(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
