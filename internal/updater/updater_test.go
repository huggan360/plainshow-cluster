package updater

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"1.0.1", "1.0.0", true},
		{"v1.1.0", "1.0.9", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", false},
		{"1.0.0-alpha.2", "1.0.0-alpha.1", true},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", false},
		{"1.0.0-beta.1", "1.0.0-alpha.9", true},
		{"1.0.0", "1.0.0-rc.3", true},
		{"1.0.0-alpha.1", "1.0.0", false},
		{"1.0.0-alpha.2", "v1.0.0-alpha.1-3-gabcdef", true},
		{"1.0.0-alpha.1", "81bfd39", true},
		{"1.0.0", "0.1.0-dev", true},
		{"", "0.1.0", false},
	}
	for _, tc := range tests {
		if got := IsNewer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v",
				tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName(); got == "" || got[:10] != "pscluster-" {
		t.Fatalf("unexpected asset name %q", got)
	}
}
