package ui

import "strings"

// sparkline blocks: low -> high
var blocks = []rune("▁▂▃▄▅▆▇█")

// Spark renders a sparkline scaled to the range the data itself covers. Use
// it for unbounded series — bytes/s, MHz, RTT — where there is no meaningful
// ceiling to scale against and the shape is what matters.
func Spark(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	values = tailValues(values, width)

	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV <= 1e-9 {
		return strings.Repeat(string(blocks[0]), len(values)) + strings.Repeat(" ", width-len(values))
	}
	return renderSpark(values, width, minV, maxV)
}

// SparkPct renders a sparkline on a fixed 0-100 axis, for every reading that
// is a percentage of something.
//
// Auto-scaling such a series is actively misleading: a core idling between
// 2% and 3% and one pinned between 97% and 98% both span one percentage
// point, so both drew the identical full-height zigzag, and a core flat at
// 100% drew the *lowest* row — the same picture as one flat at 0%. A fixed
// axis costs the fine detail of a narrow band and buys back the one thing a
// glance is for.
func SparkPct(values []float64, width int) string {
	return SparkRange(values, width, 0, 100)
}

// SparkRange renders a sparkline on an explicit axis. Values outside the axis
// are clamped to it rather than stretching it.
func SparkRange(values []float64, width int, lo, hi float64) string {
	if width <= 0 {
		return ""
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	if hi-lo <= 1e-9 {
		return Spark(values, width) // a degenerate axis is no axis
	}
	return renderSpark(tailValues(values, width), width, lo, hi)
}

// tailValues keeps the most recent width points.
func tailValues(values []float64, width int) []float64 {
	if len(values) > width {
		return values[len(values)-width:]
	}
	return values
}

// renderSpark maps values onto the block ramp between lo and hi, padding the
// result out to width.
func renderSpark(values []float64, width int, lo, hi float64) string {
	span := hi - lo
	var b strings.Builder
	b.Grow(width * 3)
	for _, v := range values {
		n := (v - lo) / span
		idx := int(n * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	if len(values) < width {
		b.WriteString(strings.Repeat(" ", width-len(values)))
	}
	return b.String()
}
