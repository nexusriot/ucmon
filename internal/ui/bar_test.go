package ui

import (
	"strings"
	"testing"
)

func countRune(s string, r rune) int {
	n := 0
	for _, c := range s {
		if c == r {
			n++
		}
	}
	return n
}

func TestRenderBarFill(t *testing.T) {
	bar := RenderBar(50, 10, "42")
	if got := countRune(bar, '█'); got != 5 {
		t.Errorf("50%% of 10 = %d filled, want 5", got)
	}
	if got := countRune(bar, '░'); got != 5 {
		t.Errorf("empty cells = %d, want 5", got)
	}
}

func TestRenderBarClamps(t *testing.T) {
	if got := countRune(RenderBar(150, 10, "42"), '█'); got != 10 {
		t.Errorf(">100%% should fill the bar, got %d", got)
	}
	if got := countRune(RenderBar(-5, 10, "42"), '█'); got != 0 {
		t.Errorf("negative should be empty, got %d", got)
	}
	if got := RenderBar(50, 0, "42"); got != "" {
		t.Errorf("zero width = %q", got)
	}
}

func TestRenderBarWithLabel(t *testing.T) {
	out := RenderBarWithLabel("", 42.5, 30)
	if !strings.Contains(out, "42.5%") {
		t.Errorf("label missing: %q", out)
	}
	// Even a tiny width keeps a minimum 5-cell bar.
	out = RenderBarWithLabel("", 100, 4)
	if got := countRune(out, '█'); got != 5 {
		t.Errorf("minimum bar width = %d, want 5", got)
	}
}
