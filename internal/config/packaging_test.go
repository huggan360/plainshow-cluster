package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPackagingYAMLParses catches a workflow file that no longer parses. CI
// discovers that only when a tag is pushed, by which point the release has
// already half-happened.
func TestPackagingYAMLParses(t *testing.T) {
	for _, path := range []string{
		"../../.github/workflows/release.yml",
		"../../.github/workflows/check.yml",
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
