package ui

import (
	"strings"
	"testing"
)

// Every key the Update handler acts on must be documented in the overlay.
func TestHelpContentCoversKeys(t *testing.T) {
	content := helpContent()
	for _, key := range []string{
		"space", "?", "w", "g", "enter", "k / K", "+ / -", "p / c",
		"j / k", "f", "/", "m", "c", "ctrl+u", "s", "t",
	} {
		if !strings.Contains(content, key) {
			t.Errorf("help does not mention %q", key)
		}
	}
}

func TestTwoColumns(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	out := twoColumns(lines, 10, 5)
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !strings.Contains(rows[0], "a") || !strings.Contains(rows[0], "c") {
		t.Errorf("row 0 = %q", rows[0])
	}
}
