package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPackagingYAMLParses catches a packaging file that no longer parses. CI
// discovers that only when a tag is pushed, by which point the release has
// already half-happened — a snapcraft file rejected outright once cost four
// rounds of chasing symptoms.
func TestPackagingYAMLParses(t *testing.T) {
	for _, path := range []string{
		"../../.github/workflows/release.yml",
		"../../.github/workflows/check.yml",
		"../../snap/snapcraft.yaml",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s is not valid YAML: %v", path, err)
		}
	}
}
