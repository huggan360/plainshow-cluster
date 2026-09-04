package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPackagingYAMLParses(t *testing.T) {
	for _, path := range []string{"../../snap/snapcraft.yaml", "../../.github/workflows/release.yml"} {
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
