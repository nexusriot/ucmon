package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// cpufreqBase is a var so tests can point it at a fake sysfs tree.
var cpufreqBase = "/sys/devices/system/cpu/cpufreq"

// PolicyFreq is one cpufreq policy — a group of cores sharing a frequency
// domain (on the Pi/uConsole a single policy0 covers all cores).
type PolicyFreq struct {
	Policy    string // e.g. "policy0"
	CPUs      string // affected cpu list, e.g. "0 1 2 3"
	CurMHz    float64
	MinMHz    float64
	MaxMHz    float64
	Governor  string
	Available []string // scaling_available_governors
	Writable  bool     // scaling_governor writable by this process
}

// ReadCPUFreq returns one entry per cpufreq policy, sorted by name. Hosts
// without cpufreq support (many VMs and containers) yield an empty slice.
func ReadCPUFreq() []PolicyFreq {
	entries, err := os.ReadDir(cpufreqBase)
	if err != nil {
		return nil
	}

	var out []PolicyFreq
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "policy") {
			continue
		}
		dir := filepath.Join(cpufreqBase, e.Name())

		cur, ok := sysReadInt(filepath.Join(dir, "scaling_cur_freq"))
		if !ok {
			continue
		}
		pf := PolicyFreq{Policy: e.Name(), CurMHz: float64(cur) / 1000} // kHz -> MHz

		if v, ok := sysReadInt(filepath.Join(dir, "cpuinfo_min_freq")); ok {
			pf.MinMHz = float64(v) / 1000
		}
		if v, ok := sysReadInt(filepath.Join(dir, "cpuinfo_max_freq")); ok {
			pf.MaxMHz = float64(v) / 1000
		}
		if s, ok := sysReadStr(filepath.Join(dir, "affected_cpus")); ok {
			pf.CPUs = s
		}
		if s, ok := sysReadStr(filepath.Join(dir, "scaling_governor")); ok {
			pf.Governor = s
		}
		if s, ok := sysReadStr(filepath.Join(dir, "scaling_available_governors")); ok {
			pf.Available = strings.Fields(s)
		}
		pf.Writable = writable(filepath.Join(dir, "scaling_governor"))

		out = append(out, pf)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Policy < out[j].Policy })
	return out
}

// NextGovernor returns the governor after cur in avail, wrapping around.
// An empty avail returns cur; a cur not present in avail returns avail[0].
func NextGovernor(cur string, avail []string) string {
	if len(avail) == 0 {
		return cur
	}
	for i, g := range avail {
		if g == cur {
			return avail[(i+1)%len(avail)]
		}
	}
	return avail[0]
}

// SetGovernor writes gov to the policy's scaling_governor. Returns a
// descriptive error on permission failure so the UI can hint at the fix.
func SetGovernor(policy, gov string) error {
	if policy == "" || gov == "" {
		return fmt.Errorf("no cpufreq policy")
	}
	path := filepath.Join(cpufreqBase, policy, "scaling_governor")
	if err := os.WriteFile(path, []byte(gov), 0644); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied (needs root or a udev rule on %s)", path)
		}
		return err
	}
	return nil
}
