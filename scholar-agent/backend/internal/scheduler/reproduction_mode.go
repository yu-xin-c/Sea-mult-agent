package scheduler

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"scholar-agent-backend/internal/models"
)

const (
	reproductionModeAuto  = "auto"
	reproductionModeSmoke = "smoke"
	reproductionModeFull  = "full"
)

type ReproductionModeDecision struct {
	RequestedMode string                    `json:"requested_mode"`
	EffectiveMode string                    `json:"effective_mode"`
	FullEligible  bool                      `json:"full_eligible"`
	Reasons       []string                  `json:"reasons,omitempty"`
	Probe         ReproductionResourceProbe `json:"probe"`
}

type ReproductionResourceProbe struct {
	CPUCount   int            `json:"cpu_count"`
	MemoryGB   float64        `json:"memory_gb"`
	DiskFreeGB float64        `json:"disk_free_gb"`
	GPUCount   int            `json:"gpu_count"`
	GPUNames   []string       `json:"gpu_names,omitempty"`
	Thresholds map[string]any `json:"thresholds"`
}

func decideReproductionMode(task *models.Task, workspacePath string) ReproductionModeDecision {
	requestedMode := normalizeReproductionMode(chooseNonEmpty(
		os.Getenv("PAPER_REPRO_MODE"),
		taskInputValue(task, "requested_reproduction_mode"),
	))
	if requestedMode == "" {
		requestedMode = reproductionModeAuto
	}
	fullRequested := taskBoolInput(task, "full_reproduction_requested") || requestedMode == reproductionModeFull
	return DecidePaperReproductionMode(requestedMode, fullRequested, workspacePath)
}

func DecidePaperReproductionMode(requestedMode string, fullRequested bool, workspacePath string) ReproductionModeDecision {
	requestedMode = normalizeReproductionMode(chooseNonEmpty(os.Getenv("PAPER_REPRO_MODE"), requestedMode))
	if requestedMode == "" {
		requestedMode = reproductionModeAuto
	}

	probe := probeReproductionResources(workspacePath)
	eligible, reasons := probe.fullReproductionEligible()
	fullRequested = fullRequested || requestedMode == reproductionModeFull

	decision := ReproductionModeDecision{
		RequestedMode: requestedMode,
		EffectiveMode: reproductionModeSmoke,
		FullEligible:  eligible,
		Reasons:       append([]string(nil), reasons...),
		Probe:         probe,
	}

	switch requestedMode {
	case reproductionModeSmoke:
		decision.Reasons = append([]string{"smoke mode explicitly requested"}, decision.Reasons...)
	case reproductionModeFull:
		if eligible {
			decision.EffectiveMode = reproductionModeFull
			decision.Reasons = []string{"full reproduction enabled: local resources satisfy configured thresholds"}
		} else {
			decision.Reasons = append([]string{"full reproduction requested but local resources are insufficient"}, decision.Reasons...)
		}
	default:
		if fullRequested && eligible {
			decision.EffectiveMode = reproductionModeFull
			decision.Reasons = []string{"auto mode enabled full reproduction because the request asks for a full/BLEU run and local resources satisfy thresholds"}
		} else if !fullRequested {
			decision.Reasons = append([]string{"auto mode kept smoke reproduction because full/BLEU run was not requested"}, decision.Reasons...)
		}
	}

	return decision
}

func normalizeReproductionMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "auto", "default":
		return reproductionModeAuto
	case "smoke", "minimal", "mini", "quick", "最小", "快速":
		return reproductionModeSmoke
	case "full", "complete", "bleu", "完整", "全量":
		return reproductionModeFull
	default:
		return reproductionModeAuto
	}
}

func probeReproductionResources(workspacePath string) ReproductionResourceProbe {
	memoryGB := detectMemoryGB()
	diskGB := detectDiskFreeGB(chooseNonEmpty(workspacePath, os.TempDir()))
	gpuNames := detectCUDAGPUNames()
	return ReproductionResourceProbe{
		CPUCount:   runtime.NumCPU(),
		MemoryGB:   roundOneDecimal(memoryGB),
		DiskFreeGB: roundOneDecimal(diskGB),
		GPUCount:   len(gpuNames),
		GPUNames:   gpuNames,
		Thresholds: map[string]any{
			"min_cpu_count":    intEnv("PAPER_REPRO_FULL_MIN_CPU", 16),
			"min_memory_gb":    floatEnv("PAPER_REPRO_FULL_MIN_MEMORY_GB", 64),
			"min_disk_free_gb": floatEnv("PAPER_REPRO_FULL_MIN_DISK_GB", 100),
			"min_cuda_gpu":     intEnv("PAPER_REPRO_FULL_MIN_GPU", 1),
		},
	}
}

func (p ReproductionResourceProbe) fullReproductionEligible() (bool, []string) {
	reasons := []string{}
	minCPU := intEnv("PAPER_REPRO_FULL_MIN_CPU", 16)
	minMemory := floatEnv("PAPER_REPRO_FULL_MIN_MEMORY_GB", 64)
	minDisk := floatEnv("PAPER_REPRO_FULL_MIN_DISK_GB", 100)
	minGPU := intEnv("PAPER_REPRO_FULL_MIN_GPU", 1)

	if p.CPUCount < minCPU {
		reasons = append(reasons, fmt.Sprintf("cpu_count=%d < required=%d", p.CPUCount, minCPU))
	}
	if p.MemoryGB < minMemory {
		reasons = append(reasons, fmt.Sprintf("memory_gb=%.1f < required=%.1f", p.MemoryGB, minMemory))
	}
	if p.DiskFreeGB < minDisk {
		reasons = append(reasons, fmt.Sprintf("disk_free_gb=%.1f < required=%.1f", p.DiskFreeGB, minDisk))
	}
	if p.GPUCount < minGPU {
		reasons = append(reasons, fmt.Sprintf("cuda_gpu_count=%d < required=%d", p.GPUCount, minGPU))
	}
	return len(reasons) == 0, reasons
}

func detectMemoryGB() float64 {
	switch runtime.GOOS {
	case "darwin":
		raw, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			if bytes, parseErr := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64); parseErr == nil {
				return bytes / 1024 / 1024 / 1024
			}
		}
	case "linux":
		raw, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if !strings.HasPrefix(line, "MemTotal:") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, parseErr := strconv.ParseFloat(fields[1], 64); parseErr == nil {
						return kb / 1024 / 1024
					}
				}
			}
		}
	}
	return 0
}

func detectDiskFreeGB(path string) float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return float64(stat.Bavail) * float64(stat.Bsize) / 1024 / 1024 / 1024
}

func detectCUDAGPUNames() []string {
	raw, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
	if err != nil {
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func taskBoolInput(task *models.Task, key string) bool {
	if task == nil || task.Inputs == nil {
		return false
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true") || strings.TrimSpace(typed) == "1"
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(typed)), "true") || strings.TrimSpace(fmt.Sprint(typed)) == "1"
	}
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func floatEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func roundOneDecimal(value float64) float64 {
	return float64(int(value*10+0.5)) / 10
}
