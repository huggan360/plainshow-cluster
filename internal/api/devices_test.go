package api

import (
	"encoding/json"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
)

func TestThisDeviceUsesLiveInventory(t *testing.T) {
	devices := []accountserver.AccountDevice{
		{ID: "self", GPUCount: 1},
		{ID: "remote", CPUCores: 4, RAMTotalMB: 8192, GPUCount: 2},
	}
	info := sysinfo.Info{
		OS: "linux", Arch: "arm64", CPUCores: 8, RAMTotalMB: 16384,
		GPUs: []sysinfo.GPU{{Name: "Live GPU", Vendor: "amd", VRAMTotalMB: 4096}},
	}
	enrichThisDevice(devices, "self", info)

	var gpus []sysinfo.GPU
	if err := json.Unmarshal(devices[0].GPUs, &gpus); err != nil {
		t.Fatal(err)
	}
	if !devices[0].Online || devices[0].CPUCores != 8 || devices[0].RAMTotalMB != 16384 ||
		devices[0].GPUCount != 1 || len(gpus) != 1 || gpus[0].Name != "Live GPU" {
		t.Fatalf("self inventory was not enriched: %+v, GPUs %+v", devices[0], gpus)
	}
	if devices[1].CPUCores != 4 || devices[1].RAMTotalMB != 8192 || devices[1].GPUCount != 2 {
		t.Fatalf("remote inventory was changed: %+v", devices[1])
	}
}
