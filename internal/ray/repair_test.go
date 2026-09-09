package ray

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func useFakeManagedPython(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	python := filepath.Join(dir, "python")
	if err := os.WriteFile(python, []byte("fake"), 0755); err != nil {
		t.Fatal(err)
	}
	previousManaged, previousCommand := managed, repairCommand
	UseManaged(filepath.Join(dir, "ray"))
	t.Cleanup(func() { UseManaged(previousManaged); repairCommand = previousCommand })
}

func TestRepairManagedKeepsMatchingRuntime(t *testing.T) {
	useFakeManagedPython(t)
	calls := 0
	repairCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != managedPython() || strings.Join(args, " ") != "-c import ray; print(ray.__version__)" {
			t.Fatalf("unexpected command: %s %q", name, args)
		}
		return []byte(ManagedVersion + "\n"), nil
	}
	if err := RepairManaged(context.Background(), func(int, string, string) {}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("matching Ray runtime used %d commands, want one verification", calls)
	}
}

func TestRepairManagedInstallsAndVerifiesPinnedRay(t *testing.T) {
	useFakeManagedPython(t)
	calls := [][]string{}
	repairCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		switch len(calls) {
		case 1:
			return []byte("2.57.0\n"), nil
		case 2:
			return []byte("installed\n"), nil
		default:
			return []byte(ManagedVersion + "\n"), nil
		}
	}
	lastPercent := 0
	if err := RepairManaged(context.Background(), func(percent int, _, _ string) {
		if percent < lastPercent {
			t.Fatalf("repair progress moved backwards from %d to %d", lastPercent, percent)
		}
		lastPercent = percent
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("repair used %d commands, want check, install and verification", len(calls))
	}
	install := strings.Join(calls[1], " ")
	if !strings.Contains(install, "-m pip install --upgrade ray[default]=="+ManagedVersion) {
		t.Fatalf("repair installed the wrong package: %s", install)
	}
}
