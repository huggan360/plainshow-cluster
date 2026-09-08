package ray

import (
	_ "embed"
	"errors"
	"strconv"
	"strings"
)

//go:embed preset.py
var presetCode string

// Preset renders a dependency-free Ray skeleton which discovers live resources
// at execution, so a file remains useful after a device goes offline.
func Preset(mode, projectID, filename string, limit int) (string, error) {
	titles := map[string]string{"cpu": "All CPUs", "gpu": "All GPUs", "mixed": "GPUs and CPU-only devices", "nvidia": "NVIDIA GPUs", "amd": "AMD GPUs", "intel": "Intel GPUs"}
	title, ok := titles[mode]
	if !ok {
		return "", errors.New("Choose CPU, GPU, mixed, NVIDIA, AMD or Intel")
	}
	if limit < 0 || limit > 1024 {
		return "", errors.New("Worker limit must be between 0 and 1024")
	}
	return strings.NewReplacer("__TITLE__", title, "__MODE__", mode, "__PROJECT_ID__", projectID, "__FILENAME__", filename, "__LIMIT__", strconv.Itoa(limit)).Replace(presetCode), nil
}
