package ui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func trunc(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// rule draws a horizontal separator n columns wide, treating a negative count
// as zero. Separator widths are computed from the terminal size, and
// strings.Repeat panics below zero.
func rule(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("─", n)
}

func padRight(s string, w int) string {
	l := utf8.RuneCountInString(s)
	if l >= w {
		return s
	}
	return s + strings.Repeat(" ", w-l)
}

func padTo(width int, s string) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func containsFold(s, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(q))
}

// highlightFold reverse-videos every case-insensitive occurrence of q in s.
// Matching runs entirely on rune slices: a byte-indexed search would drift
// against rune-indexed slicing as soon as the row holds a multi-byte
// character (the "…" trunc appends is one), corrupting the output or slicing
// out of range.
func highlightFold(s, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return s
	}

	rs, lq := []rune(s), lowerRunes([]rune(q))
	ls := lowerRunes(rs)

	var out strings.Builder
	i := 0
	for i < len(rs) {
		j := indexRunes(ls[i:], lq)
		if j < 0 {
			break
		}
		out.WriteString(string(rs[i : i+j]))
		out.WriteString(hlStyle.Render(string(rs[i+j : i+j+len(lq)])))
		i += j + len(lq)
	}
	out.WriteString(string(rs[i:]))

	return out.String()
}

func lowerRunes(in []rune) []rune {
	out := make([]rune, len(in))
	for i, r := range in {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// indexRunes reports the index of the first occurrence of sub in r, or -1.
func indexRunes(r, sub []rune) int {
	for i := 0; i+len(sub) <= len(r); i++ {
		if slices.Equal(r[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func ansiSafeTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var out strings.Builder
	visible := 0

	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) {
					b := s[j]
					if b >= 0x40 && b <= 0x7E {
						j++
						break
					}
					j++
				}
				out.WriteString(s[i:j])
				i = j
				continue
			}
			if i+1 < len(s) && s[i+1] == ']' {
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				out.WriteString(s[i:j])
				i = j
				continue
			}
			if i+1 < len(s) {
				out.WriteByte(s[i])
				out.WriteByte(s[i+1])
				i += 2
			} else {
				out.WriteByte(s[i])
				i++
			}
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}

		w := lipgloss.Width(string(r))
		if visible+w > maxWidth {
			break
		}

		out.WriteRune(r)
		visible += w
		i += size
	}

	return out.String()
}

func hardClipLinesToWidth(s string, w int) string {
	if w <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = ansiSafeTruncate(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

// clipHeight drops any lines beyond maxLines so a tab body can never grow
// taller than its box. Without this, lipgloss renders all lines and the
// frame overflows the terminal, which makes Bubble Tea's alt-screen
// renderer blank the screen on short displays (e.g. the uConsole).
func clipHeight(s string, maxLines int) string {
	if maxLines <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func clampToWidthOneLine(s string, w int) string {
	s = oneLine(s)
	if w <= 0 {
		return ""
	}
	return ansiSafeTruncate(s, w)
}
