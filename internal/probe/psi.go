package probe

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// psiBase is a var so tests can point it at a fake /proc/pressure tree.
var psiBase = "/proc/pressure"

// PSIRes is the pressure-stall reading for one resource. "some" = share of
// wall time at least one task was stalled on the resource; "full" = all
// non-idle tasks were stalled (memory/io only in practice).
type PSIRes struct {
	SomeAvg10  float64
	SomeAvg60  float64
	SomeAvg300 float64
	FullAvg10  float64
	FullAvg60  float64
	FullAvg300 float64
	HasSome    bool
	HasFull    bool
}

type PSISnapshot struct {
	Present bool // kernel exposes /proc/pressure (CONFIG_PSI, psi=1)
	CPU     PSIRes
	Mem     PSIRes
	IO      PSIRes
	TakenAt time.Time
}

// ReadPSI reads /proc/pressure/{cpu,memory,io}. Kernels without PSI simply
// yield Present=false.
func ReadPSI() PSISnapshot {
	snap := PSISnapshot{TakenAt: time.Now()}
	for name, dst := range map[string]*PSIRes{
		"cpu":    &snap.CPU,
		"memory": &snap.Mem,
		"io":     &snap.IO,
	} {
		b, err := os.ReadFile(filepath.Join(psiBase, name))
		if err != nil {
			continue
		}
		*dst = parsePSI(string(b))
		if dst.HasSome || dst.HasFull {
			snap.Present = true
		}
	}
	return snap
}

// parsePSI parses the two-line pressure file format:
//
//	some avg10=0.12 avg60=0.05 avg300=0.01 total=123456
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=7890
func parsePSI(s string) PSIRes {
	var r PSIRes
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var a10, a60, a300 float64
		for _, f := range fields[1:] {
			switch {
			case strings.HasPrefix(f, "avg10="):
				a10, _ = strconv.ParseFloat(f[len("avg10="):], 64)
			case strings.HasPrefix(f, "avg60="):
				a60, _ = strconv.ParseFloat(f[len("avg60="):], 64)
			case strings.HasPrefix(f, "avg300="):
				a300, _ = strconv.ParseFloat(f[len("avg300="):], 64)
			}
		}
		switch fields[0] {
		case "some":
			r.SomeAvg10, r.SomeAvg60, r.SomeAvg300, r.HasSome = a10, a60, a300, true
		case "full":
			r.FullAvg10, r.FullAvg60, r.FullAvg300, r.HasFull = a10, a60, a300, true
		}
	}
	return r
}
