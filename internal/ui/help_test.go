package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestTwoColumnsKeepsSectionsWhole(t *testing.T) {
	blocks := []string{"A\na1\na2\n", "B\nb1\n", "C\nc1\nc2\nc3\n"}
	out := twoColumns(blocks, 10)
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	left := func(r string) string { return strings.TrimSpace(r[:min(10, len(r))]) }
	var col []string
	for _, r := range rows {
		col = append(col, left(r))
	}
	got := strings.Join(col, ",")
	if !strings.HasPrefix(got, "A,a1,a2,,B,b1") {
		t.Errorf("left column = %q, want sections A and B whole", got)
	}
	if !strings.Contains(rows[0], "C") || !strings.Contains(rows[3], "c3") {
		t.Errorf("right column does not hold section C whole:\n%s", out)
	}
}

// At the uConsole's 120 columns every description fits or wraps — nothing
// is cut off mid-word as "pause sampling (alerts keep r" was.
func TestHelpOverlayLosesNothing(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {160, 30}, {160, 48}} {
		m := testModel()
		next, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		m = next.(Model)
		m.showHelp = true
		v := ansiRE.ReplaceAllString(m.viewHelp(), "")
		if strings.Contains(v, "…") {
			t.Errorf("%dx%d: a help line was truncated:\n%s", sz[0], sz[1], v)
		}
		for _, sec := range helpSections {
			for _, k := range sec.keys {
				words := strings.Fields(k[1])
				if !strings.Contains(v, words[len(words)-1]) {
					t.Errorf("%dx%d: the end of %q is missing", sz[0], sz[1], k[1])
				}
			}
		}
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
