package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTrunc(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"héllo", 4, "hél…"}, // rune-aware
		{"hello", 1, "…"},
		{"hello", 0, ""},
	}
	for _, c := range cases {
		if got := trunc(c.in, c.max); got != c.want {
			t.Errorf("trunc(%q,%d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestPadRightAndPadTo(t *testing.T) {
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight = %q", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight over-width = %q", got)
	}
	if got := padTo(5, "ab"); got != "   ab" {
		t.Errorf("padTo = %q", got)
	}
	if got := padTo(0, "ab"); got != "" {
		t.Errorf("padTo width 0 = %q", got)
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("Firefox", "fire") || !containsFold("firefox", "FOX") {
		t.Error("case-insensitive match failed")
	}
	if !containsFold("anything", "") {
		t.Error("empty query must match")
	}
	if containsFold("abc", "z") {
		t.Error("false positive")
	}
}

func TestHighlightFold(t *testing.T) {
	// No match (or empty query) returns the string unchanged.
	if got := highlightFold("hello", "zz"); got != "hello" {
		t.Errorf("no-match = %q", got)
	}
	if got := highlightFold("hello", ""); got != "hello" {
		t.Errorf("empty query = %q", got)
	}

	// Expected value built with the same style call, so the assertion holds
	// under any terminal color profile.
	want := "he" + hlStyle.Render("LL") + "o"
	if got := highlightFold("heLLo", "ll"); got != want {
		t.Errorf("highlight = %q, want %q", got, want)
	}

	// All occurrences are wrapped.
	want = hlStyle.Render("a") + "b" + hlStyle.Render("a")
	if got := highlightFold("aba", "a"); got != want {
		t.Errorf("multi = %q, want %q", got, want)
	}
}

func TestAnsiSafeTruncate(t *testing.T) {
	// Escape sequences survive truncation and cost no visible width.
	in := "\x1b[31mhello\x1b[0m world"
	got := ansiSafeTruncate(in, 5)
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[0m") {
		t.Errorf("escape sequences dropped: %q", got)
	}
	if lipgloss.Width(got) != 5 {
		t.Errorf("visible width = %d, want 5 (%q)", lipgloss.Width(got), got)
	}

	if got := ansiSafeTruncate("plain", 3); got != "pla" {
		t.Errorf("plain truncate = %q", got)
	}
	if got := ansiSafeTruncate("abc", 0); got != "" {
		t.Errorf("width 0 = %q", got)
	}
}

func TestHardClipLinesToWidth(t *testing.T) {
	in := "short\nthis-is-a-long-line\nx"
	got := hardClipLinesToWidth(in, 6)
	lines := strings.Split(got, "\n")
	if lines[0] != "short" || lines[1] != "this-i" || lines[2] != "x" {
		t.Errorf("clip = %q", lines)
	}
}

func TestClipHeight(t *testing.T) {
	in := "a\nb\nc\nd"
	if got := clipHeight(in, 2); got != "a\nb" {
		t.Errorf("clipHeight = %q", got)
	}
	if got := clipHeight(in, 10); got != in {
		t.Errorf("under-height should be unchanged: %q", got)
	}
	if got := clipHeight(in, 0); got != in {
		t.Errorf("maxLines 0 disables clipping: %q", got)
	}
}

func TestClampToWidthOneLine(t *testing.T) {
	got := clampToWidthOneLine("a\nb\rc", 10)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("newlines survived: %q", got)
	}
	if got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := clampToWidthOneLine("abcdef", 3); got != "abc" {
		t.Errorf("clamp = %q", got)
	}
}

// A multi-byte rune anywhere in the row used to desynchronise highlightFold's
// byte-indexed search from its rune-indexed slicing — corrupting the row or
// panicking with a slice-bounds error. trunc appends "…" routinely, so any
// filter over a truncated process name hit this.
func TestHighlightFoldMultibyte(t *testing.T) {
	cases := []struct{ row, q string }{
		{"aaa… b1", "1"},
		{"…S", "s"},
		{"x…y…z", "z"},
		{"proc…name S", "s"},
		{"  9999  root  verylongprocname…  0.0  0.0  1.0 MB  S", "s"},
		{"héllo wörld", "o"},
		{"└─ systemd", "s"},
		{"日本語プロセス", "語"},
	}
	for _, c := range cases {
		got := stripANSICodes(highlightFold(c.row, c.q))
		if got != c.row {
			t.Errorf("highlightFold(%q, %q) corrupted the row: %q", c.row, c.q, got)
		}
	}
}

func TestHighlightFoldMarksEveryMatch(t *testing.T) {
	out := highlightFold("a…A…a", "a")
	if n := strings.Count(out, hlStyle.Render("a")) + strings.Count(out, hlStyle.Render("A")); n != 3 {
		t.Errorf("expected 3 highlighted matches in %q", out)
	}
	if got := stripANSICodes(out); got != "a…A…a" {
		t.Errorf("row = %q", got)
	}
}

// stripANSICodes removes SGR sequences so a test can compare rendered text.
func stripANSICodes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Separator widths are derived from the terminal size and can go negative;
// strings.Repeat panics there, so rule clamps instead.
func TestRule(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{{-10, ""}, {-1, ""}, {0, ""}, {1, "─"}, {3, "───"}}
	for _, c := range cases {
		if got := rule(c.n); got != c.want {
			t.Errorf("rule(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
