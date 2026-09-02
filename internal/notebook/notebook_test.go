package notebook

import (
	"strings"
	"testing"
)

func TestUnavailableOrLaunchesRealJupyter(t *testing.T) {
	if !Available() {
		if _, err := NewManager().Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "jupyter_server") {
			t.Fatalf("missing Jupyter error = %v", err)
		}
		return
	}
	m := NewManager()
	t.Cleanup(m.Close)
	status, err := m.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.PID == 0 || !strings.HasPrefix(status.URL, "/jupyter/") {
		t.Fatalf("unexpected status: %+v", status)
	}
}
