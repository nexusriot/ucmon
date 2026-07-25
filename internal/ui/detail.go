package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ucmon/internal/probe"
)

// procDetailState is the open detail overlay for one process. The overlay
// refreshes on the process tick while it stays open.
type procDetailState struct {
	pid    int32
	name   string // from the list row, shown until the fetch lands
	d      probe.ProcDetail
	loaded bool
	notice string // transient renice / signal status
}

type procDetailMsg struct {
	d   probe.ProcDetail
	err error
}

func fetchProcDetailCmd(pid int32) tea.Cmd {
	return func() tea.Msg {
		d, err := probe.GetProcDetail(pid)
		return procDetailMsg{d: d, err: err}
	}
}

// openProcDetail opens the overlay for the currently selected process.
func (m *Model) openProcDetail() tea.Cmd {
	p, ok := m.selectedProc()
	if !ok {
		return nil
	}
	m.detail = &procDetailState{pid: p.PID, name: p.Name}
	return fetchProcDetailCmd(p.PID)
}

// updateProcDetail handles keys while the overlay is open. The kill-confirm
// prompt is handled by the caller before this runs.
func (m *Model) updateProcDetail(key string) (tea.Cmd, bool) {
	st := m.detail
	switch key {
	case "esc", "q", "enter":
		m.detail = nil
		return nil, true
	case "+", "=":
		return m.reniceDetail(1), true
	case "-", "_":
		return m.reniceDetail(-1), true
	case "p":
		if err := probe.SuspendProcess(st.pid); err != nil {
			st.notice = errStyle.Render("SIGSTOP failed: " + err.Error())
		} else {
			st.notice = warnStyle.Render(fmt.Sprintf("sent SIGSTOP to %d", st.pid))
		}
		return fetchProcDetailCmd(st.pid), true
	case "c":
		if err := probe.ResumeProcess(st.pid); err != nil {
			st.notice = errStyle.Render("SIGCONT failed: " + err.Error())
		} else {
			st.notice = okStyle.Render(fmt.Sprintf("sent SIGCONT to %d", st.pid))
		}
		return fetchProcDetailCmd(st.pid), true
	case "k":
		m.procsKill = &killPrompt{pid: st.pid, name: st.displayName(), hard: false}
		return nil, true
	case "K":
		m.procsKill = &killPrompt{pid: st.pid, name: st.displayName(), hard: true}
		return nil, true
	case "ctrl+c":
		return tea.Quit, true
	}
	return nil, true // overlay swallows everything else
}

func (st *procDetailState) displayName() string {
	if st.loaded && st.d.Name != "" {
		return st.d.Name
	}
	return st.name
}

func (m *Model) reniceDetail(delta int) tea.Cmd {
	st := m.detail
	if !st.loaded {
		return nil
	}
	target := probe.ClampNice(int(st.d.Nice) + delta)
	if err := probe.SetNice(st.pid, target); err != nil {
		st.notice = errStyle.Render(fmt.Sprintf("renice %d failed: %v (negative values need root)", target, err))
		return nil
	}
	st.notice = okStyle.Render(fmt.Sprintf("nice → %d", target))
	return fetchProcDetailCmd(st.pid)
}

func (m Model) viewProcDetail() string {
	w := min(m.w-2, 140)
	bodyH := m.bodyHeight()
	innerW := max(10, w-4)
	st := m.detail

	var b strings.Builder

	title := titleStyle.Render(fmt.Sprintf("Process %d", st.pid))
	if n := st.displayName(); n != "" {
		title += "  " + okStyle.Render(n)
	}

	// Status line: kill prompt > notice > key hint.
	statusLine := subtleStyle.Render("+/- nice • p SIGSTOP • c SIGCONT • k/K kill • esc close")
	if st.notice != "" {
		statusLine = st.notice
	}
	if m.procsKill != nil {
		kp := *m.procsKill
		style := warnStyle
		if kp.hard {
			style = errStyle
		}
		statusLine = style.Render(fmt.Sprintf("Send %s to %s (pid %d)?", kp.signal(), kp.name, kp.pid)) +
			subtleStyle.Render("  y / N")
	}

	b.WriteString(title + "\n")
	b.WriteString(clampToWidthOneLine(statusLine, innerW) + "\n\n")

	if !st.loaded {
		b.WriteString("Loading details…\n")
		return boxStyle.Width(w).Height(bodyH).Render(clipHeight(b.String(), bodyH))
	}

	d := st.d
	row := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString("  " + subtleStyle.Render(padRight(label, 10)) +
			clampToWidthOneLine(value, innerW-12) + "\n")
	}

	row("user", d.User)
	row("status", d.Status)
	if d.CreateTime > 0 {
		started := time.Unix(d.CreateTime/1000, 0)
		row("started", fmt.Sprintf("%s  (up %s)",
			started.Format("2006-01-02 15:04:05"),
			time.Since(started).Truncate(time.Second)))
	}
	row("nice", fmt.Sprintf("%d", d.Nice))
	row("threads", fmt.Sprintf("%d", d.Threads))
	if d.FDs >= 0 {
		row("open fds", fmt.Sprintf("%d", d.FDs))
	}
	if d.Children > 0 {
		row("children", fmt.Sprintf("%d", d.Children))
	}
	mem := fmt.Sprintf("rss %s  vms %s", probe.HumanBytes(d.MemRSS), probe.HumanBytes(d.MemVMS))
	if d.MemSwap > 0 {
		mem += fmt.Sprintf("  swap %s", probe.HumanBytes(d.MemSwap))
	}
	row("memory", mem)
	if d.IOOK {
		row("disk io", fmt.Sprintf("read %s  written %s",
			probe.HumanBytes(d.IORead), probe.HumanBytes(d.IOWrite)))
	}
	row("exe", d.Exe)
	row("cwd", d.Cwd)

	if d.CmdLine != "" {
		b.WriteString("\n  " + subtleStyle.Render("cmdline") + "\n")
		wrapped := lipgloss.NewStyle().Width(innerW - 4).Render(d.CmdLine)
		for _, line := range strings.Split(wrapped, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}

	if len(d.Conns) > 0 {
		b.WriteString("\n  " + subtleStyle.Render(fmt.Sprintf("sockets (%d)", len(d.Conns))) + "\n")
		shown := d.Conns
		if len(shown) > 8 {
			shown = shown[:8]
		}
		for _, c := range shown {
			line := fmt.Sprintf("    %-4s %-24s %-24s %s",
				c.Proto, trunc(c.LocalAddr, 24), trunc(c.RemoteAddr, 24), c.Status)
			b.WriteString(clampToWidthOneLine(line, innerW) + "\n")
		}
		if len(d.Conns) > len(shown) {
			b.WriteString(subtleStyle.Render(fmt.Sprintf("    … %d more", len(d.Conns)-len(shown))) + "\n")
		}
	}

	return boxStyle.Width(w).Height(bodyH).Render(clipHeight(b.String(), bodyH))
}
