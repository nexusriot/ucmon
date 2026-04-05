package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ucmon/internal/probe"
)

const version = "0.1.0"

type tab int

const (
	tabCPU  tab = iota // CPU load & temperatures
	tabProc            // processes
	tabDisk            // disk usage
	tabNet             // network
	headerH = 1
	footerH = 1
)

// messages
type tickMsg time.Time
type cpuMsg probe.CPUSnapshot
type procMsg []probe.ProcInfo
type diskMsg struct {
	disks []probe.DiskInfo
	ios   []probe.DiskIOInfo
}
type netMsg probe.NetSnapshot
type connMsg []probe.ConnInfo
type errMsg struct{ error }

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type Model struct {
	w, h int

	activeTab tab
	err       error

	// CPU tab
	cpuSnap     probe.CPUSnapshot
	cpuHist     []float64 // total CPU history
	tempHist    map[string][]float64
	coreHists   [][]float64

	// Processes tab
	procs      []probe.ProcInfo
	procsVP    viewport.Model
	procsText  string
	procsSearch    textinput.Model
	procsSearching bool
	procsQuery     string

	// Disk tab
	disks   []probe.DiskInfo
	diskIOs []probe.DiskIOInfo
	diskSampler *probe.DiskSampler

	// Network tab
	netSampler  *probe.NetSampler
	netSnap     probe.NetSnapshot
	conns       []probe.ConnInfo
	netVP       viewport.Model
	netText     string
	netSearch    textinput.Model
	netSearching bool
	netQuery     string
	rxHist      map[string][]float64
	txHist      map[string][]float64

	tickCount int
}

func NewModel() Model {
	pvp := viewport.New(0, 0)
	nvp := viewport.New(0, 0)

	ps := textinput.New()
	ps.Placeholder = "search process name / pid"
	ps.Prompt = "/ "
	ps.CharLimit = 64

	ns := textinput.New()
	ns.Placeholder = "search connection / process"
	ns.Prompt = "/ "
	ns.CharLimit = 64

	return Model{
		activeTab:   tabCPU,
		tempHist:    map[string][]float64{},
		diskSampler: probe.NewDiskSampler(),
		netSampler:  probe.NewNetSampler(),
		procsVP:     pvp,
		netVP:       nvp,
		procsSearch: ps,
		netSearch:   ns,
		rxHist:      map[string][]float64{},
		txHist:      map[string][]float64{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchCPUCmd(),
		fetchProcsCmd(),
		m.fetchDiskCmd(),
		fetchNetCmd(m.netSampler),
		fetchConnsCmd(),
		tickEvery(1*time.Second),
	)
}

func fetchCPUCmd() tea.Cmd {
	return func() tea.Msg {
		snap, err := probe.SampleCPU()
		if err != nil {
			return errMsg{err}
		}
		return cpuMsg(snap)
	}
}

func fetchProcsCmd() tea.Cmd {
	return func() tea.Msg {
		procs, err := probe.ListProcesses(100)
		if err != nil {
			return errMsg{err}
		}
		return procMsg(procs)
	}
}

func (m Model) fetchDiskCmd() tea.Cmd {
	sampler := m.diskSampler
	return func() tea.Msg {
		disks, ios, err := sampler.Sample(1.0)
		if err != nil {
			return errMsg{err}
		}
		return diskMsg{disks, ios}
	}
}

func fetchNetCmd(s *probe.NetSampler) tea.Cmd {
	return func() tea.Msg {
		snap, err := s.Sample()
		if err != nil {
			return errMsg{err}
		}
		return netMsg(snap)
	}
}

func fetchConnsCmd() tea.Cmd {
	return func() tea.Msg {
		conns, err := probe.ListConnections()
		if err != nil {
			return errMsg{err}
		}
		return connMsg(conns)
	}
}

func (m Model) bodyHeight() int {
	return max(8, m.h-headerH-footerH-2)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		bodyH := m.bodyHeight()

		vpW := min(m.w-2, 140)
		m.procsVP.Width = max(10, vpW-2)
		m.procsVP.Height = max(5, bodyH-4)
		m.netVP.Width = max(10, vpW-2)
		m.netVP.Height = max(5, bodyH-4)

		m.procsVP.SetContent(hardClipLinesToWidth(m.procsText, m.procsVP.Width))
		m.netVP.SetContent(hardClipLinesToWidth(m.netText, m.netVP.Width))
		return m, nil

	case tickMsg:
		m.tickCount++
		cmds := []tea.Cmd{
			fetchCPUCmd(),
			tickEvery(1 * time.Second),
		}
		if m.tickCount%3 == 0 {
			cmds = append(cmds, fetchProcsCmd(), fetchConnsCmd())
		}
		if m.tickCount%5 == 0 {
			cmds = append(cmds, m.fetchDiskCmd(), fetchNetCmd(m.netSampler))
		} else {
			cmds = append(cmds, fetchNetCmd(m.netSampler))
		}
		return m, tea.Batch(cmds...)

	case cpuMsg:
		m.cpuSnap = probe.CPUSnapshot(msg)
		m.err = nil

		m.cpuHist = append(m.cpuHist, m.cpuSnap.TotalPercent)
		m.cpuHist = probe.ClampHistory(m.cpuHist, 200)

		// per-core history
		for len(m.coreHists) < len(m.cpuSnap.PerCorePercent) {
			m.coreHists = append(m.coreHists, nil)
		}
		for i, pct := range m.cpuSnap.PerCorePercent {
			m.coreHists[i] = append(m.coreHists[i], pct)
			m.coreHists[i] = probe.ClampHistory(m.coreHists[i], 200)
		}

		// temp history
		for _, t := range m.cpuSnap.Temperatures {
			m.tempHist[t.Label] = append(m.tempHist[t.Label], t.Temp)
			m.tempHist[t.Label] = probe.ClampHistory(m.tempHist[t.Label], 200)
		}
		return m, nil

	case procMsg:
		m.procs = msg
		m.procsText = m.renderProcsText()
		m.procsVP.SetContent(hardClipLinesToWidth(m.procsText, m.procsVP.Width))
		return m, nil

	case diskMsg:
		m.disks = msg.disks
		m.diskIOs = msg.ios
		return m, nil

	case netMsg:
		m.netSnap = probe.NetSnapshot(msg)
		for _, ii := range m.netSnap.Ifaces {
			if !ii.IsUp {
				continue
			}
			m.rxHist[ii.Name] = append(m.rxHist[ii.Name], ii.RxBps)
			m.rxHist[ii.Name] = probe.ClampHistory(m.rxHist[ii.Name], 200)
			m.txHist[ii.Name] = append(m.txHist[ii.Name], ii.TxBps)
			m.txHist[ii.Name] = probe.ClampHistory(m.txHist[ii.Name], 200)
		}
		return m, nil

	case connMsg:
		m.conns = msg
		m.netText = m.renderNetText()
		m.netVP.SetContent(hardClipLinesToWidth(m.netText, m.netVP.Width))
		return m, nil

	case errMsg:
		m.err = msg.error
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab", "right":
			m.activeTab = (m.activeTab + 1) % 4
			return m, nil
		case "shift+tab", "left":
			m.activeTab = (m.activeTab + 3) % 4
			return m, nil
		case "1":
			m.activeTab = tabCPU
			return m, nil
		case "2":
			m.activeTab = tabProc
			return m, nil
		case "3":
			m.activeTab = tabDisk
			return m, nil
		case "4":
			m.activeTab = tabNet
			return m, nil
		case "/":
			if m.activeTab == tabProc {
				m.procsSearching = true
				m.procsSearch.Focus()
				m.procsSearch.SetValue(m.procsQuery)
				return m, nil
			}
			if m.activeTab == tabNet {
				m.netSearching = true
				m.netSearch.Focus()
				m.netSearch.SetValue(m.netQuery)
				return m, nil
			}
		case "ctrl+u":
			if m.activeTab == tabProc && !m.procsSearching {
				m.procsQuery = ""
				m.procsSearch.SetValue("")
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				return m, nil
			}
			if m.activeTab == tabNet && !m.netSearching {
				m.netQuery = ""
				m.netSearch.SetValue("")
				m.netText = m.renderNetText()
				m.netVP.SetContent(m.netText)
				return m, nil
			}
		}
	}

	// search mode: processes
	if m.activeTab == tabProc && m.procsSearching {
		var cmd tea.Cmd
		m.procsSearch, cmd = m.procsSearch.Update(msg)
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.procsQuery = strings.TrimSpace(m.procsSearch.Value())
				m.procsSearching = false
				m.procsSearch.Blur()
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				return m, nil
			case "esc":
				m.procsSearching = false
				m.procsSearch.Blur()
				return m, nil
			case "ctrl+u":
				m.procsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// search mode: network
	if m.activeTab == tabNet && m.netSearching {
		var cmd tea.Cmd
		m.netSearch, cmd = m.netSearch.Update(msg)
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.netQuery = strings.TrimSpace(m.netSearch.Value())
				m.netSearching = false
				m.netSearch.Blur()
				m.netText = m.renderNetText()
				m.netVP.SetContent(m.netText)
				return m, nil
			case "esc":
				m.netSearching = false
				m.netSearch.Blur()
				return m, nil
			case "ctrl+u":
				m.netSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// scroll: processes
	if m.activeTab == tabProc {
		var cmd tea.Cmd
		m.procsVP, cmd = m.procsVP.Update(msg)
		return m, cmd
	}

	// scroll: network
	if m.activeTab == tabNet {
		var cmd tea.Cmd
		m.netVP, cmd = m.netVP.Update(msg)
		return m, cmd
	}

	return m, nil
}

// ── View ──

func (m Model) View() string {
	header := m.renderHeader()

	var body string
	switch m.activeTab {
	case tabCPU:
		body = m.viewCPU()
	case tabProc:
		body = m.viewProcs()
	case tabDisk:
		body = m.viewDisk()
	case tabNet:
		body = m.viewNet()
	}

	footer := subtleStyle.Render("Keys: tab/←/→ switch • 1-4 jump • / search • ctrl+u clear • ctrl+c quit")
	if m.err != nil {
		footer = errStyle.Render("Error: " + m.err.Error())
	}

	footer = clampToWidthOneLine(footer, m.w)
	footer = lipgloss.NewStyle().Width(m.w).Render(footer)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer) + "\x1b[0m"
}

func (m Model) renderHeader() string {
	tabs := []string{
		renderTab("1 CPU/Temp", m.activeTab == tabCPU),
		renderTab("2 Processes", m.activeTab == tabProc),
		renderTab("3 Disk", m.activeTab == tabDisk),
		renderTab("4 Network", m.activeTab == tabNet),
	}

	left := titleStyle.Render(fmt.Sprintf("ucmon %s", version)) + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))

	rem := m.w - lipgloss.Width(left)
	if rem < 0 {
		rem = 0
	}

	right := joinTabsWithinWidth(tabs, rem)

	line := left + padTo(rem, right)
	return lipgloss.NewStyle().Width(m.w).Render(line)
}

func renderTab(s string, active bool) string {
	if active {
		return selectedStyle.Padding(0, 1).Render(s)
	}
	return subtleStyle.Padding(0, 1).Render(s)
}

func joinTabsWithinWidth(tabs []string, maxW int) string {
	if maxW <= 0 || len(tabs) == 0 {
		return ""
	}

	var out strings.Builder
	used := 0
	sep := " "

	for i, t := range tabs {
		tw := lipgloss.Width(t)
		addSep := i > 0
		sepW := 0
		if addSep {
			sepW = lipgloss.Width(sep)
		}

		if used+sepW+tw > maxW {
			ell := subtleStyle.Render("…")
			ellW := lipgloss.Width(ell)
			if used > 0 && used+sepW+ellW <= maxW {
				out.WriteString(sep)
				out.WriteString(ell)
			} else if used == 0 && ellW <= maxW {
				out.WriteString(ell)
			}
			break
		}

		if addSep {
			out.WriteString(sep)
			used += sepW
		}

		out.WriteString(t)
		used += tw
	}

	return out.String()
}

// ── Tab 1: CPU / Temperature ──

func (m Model) viewCPU() string {
	bodyH := m.bodyHeight()
	w := min(m.w-2, 140)

	var b strings.Builder

	// Hostname & uptime from net snapshot if available
	if m.netSnap.Hostname != "" {
		b.WriteString(fmt.Sprintf("Host: %s  Uptime: %s\n",
			okStyle.Render(m.netSnap.Hostname),
			m.netSnap.Uptime.Truncate(time.Second)))
	}

	if m.cpuSnap.TakenAt.IsZero() {
		b.WriteString("Collecting CPU data…\n")
		return boxStyle.Width(w).Height(bodyH).Render(b.String())
	}

	b.WriteString(fmt.Sprintf("Time: %s\n\n", m.cpuSnap.TakenAt.Format("2006-01-02 15:04:05")))

	// Total CPU
	cpuColor := "42"
	if m.cpuSnap.TotalPercent >= 80 {
		cpuColor = "196"
	} else if m.cpuSnap.TotalPercent >= 50 {
		cpuColor = "214"
	}
	cpuStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(cpuColor))

	chartW := max(10, w-20)
	b.WriteString(titleStyle.Render("CPU Total") + " " + cpuStyle.Render(fmt.Sprintf("%.1f%%", m.cpuSnap.TotalPercent)) + "\n")
	b.WriteString(RenderBarWithLabel("", m.cpuSnap.TotalPercent, min(40, chartW)) + "\n")
	b.WriteString(Spark(m.cpuHist, min(chartW, 60)) + "\n\n")

	// Per-core
	if len(m.cpuSnap.PerCorePercent) > 0 {
		b.WriteString(titleStyle.Render("Per Core") + "\n")
		coreChartW := min(30, chartW/2)
		for i, pct := range m.cpuSnap.PerCorePercent {
			label := fmt.Sprintf("Core %d", i)
			bar := RenderBarWithLabel(label, pct, coreChartW)
			spark := ""
			if i < len(m.coreHists) {
				spark = Spark(m.coreHists[i], min(20, chartW/3))
			}
			b.WriteString(fmt.Sprintf("  %-8s %s %s\n", label, bar, spark))
		}
		b.WriteString("\n")
	}

	// Temperatures (high priority)
	if len(m.cpuSnap.Temperatures) > 0 {
		b.WriteString(titleStyle.Render("Temperatures") + "\n")
		tempChartW := min(30, chartW/2)
		for _, t := range m.cpuSnap.Temperatures {
			color := probe.TempColor(t.Temp)
			tStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
			label := trunc(t.Label, 20)
			tempBar := RenderBar(t.Temp, tempChartW, color)
			spark := ""
			if hist, ok := m.tempHist[t.Label]; ok {
				spark = Spark(hist, min(20, chartW/3))
			}
			b.WriteString(fmt.Sprintf("  %-20s %s %s %s\n",
				label,
				tStyle.Render(probe.FormatTemp(t.Temp)),
				tempBar,
				spark,
			))
		}
	} else {
		b.WriteString(subtleStyle.Render("No temperature sensors detected") + "\n")
	}

	return boxStyle.Width(w).Height(bodyH).Render(b.String())
}

// ── Tab 2: Processes ──

func (m Model) viewProcs() string {
	procsW := min(m.w-2, 140)
	procsH := m.bodyHeight()

	searchLine := subtleStyle.Render("Press / to search")
	if m.procsQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.procsQuery) + subtleStyle.Render("  (/ change, ctrl+u clear)")
	}
	if m.procsSearching {
		searchLine = m.procsSearch.View()
	}

	content := searchLine + "\n\n" + m.procsVP.View()
	return boxStyle.Width(procsW).Height(procsH).Render(content)
}

func (m Model) renderProcsText() string {
	var b strings.Builder

	w := m.procsVP.Width
	if w <= 0 {
		w = 120
	}

	colPID := 7
	colUser := 10
	colCPU := 7
	colMem := 7
	colRSS := 10
	colStat := 3
	colName := max(12, w-colPID-colUser-colCPU-colMem-colRSS-colStat-14)
	if colName > 30 {
		colName = 30
	}

	b.WriteString("Processes (sorted by CPU)\n")
	b.WriteString("Scroll: ↑↓ PgUp/PgDn\n\n")

	h := fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s\n",
		padRight("PID", colPID),
		padRight("USER", colUser),
		padRight("NAME", colName),
		padRight("CPU%", colCPU),
		padRight("MEM%", colMem),
		padRight("RSS", colRSS),
		padRight("S", colStat),
	)
	b.WriteString(h)
	b.WriteString(strings.Repeat("─", min(w, colPID+colUser+colName+colCPU+colMem+colRSS+colStat+12)) + "\n")

	if len(m.procs) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.procsQuery
	for _, p := range m.procs {
		name := p.Name
		if name == "" {
			name = "-"
		}
		user := p.User
		if user == "" {
			user = "-"
		}

		if q != "" && !(containsFold(name, q) || containsFold(fmt.Sprintf("%d", p.PID), q) || containsFold(user, q)) {
			continue
		}

		pidS := padRight(trunc(fmt.Sprintf("%d", p.PID), colPID), colPID)
		userS := padRight(trunc(user, colUser), colUser)
		nameS := padRight(trunc(name, colName), colName)
		cpuS := padRight(fmt.Sprintf("%5.1f", p.CPUPct), colCPU)
		memS := padRight(fmt.Sprintf("%5.1f", p.MemPct), colMem)
		rssS := padRight(probe.HumanBytes(p.MemRSS), colRSS)
		statS := padRight(p.Status, colStat)

		nameS = highlightFold(nameS, q)
		pidS = highlightFold(pidS, q)

		b.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s\n",
			pidS, userS, nameS, cpuS, memS, rssS, statS))
	}

	return b.String()
}

// ── Tab 3: Disk ──

func (m Model) viewDisk() string {
	bodyH := m.bodyHeight()
	w := min(m.w-2, 140)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Disk Usage") + "\n\n")

	if len(m.disks) == 0 {
		b.WriteString("Collecting disk data…\n")
		return boxStyle.Width(w).Height(bodyH).Render(b.String())
	}

	barW := min(30, w/3)
	colDev := 16
	colMount := max(12, min(24, w/5))
	colFS := 8
	colTotal := 9
	colUsed := 9
	colFree := 9

	h := fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s\n",
		padRight("DEVICE", colDev),
		padRight("MOUNT", colMount),
		padRight("FS", colFS),
		padRight("TOTAL", colTotal),
		padRight("USED", colUsed),
		padRight("FREE", colFree),
		"USAGE",
	)
	b.WriteString(h)
	b.WriteString(strings.Repeat("─", min(w-4, colDev+colMount+colFS+colTotal+colUsed+colFree+barW+20)) + "\n")

	for _, d := range m.disks {
		dev := trunc(d.Device, colDev)
		mount := trunc(d.MountPoint, colMount)
		bar := RenderBarWithLabel("", d.UsedPct, barW)

		b.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s\n",
			padRight(dev, colDev),
			padRight(mount, colMount),
			padRight(d.FSType, colFS),
			padRight(probe.HumanBytes(d.Total), colTotal),
			padRight(probe.HumanBytes(d.Used), colUsed),
			padRight(probe.HumanBytes(d.Free), colFree),
			bar,
		))
	}

	// Disk I/O
	if len(m.diskIOs) > 0 {
		b.WriteString("\n" + titleStyle.Render("Disk I/O") + "\n\n")
		b.WriteString(fmt.Sprintf("%-16s  %12s  %12s\n", "DEVICE", "READ/s", "WRITE/s"))
		b.WriteString(strings.Repeat("─", 44) + "\n")
		for _, io := range m.diskIOs {
			if io.ReadBps == 0 && io.WriteBps == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("%-16s  %12s  %12s\n",
				trunc(io.Device, 16),
				probe.HumanBytesPerSec(io.ReadBps),
				probe.HumanBytesPerSec(io.WriteBps),
			))
		}
	}

	return boxStyle.Width(w).Height(bodyH).Render(b.String())
}

// ── Tab 4: Network ──

func (m Model) viewNet() string {
	bodyH := m.bodyHeight()
	w := min(m.w-2, 140)

	var b strings.Builder

	// Interface summary with sparklines
	b.WriteString(titleStyle.Render("Interfaces") + "\n\n")

	if m.netSnap.TakenAt.IsZero() {
		b.WriteString("Collecting network data…\n\n")
	} else {
		chartW := min(25, w/4)
		for _, ii := range m.netSnap.Ifaces {
			if !ii.IsUp {
				continue
			}
			b.WriteString(fmt.Sprintf("  %s  %s",
				okStyle.Render(padRight(ii.Name, 12)),
				subtleStyle.Render(padRight(ii.Hardware, 18)),
			))
			if len(ii.Addrs) > 0 {
				b.WriteString(subtleStyle.Render(trunc(ii.Addrs[0], 20)))
			}
			b.WriteString("\n")
			rxSpark := Spark(m.rxHist[ii.Name], chartW)
			txSpark := Spark(m.txHist[ii.Name], chartW)
			b.WriteString(fmt.Sprintf("    RX: %-12s %s\n", probe.HumanBytesPerSec(ii.RxBps), rxSpark))
			b.WriteString(fmt.Sprintf("    TX: %-12s %s\n", probe.HumanBytesPerSec(ii.TxBps), txSpark))
		}
		b.WriteString("\n")
	}

	// Connections section uses viewport
	netW := min(m.w-2, 140)
	netH := max(5, bodyH-lipgloss.Height(b.String())-4)

	searchLine := subtleStyle.Render("Press / to search connections")
	if m.netQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.netQuery) + subtleStyle.Render("  (/ change, ctrl+u clear)")
	}
	if m.netSearching {
		searchLine = m.netSearch.View()
	}

	// Combine iface summary + connections in one box
	connView := searchLine + "\n" + m.netVP.View()

	content := b.String() + boxStyle.Width(netW-4).Height(netH).Render(connView)
	return boxStyle.Width(w).Height(bodyH).Render(content)
}

func (m Model) renderNetText() string {
	var b strings.Builder

	w := m.netVP.Width
	if w <= 0 {
		w = 120
	}

	colProto := 4
	colLocal := min(28, max(16, w/4))
	colRemote := min(28, max(16, w/4))
	colStatus := 12
	colPID := 7

	b.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s\n",
		padRight("PR", colProto),
		padRight("LOCAL", colLocal),
		padRight("REMOTE", colRemote),
		padRight("STATUS", colStatus),
		padRight("PID", colPID),
		"PROCESS",
	))
	b.WriteString(strings.Repeat("─", min(w, colProto+colLocal+colRemote+colStatus+colPID+20)) + "\n")

	if len(m.conns) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.netQuery
	for _, c := range m.conns {
		proc := c.Process
		if proc == "" {
			proc = "-"
		}

		if q != "" && !(containsFold(c.LocalAddr, q) || containsFold(c.RemoteAddr, q) ||
			containsFold(proc, q) || containsFold(c.Status, q) || containsFold(c.Proto, q)) {
			continue
		}

		rest := max(5, w-(colProto+colLocal+colRemote+colStatus+colPID+12))
		procS := trunc(proc, rest)

		localS := padRight(trunc(c.LocalAddr, colLocal), colLocal)
		remoteS := padRight(trunc(c.RemoteAddr, colRemote), colRemote)

		localS = highlightFold(localS, q)
		remoteS = highlightFold(remoteS, q)
		procS = highlightFold(procS, q)

		b.WriteString(fmt.Sprintf("%s  %s  %s  %s  %s  %s\n",
			padRight(c.Proto, colProto),
			localS,
			remoteS,
			padRight(c.Status, colStatus),
			padRight(fmt.Sprintf("%d", c.PID), colPID),
			procS,
		))
	}

	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
