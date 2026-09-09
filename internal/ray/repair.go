package ray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ManagedVersion is the Ray release every Plainshow node uses. A cluster may
// mix Python patch releases, but its Ray package version must remain exact.
const ManagedVersion = "2.58.0"

// RepairProgress describes one bounded phase of repairing the managed runtime.
type RepairProgress func(percent int, stage, detail string)

var repairCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = managedEnvironment()
	return command.CombinedOutput()
}

func managedRayVersion(ctx context.Context) (string, error) {
	if managed == "" {
		return "", errors.New("Plainshow has no managed Ray path")
	}
	if _, err := os.Stat(managedPython()); err != nil {
		return "", fmt.Errorf("managed Python is unavailable: %w", err)
	}
	out, err := repairCommand(ctx, managedPython(), "-c", "import ray; print(ray.__version__)")
	if err != nil {
		return "", fmt.Errorf("Ray import failed: %s", repairOutput(out, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// RepairManaged verifies the private runtime and installs the pinned Ray build
// only when it is absent or wrong. Python patch-level compatibility is applied
// by managedEnvironment and needs no system Python replacement.
func RepairManaged(ctx context.Context, progress RepairProgress) error {
	progress(15, "Checking managed Python", managedPython())
	version, checkErr := managedRayVersion(ctx)
	if checkErr == nil && version == ManagedVersion {
		progress(65, "Ray verified", "Ray "+version+" is installed; applying cluster compatibility settings.")
		return nil
	}

	progress(30, "Installing Ray", "Installing Ray "+ManagedVersion+" in Plainshow's private Python environment.")
	out, err := repairCommand(ctx, managedPython(), "-m", "pip", "install", "--upgrade", "ray[default]=="+ManagedVersion)
	if err != nil {
		return fmt.Errorf("managed Ray installation failed: %s", repairOutput(out, err))
	}
	progress(65, "Verifying Ray", "Checking the repaired managed environment.")
	version, err = managedRayVersion(ctx)
	if err != nil {
		return err
	}
	if version != ManagedVersion {
		return fmt.Errorf("managed Ray reports version %s; expected %s", version, ManagedVersion)
	}
	return nil
}

func repairOutput(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if message == "" && err != nil {
		message = err.Error()
	}
	if len(message) > 2000 {
		message = message[len(message)-2000:]
	}
	return message
}
