package notebook

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPersistentKernel(t *testing.T) {
	if !Available() {
		t.Skip("python3 is not installed")
	}
	m := NewManager()
	t.Cleanup(m.Close)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := m.Execute(ctx, dir, "value = 6 * 7\nprint('ready')")
	if err != nil {
		t.Fatal(err)
	}
	if joined(first.Outputs) != "ready\n" {
		t.Fatalf("stdout = %q", joined(first.Outputs))
	}
	second, err := m.Execute(ctx, dir, "value")
	if err != nil {
		t.Fatal(err)
	}
	if joined(second.Outputs) != "42" {
		t.Fatalf("persistent result = %q", joined(second.Outputs))
	}
}

func TestKernelReportsTracebackAndRestarts(t *testing.T) {
	if !Available() {
		t.Skip("python3 is not installed")
	}
	m := NewManager()
	t.Cleanup(m.Close)
	dir := t.TempDir()
	result, err := m.Execute(context.Background(), dir, "1 / 0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined(result.Outputs), "ZeroDivisionError") {
		t.Fatalf("missing traceback: %#v", result.Outputs)
	}
	if err := m.Restart(dir); err != nil {
		t.Fatal(err)
	}
	if m.Status(dir).Running {
		t.Fatal("kernel still running after restart")
	}
}

func joined(outputs []Output) string {
	var text strings.Builder
	for _, output := range outputs {
		text.WriteString(output.Text)
	}
	return text.String()
}
