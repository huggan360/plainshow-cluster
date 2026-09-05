// Package sysinfo probes the host for the resources the cluster cares about.
//
// Everything here degrades rather than fails: a machine with no GPU, no
// /proc/loadavg or no vendor utility reports what it can and leaves the rest empty.
// A worker that cannot describe itself is still a worker.
package sysinfo

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// GPU is a single accelerator visible on the host.
type GPU struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	// Vendor is "nvidia", "amd" or "intel". It is also advertised as a custom
	// Ray resource so a job that needs CUDA, ROCm or oneAPI can request a node
	// with the matching framework stack. Whether one distributed run can mix
	// vendors is ultimately a property of that project's framework/backend.
	Vendor string `json:"vendor"`
	// Trainable reports whether this device is offered to Ray. Integrated Intel
	// graphics are compute devices too when the project's oneAPI/OpenCL stack is
	// installed, so all three supported vendors are trainable by default.
	Trainable   bool `json:"trainable"`
	VRAMTotalMB int  `json:"vram_total_mb"`
	VRAMUsedMB  int  `json:"vram_used_mb"`
	UtilPercent int  `json:"util_percent"`
	TempC       int  `json:"temp_c"`
}

// Info is a snapshot of the host.
type Info struct {
	Hostname    string  `json:"hostname"`
	OS          string  `json:"os"`
	Arch        string  `json:"arch"`
	Kernel      string  `json:"kernel"`
	Model       string  `json:"model"`
	CPUCores    int     `json:"cpu_cores"`
	CPUModel    string  `json:"cpu_model"`
	LoadAvg1    float64 `json:"load_avg_1"`
	RAMTotalMB  int     `json:"ram_total_mb"`
	RAMUsedMB   int     `json:"ram_used_mb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	DiskFreeGB  float64 `json:"disk_free_gb"`
	UptimeSec   int64   `json:"uptime_sec"`
	GPUs        []GPU   `json:"gpus"`
	SampledAt   string  `json:"sampled_at"`
}

// Probe collects a snapshot. dataPath selects which filesystem is reported, so
// the UI shows the disk the node actually stores things on rather than /.
func Probe(dataPath string) Info {
	i := Info{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		CPUCores:  runtime.NumCPU(),
		GPUs:      []GPU{},
		SampledAt: time.Now().UTC().Format(time.RFC3339),
	}
	i.Hostname, _ = os.Hostname()
	i.Kernel = firstLine("/proc/sys/kernel/osrelease")
	i.Model = strings.TrimRight(firstLine("/proc/device-tree/model"), "\x00")
	i.CPUModel = cpuModel()
	i.LoadAvg1 = loadAvg1()
	i.RAMTotalMB, i.RAMUsedMB = memory()
	i.DiskTotalGB, i.DiskFreeGB = disk(dataPath)
	i.UptimeSec = uptime()
	i.GPUs = probeGPUs()
	return i
}

func firstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

// cpuModel reads the friendliest CPU name /proc/cpuinfo offers. The key differs
// between x86 ("model name") and ARM ("Model"/"Hardware"), so try each.
func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()

	fallback := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch key {
		case "model name":
			return val
		case "hardware", "model":
			if fallback == "" {
				fallback = val
			}
		}
	}
	return fallback
}

func loadAvg1() float64 {
	fields := strings.Fields(firstLine("/proc/loadavg"))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

// memory reports total and used RAM in MB, using MemAvailable because "free" on
// Linux excludes reclaimable cache and badly understates what a job can have.
func memory() (total, used int) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var totalKB, availKB int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "MemTotal:":
			totalKB = v
		case "MemAvailable:":
			availKB = v
		}
	}
	if totalKB == 0 {
		return 0, 0
	}
	return totalKB / 1024, (totalKB - availKB) / 1024
}

func disk(path string) (totalGB, freeGB float64) {
	var st syscall.Statfs_t
	if path == "" {
		path = "/"
	}
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	const gb = 1024 * 1024 * 1024
	bs := float64(st.Bsize)
	return float64(st.Blocks) * bs / gb, float64(st.Bavail) * bs / gb
}

func uptime() int64 {
	fields := strings.Fields(firstLine("/proc/uptime"))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return int64(v)
}

// probeGPUs reports every accelerator on the host.
//
// Each vendor is asked in its own way and none of them being present is an
// ordinary answer: a machine with no GPU is still a useful CPU worker.
func probeGPUs() []GPU {
	out := []GPU{}
	out = append(out, probeNVIDIA()...)
	out = append(out, probeAMD()...)
	out = append(out, probeIntel()...)
	for i := range out {
		out[i].Index = i
	}
	return out
}

// run executes a probe command. A variable so the parsing can be tested on a
// machine that has none of these tools.
var run = func(name string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Output()
}

func atoi(s string) int { v, _ := strconv.Atoi(strings.TrimSpace(s)); return v }

// probeNVIDIA reads nvidia-smi, which reports everything worth showing.
func probeNVIDIA() []GPU {
	out := []GPU{}
	raw, err := run("nvidia-smi",
		"--query-gpu=name,memory.total,memory.used,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 5 {
			continue
		}
		out = append(out, GPU{
			Name: strings.TrimSpace(parts[0]), Vendor: "nvidia", Trainable: true,
			VRAMTotalMB: atoi(parts[1]), VRAMUsedMB: atoi(parts[2]),
			UtilPercent: atoi(parts[3]), TempC: atoi(parts[4]),
		})
	}
	return out
}

// probeAMD reads rocm-smi when ROCm is installed, and falls back to the kernel
// driver's own files otherwise, so a Radeon is at least visible without ROCm.
func probeAMD() []GPU {
	if found := probeROCm(); len(found) > 0 {
		return found
	}
	return probeSysfsVendor("0x1002", "amd", true)
}

func probeROCm() []GPU {
	out := []GPU{}
	raw, err := run("rocm-smi", "--showproductname", "--showmeminfo", "vram",
		"--showuse", "--showtemp", "--json")
	if err != nil {
		return out
	}
	var payload map[string]map[string]string
	if json.Unmarshal(raw, &payload) != nil {
		return out
	}
	cards := make([]string, 0, len(payload))
	for card := range payload {
		if strings.HasPrefix(card, "card") {
			cards = append(cards, card)
		}
	}
	sort.Strings(cards)
	for _, card := range cards {
		fields := payload[card]
		gpu := GPU{Vendor: "amd", Trainable: true, Name: firstField(fields,
			"Card Series", "Card Model", "Card SKU", "Card series")}
		if gpu.Name == "" {
			gpu.Name = "AMD GPU"
		}
		// rocm-smi reports VRAM in bytes.
		gpu.VRAMTotalMB = atoi(firstField(fields, "VRAM Total Memory (B)")) / (1 << 20)
		gpu.VRAMUsedMB = atoi(firstField(fields, "VRAM Total Used Memory (B)")) / (1 << 20)
		gpu.UtilPercent = atoi(strings.TrimSuffix(
			firstField(fields, "GPU use (%)"), "%"))
		gpu.TempC = atoi(strings.SplitN(firstField(fields,
			"Temperature (Sensor edge) (C)", "Temperature (Sensor junction) (C)"), ".", 2)[0])
		out = append(out, gpu)
	}
	return out
}

func firstField(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key]; ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// probeIntel finds integrated and discrete Intel graphics through the kernel.
// Whether a particular job can use one depends on that project's oneAPI,
// OpenCL or framework runtime, just as AMD needs ROCm and NVIDIA needs CUDA.
func probeIntel() []GPU { return probeSysfsVendor("0x8086", "intel", true) }

// probeSysfsVendor reads the kernel's own view of the graphics devices, which
// needs no vendor tooling installed.
func probeSysfsVendor(vendorID, vendor string, trainable bool) []GPU {
	out := []GPU{}
	cards, err := filepath.Glob("/sys/class/drm/card[0-9]*")
	if err != nil {
		return out
	}
	sort.Strings(cards)
	for _, card := range cards {
		// Skip connectors such as card0-DP-1, which are outputs not devices.
		if strings.Contains(filepath.Base(card), "-") {
			continue
		}
		if strings.TrimSpace(firstLine(filepath.Join(card, "device/vendor"))) != vendorID {
			continue
		}
		gpu := GPU{Vendor: vendor, Trainable: trainable}
		gpu.Name = strings.TrimSpace(firstLine(filepath.Join(card, "device/product_name")))
		if gpu.Name == "" {
			gpu.Name = vendorName(vendor) + " graphics"
		}
		if total := atoi(firstLine(filepath.Join(card, "device/mem_info_vram_total"))); total > 0 {
			gpu.VRAMTotalMB = total / (1 << 20)
			gpu.VRAMUsedMB = atoi(firstLine(filepath.Join(card, "device/mem_info_vram_used"))) / (1 << 20)
		}
		out = append(out, gpu)
	}
	return out
}

func vendorName(vendor string) string {
	switch vendor {
	case "amd":
		return "AMD"
	case "intel":
		return "Intel"
	case "nvidia":
		return "NVIDIA"
	}
	return vendor
}
