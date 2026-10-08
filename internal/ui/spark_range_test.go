package ui

import (
	"strings"
	"testing"
)

// The reason SparkPct exists: auto-scaling a percentage draws an idle core
// and a saturated one identically, because both span one percentage point.
func TestSparkPctSeparatesIdleFromSaturated(t *testing.T) {
	idle := []float64{2, 3, 2, 3, 2, 3, 2, 3}
	pegged := []float64{97, 98, 97, 98, 97, 98, 97, 98}

	if Spark(idle, 8) != Spark(pegged, 8) {
		t.Fatal("precondition changed: auto-scaled sparklines used to be identical here")
	}

	gotIdle, gotPegged := SparkPct(idle, 8), SparkPct(pegged, 8)
	if gotIdle == gotPegged {
		t.Errorf("SparkPct still draws 2-3%% and 97-98%% the same: %q", gotIdle)
	}
	if !strings.ContainsRune(gotIdle, '▁') {
		t.Errorf("idle series = %q, want the lowest block", gotIdle)
	}
	// 97-98%% is near the top of the ramp but not at it; what matters is that
	// it sits far above the idle series rather than beside it.
	if blockLevel(gotPegged) < 6 {
		t.Errorf("pegged series = %q, want a near-full block", gotPegged)
	}
	if blockLevel(gotIdle) > 1 {
		t.Errorf("idle series = %q, want a near-empty block", gotIdle)
	}
}

// A core pinned at 100%% used to render as the bottom row — the same picture
// as one flat at 0%%.
func TestSparkPctFlatSeriesSitAtTheirLevel(t *testing.T) {
	flat100 := []float64{100, 100, 100, 100}
	flat0 := []float64{0, 0, 0, 0}
	flat50 := []float64{50, 50, 50, 50}

	hi, lo, mid := SparkPct(flat100, 4), SparkPct(flat0, 4), SparkPct(flat50, 4)
	if hi == lo {
		t.Errorf("100%% and 0%% render identically: %q", hi)
	}
	if strings.TrimRight(hi, " ") != "████" {
		t.Errorf("flat 100%% = %q, want a full bar", hi)
	}
	if strings.TrimRight(lo, " ") != "▁▁▁▁" {
		t.Errorf("flat 0%% = %q, want the lowest block", lo)
	}
	if mid == hi || mid == lo {
		t.Errorf("flat 50%% = %q, must sit between 0%% and 100%%", mid)
	}
}

func TestSparkRangeClampsOutliers(t *testing.T) {
	got := SparkRange([]float64{-50, 0, 50, 100, 500}, 5, 0, 100)
	if strings.ContainsRune(got, ' ') {
		t.Errorf("got %q, want five block runes", got)
	}
	runes := []rune(got)
	if runes[0] != '▁' {
		t.Errorf("value below the floor = %q, want the lowest block", string(runes[0]))
	}
	if runes[4] != '█' {
		t.Errorf("value above the ceiling = %q, want the highest block", string(runes[4]))
	}
}

func TestSparkRangeDegenerateAxisFallsBack(t *testing.T) {
	// A zero-width axis cannot place anything; fall back to auto-scaling
	// rather than dividing by it.
	if got := SparkRange([]float64{1, 2, 3}, 3, 5, 5); got == "" {
		t.Error("a degenerate axis produced no output")
	}
}

func TestSparkRangeWidthAndPadding(t *testing.T) {
	for _, n := range []int{1, 3, 8} {
		got := SparkPct([]float64{10, 20, 30}, n)
		if len([]rune(got)) != n {
			t.Errorf("SparkPct(width=%d) returned %d runes: %q", n, len([]rune(got)), got)
		}
	}
	if got := SparkPct(nil, 4); got != "    " {
		t.Errorf("empty series = %q, want four spaces", got)
	}
	if got := SparkPct([]float64{1}, 0); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}

// Only the most recent points are drawn, so a long history still shows now.
func TestSparkPctTakesTail(t *testing.T) {
	vals := make([]float64, 100)
	for i := range vals {
		vals[i] = 0
	}
	vals[99] = 100
	got := SparkPct(vals, 4)
	runes := []rune(got)
	if runes[3] != '█' {
		t.Errorf("last sample = %q, want the newest value at the right edge (%q)", string(runes[3]), got)
	}
}

// blockLevel is the highest block-ramp index present in a sparkline.
func blockLevel(s string) int {
	best := -1
	for _, r := range s {
		for i, b := range blocks {
			if r == b && i > best {
				best = i
			}
		}
	}
	return best
}
