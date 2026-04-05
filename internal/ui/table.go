package ui

import (
	"strings"
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

func highlightFold(s, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return s
	}

	rs := []rune(s)
	rq := []rune(q)

	ls := strings.ToLower(string(rs))
	lq := strings.ToLower(string(rq))

	var out strings.Builder
	i := 0
	for {
		j := strings.Index(ls[i:], lq)
		if j < 0 {
			out.WriteString(string(rs[i:]))
			break
		}
		j += i

		out.WriteString(string(rs[i:j]))

		end := j + len([]rune(lq))
		if end > len(rs) {
			end = len(rs)
		}
		out.WriteString(hlStyle.Render(string(rs[j:end])))

		i = end
	}

	return out.String()
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
