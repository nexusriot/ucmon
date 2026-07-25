package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSparkWidthAndPadding(t *testing.T) {
	if got := Spark(nil, 0); got != "" {
		t.Errorf("width 0 = %q", got)
	}
	if got := Spark(nil, 5); got != "     " {
		t.Errorf("no data should be blank padding, got %q", got)
	}

	// Fewer points than width: rendered then space-padded to width.
	got := Spark([]float64{0, 100}, 6)
	if utf8.RuneCountInString(got) != 6 {
		t.Errorf("padded width = %d runes (%q)", utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "    ") {
		t.Errorf("expected trailing padding, got %q", got)
	}
}

func TestSparkScalesToRange(t *testing.T) {
	got := []rune(Spark([]float64{0, 50, 100}, 3))
	if got[0] != '▁' {
		t.Errorf("min should be the lowest block, got %q", got[0])
	}
	if got[2] != '█' {
		t.Errorf("max should be the highest block, got %q", got[2])
	}
	if got[1] == '▁' || got[1] == '█' {
		t.Errorf("midpoint should be intermediate, got %q", got[1])
	}
}

func TestSparkFlatLine(t *testing.T) {
	got := Spark([]float64{5, 5, 5}, 5)
	if !strings.HasPrefix(got, "▁▁▁") {
		t.Errorf("flat series should render lowest blocks, got %q", got)
	}
	if utf8.RuneCountInString(got) != 5 {
		t.Errorf("flat series not padded to width: %q", got)
	}
}

func TestSparkTakesTail(t *testing.T) {
	// Only the last `width` points are drawn: with the huge value outside
	// the window, the remaining flat tail renders as lowest blocks.
	got := Spark([]float64{1000, 1, 1, 1}, 3)
	if got != "▁▁▁" {
		t.Errorf("tail window = %q, want ▁▁▁", got)
	}
}
