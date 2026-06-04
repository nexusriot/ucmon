package probe

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ClockReading is one named VideoCore clock domain and its frequency in MHz.
type ClockReading struct {
	Name string
	MHz  float64
}

// GPUInfo holds Raspberry Pi / uConsole VideoCore measurements gathered via
// vcgencmd. Available is false on hosts without vcgencmd (e.g. generic x86).
type GPUInfo struct {
	Available bool
	CoreTempC float64        // SoC core temperature
	CoreVolts float64        // core voltage
	Clocks    []ClockReading // arm, core, v3d, h264, isp, …
	MemARMMB  int            // CPU-side memory split
	MemGPUMB  int            // GPU-side memory split
}

// clockDomains are queried in display order. h264/isp/hevc may read 0 when idle.
var clockDomains = []string{"arm", "core", "v3d", "h264", "isp", "hevc"}

// SampleGPU collects VideoCore stats. It never errors: a missing vcgencmd
// simply yields Available=false so the UI can show a friendly message.
func SampleGPU() GPUInfo {
	bin := vcgencmdPath()
	if bin == "" {
		return GPUInfo{}
	}

	g := GPUInfo{Available: true}

	if s, ok := vcgencmd(bin, "measure_temp"); ok {
		// "temp=47.2'C"
		g.CoreTempC = parseAfter(s, "temp=", "'")
	}
	if s, ok := vcgencmd(bin, "measure_volts", "core"); ok {
		// "volt=0.8625V"
		g.CoreVolts = parseAfter(s, "volt=", "V")
	}
	for _, dom := range clockDomains {
		if s, ok := vcgencmd(bin, "measure_clock", dom); ok {
			// "frequency(48)=600000000"
			if eq := strings.IndexByte(s, '='); eq >= 0 {
				if hz, err := strconv.ParseFloat(strings.TrimSpace(s[eq+1:]), 64); err == nil && hz > 0 {
					g.Clocks = append(g.Clocks, ClockReading{Name: dom, MHz: hz / 1e6})
				}
			}
		}
	}
	if s, ok := vcgencmd(bin, "get_mem", "arm"); ok {
		g.MemARMMB = parseMemMB(s) // "arm=948M"
	}
	if s, ok := vcgencmd(bin, "get_mem", "gpu"); ok {
		g.MemGPUMB = parseMemMB(s) // "gpu=76M"
	}

	return g
}

func vcgencmd(bin string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// parseAfter extracts the float between prefix and the suffix delimiter.
func parseAfter(s, prefix, suffix string) float64 {
	i := strings.Index(s, prefix)
	if i < 0 {
		return 0
	}
	rest := s[i+len(prefix):]
	if j := strings.Index(rest, suffix); j >= 0 {
		rest = rest[:j]
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseMemMB parses "gpu=76M" → 76.
func parseMemMB(s string) int {
	if eq := strings.IndexByte(s, '='); eq >= 0 {
		v := strings.TrimSpace(s[eq+1:])
		v = strings.TrimSuffix(v, "M")
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}
