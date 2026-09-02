// Package sysinfo probes the host for the resources the cluster cares about.
//
// Everything here degrades rather than fails: a machine with no GPU, no
// /proc/loadavg or no nvidia-smi reports what it can and leaves the rest empty.
// A worker that cannot describe itself is still a worker.
package sysinfo

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// GPU is a single accelerator visible on the host.
type GPU struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	VRAMTotalMB int    `json:"vram_total_mb"`
	VRAMUsedMB  int    `json:"vram_used_mb"`
	UtilPercent int    `json:"util_percent"`
	TempC       int    `json:"temp_c"`
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

// probeGPUs asks nvidia-smi for the accelerators on this host. A machine with
// no NVIDIA driver simply has no GPUs, which is not an error.
func probeGPUs() []GPU {
	out := []GPU{}
	bin, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--query-gpu=index,name,memory.total,memory.used,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 6 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
		out = append(out, GPU{
			Index:       atoi(parts[0]),
			Name:        parts[1],
			VRAMTotalMB: atoi(parts[2]),
			VRAMUsedMB:  atoi(parts[3]),
			UtilPercent: atoi(parts[4]),
			TempC:       atoi(parts[5]),
		})
	}
	return out
}
