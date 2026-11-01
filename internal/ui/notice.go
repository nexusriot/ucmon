package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// noticeTTL is how long a transient status line stays on screen. Sampling
// re-renders at least once per fast tick, so an expired notice disappears
// without needing its own timer.
const noticeTTL = 5 * time.Second

// notice is a status string that fades on its own. Kill confirmations,
// brightness changes and snapshot paths all used to occupy their status line
// until some unrelated keypress happened to overwrite them.
type notice struct {
	text  string
	until time.Time
}

func (n *notice) set(s string) {
	n.text = s
	n.until = time.Now().Add(noticeTTL)
}

func (n *notice) clear() {
	n.text = ""
	n.until = time.Time{}
}

// get returns the message while it is still fresh, else "".
func (n notice) get() string {
	if n.text == "" || time.Now().After(n.until) {
		return ""
	}
	return n.text
}

// errorNoticeMsg puts an error from outside the model (the exporter running
// beside the TUI) in the footer.
type errorNoticeMsg struct{ err error }

// ErrorNotice returns a message that shows err as a footer notice.
func ErrorNotice(err error) tea.Msg { return errorNoticeMsg{err} }
