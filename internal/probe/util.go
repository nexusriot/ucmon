package probe

import (
	"fmt"
	"strconv"
	"strings"
)

// suffixIndex is the number trailing a "<prefix><n>" sysfs name, or a large
// sentinel when there is none. Sysfs directory listings come back sorted as
// text, which puts policy10 and thermal_zone10 between their 1 and their 2;
// sorting on this instead keeps them in the order the hardware numbers them.
func suffixIndex(name, prefix string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
	if err != nil {
		return 1 << 30
	}
	return n
}

func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}[exp]
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), suffix)
}

func HumanBytesPerSec(bps float64) string {
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	div, exp := unit, 0
	for n := bps / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB/s", "MiB/s", "GiB/s", "TiB/s", "PiB/s", "EiB/s"}[exp]
	return fmt.Sprintf("%.1f %s", bps/div, suffix)
}

// CounterRate is the per-second rate between two readings of a monotonic
// byte counter. A counter that went backwards — an interface bounced, a
// driver reloaded, a 32-bit counter wrapped — reports 0 instead of the
// ~18 EiB/s that unsigned wrap-around would otherwise produce and that would
// then flatten every sparkline sharing the history buffer.
func CounterRate(cur, prev uint64, dt float64) float64 {
	if dt <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

func ClampHistory[T any](s []T, max int) []T {
	if max <= 0 {
		return s[:0]
	}
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
