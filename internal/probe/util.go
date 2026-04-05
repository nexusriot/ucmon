package probe

import (
	"fmt"
	"time"
)

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

func ClampHistory[T any](s []T, max int) []T {
	if max <= 0 {
		return s[:0]
	}
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func Since(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}
