package ui

import "strings"

// sparkline blocks: low -> high
var blocks = []rune("▁▂▃▄▅▆▇█")

func Spark(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	// take last width points
	if len(values) > width {
		values = values[len(values)-width:]
	}
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	// avoid div-by-zero
	span := maxV - minV
	if span <= 1e-9 {
		return strings.Repeat(string(blocks[0]), len(values)) + strings.Repeat(" ", width-len(values))
	}

	var b strings.Builder
	for _, v := range values {
		n := (v - minV) / span
		idx := int(n * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	// pad to width
	if len(values) < width {
		b.WriteString(strings.Repeat(" ", width-len(values)))
	}
	return b.String()
}
