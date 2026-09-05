package sysinfo

import (
	"errors"
	"testing"
)

func stubRun(t *testing.T, replies map[string]string) {
	t.Helper()
	original := run
	run = func(name string, args ...string) ([]byte, error) {
		if out, ok := replies[name]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("not installed")
	}
	t.Cleanup(func() { run = original })
}

// Real `nvidia-smi --query-gpu=... --format=csv,noheader,nounits` output.
const nvidiaSample = "NVIDIA GeForce RTX 5060, 8151, 1024, 37, 52\n"

func TestNVIDIAParsed(t *testing.T) {
	stubRun(t, map[string]string{"nvidia-smi": nvidiaSample})
	gpus := probeNVIDIA()
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want 1", len(gpus))
	}
	g := gpus[0]
	if g.Name != "NVIDIA GeForce RTX 5060" || g.Vendor != "nvidia" || !g.Trainable {
		t.Errorf("gpu = %+v", g)
	}
	if g.VRAMTotalMB != 8151 || g.VRAMUsedMB != 1024 || g.UtilPercent != 37 || g.TempC != 52 {
		t.Errorf("values wrong: %+v", g)
	}
}

// Real `rocm-smi --json` shape: VRAM in bytes, per-card maps.
const rocmSample = `{
  "card0": {
    "Card Series": "AMD Radeon RX 7800 XT",
    "VRAM Total Memory (B)": "17163091968",
    "VRAM Total Used Memory (B)": "1073741824",
    "GPU use (%)": "12",
    "Temperature (Sensor edge) (C)": "44.0"
  }
}`

func TestROCmParsed(t *testing.T) {
	stubRun(t, map[string]string{"rocm-smi": rocmSample})
	gpus := probeROCm()
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want 1", len(gpus))
	}
	g := gpus[0]
	if g.Vendor != "amd" || !g.Trainable || g.Name != "AMD Radeon RX 7800 XT" {
		t.Errorf("gpu = %+v", g)
	}
	// Bytes must be reported as megabytes, or a 16 GB card reads as 17 billion.
	if g.VRAMTotalMB != 16368 {
		t.Errorf("VRAMTotalMB = %d, want 16368", g.VRAMTotalMB)
	}
	if g.VRAMUsedMB != 1024 || g.UtilPercent != 12 || g.TempC != 44 {
		t.Errorf("values wrong: %+v", g)
	}
}

func TestNothingInstalledIsNotAnError(t *testing.T) {
	stubRun(t, map[string]string{})
	if got := probeNVIDIA(); len(got) != 0 {
		t.Errorf("probeNVIDIA with no tooling = %+v", got)
	}
	if got := probeROCm(); len(got) != 0 {
		t.Errorf("probeROCm with no tooling = %+v", got)
	}
	// probeGPUs must never panic on a machine with nothing at all.
	if got := probeGPUs(); got == nil {
		t.Error("probeGPUs returned nil rather than an empty list")
	}
}

func TestGarbageOutputIsIgnored(t *testing.T) {
	stubRun(t, map[string]string{"nvidia-smi": "not, enough\nfields\n", "rocm-smi": "{ broken"})
	if got := probeNVIDIA(); len(got) != 0 {
		t.Errorf("short lines were accepted: %+v", got)
	}
	if got := probeROCm(); len(got) != 0 {
		t.Errorf("invalid JSON was accepted: %+v", got)
	}
}

// Indexes must be contiguous across vendors, since they are what a job refers
// to when it asks for a particular device.
func TestIndexesAreContiguous(t *testing.T) {
	stubRun(t, map[string]string{
		"nvidia-smi": nvidiaSample + "NVIDIA GeForce RTX 4070, 12282, 0, 0, 40\n",
	})
	gpus := probeGPUs()
	for i, g := range gpus {
		if g.Index != i {
			t.Errorf("gpu %d has index %d", i, g.Index)
		}
	}
}
