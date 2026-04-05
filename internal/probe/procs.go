package probe

import (
	"fmt"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcInfo struct {
	PID     int32
	Name    string
	CPUPct  float64
	MemPct  float32
	MemRSS  uint64
	Status  string
	User    string
	CmdLine string
}

func ListProcesses(topN int) ([]ProcInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var out []ProcInfo
	for _, p := range procs {
		name, _ := p.Name()
		cpuPct, _ := p.CPUPercent()
		memPct, _ := p.MemoryPercent()
		memInfo, _ := p.MemoryInfo()
		status, _ := p.Status()
		user, _ := p.Username()
		cmdline, _ := p.Cmdline()

		var rss uint64
		if memInfo != nil {
			rss = memInfo.RSS
		}

		statusStr := ""
		if len(status) > 0 {
			statusStr = status[0]
		}

		if len(cmdline) > 120 {
			cmdline = cmdline[:120]
		}

		out = append(out, ProcInfo{
			PID:     p.Pid,
			Name:    name,
			CPUPct:  cpuPct,
			MemPct:  memPct,
			MemRSS:  rss,
			Status:  statusStr,
			User:    user,
			CmdLine: cmdline,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CPUPct > out[j].CPUPct
	})

	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}

	return out, nil
}

func FormatProcCPU(pct float64) string {
	return fmt.Sprintf("%.1f%%", pct)
}

func FormatProcMem(pct float32) string {
	return fmt.Sprintf("%.1f%%", pct)
}

func FormatCreateTime(createTime int64) string {
	t := time.Unix(createTime/1000, 0)
	return t.Format("15:04:05")
}
