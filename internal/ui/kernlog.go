package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/probe"
)

// kernelLog turns kernel-log events into alert conditions. The kernel says
// things nothing else in ucmon can see: an under-voltage dip between two
// vcgencmd samples, or a card's I/O errors minutes to hours before ext4 gives
// up and remounts read-only.
type kernelLog struct {
	groups map[string]*kernelGroup // active, by alert key
	// Session counts, for the tabs.
	underVoltage     int
	underVoltageLast time.Time
	ioErrors         map[string]int // by device
	ioLast           map[string]time.Time
	errs             int // consecutive failed reads
}

// kernelGroup is one alert's worth of events: repeats extend it rather than
// re-raising, so a card logging a thousand errors a second is one alert.
type kernelGroup struct {
	kind   probe.KernelEventKind
	device string
	first  time.Time
	last   time.Time
	count  int
}

type kmsgMsg struct {
	events []probe.KernelEvent
	err    error
}

// kmsgMaxErrors is how many reads in a row may fail before ucmon stops
// following the log rather than reporting the same failure every tick.
const kmsgMaxErrors = 5

func fetchKmsgCmd(w *probe.KmsgWatcher) tea.Cmd {
	return func() tea.Msg {
		evs, err := w.Read()
		return kmsgMsg{evs, err}
	}
}

// FollowKernelLog starts following /dev/kmsg when the config asks for it and
// the kernel allows it. Only the TUI does: --once and the exporter's
// per-scrape models live too briefly to see an event happen. Where the log
// is unreadable the alerts stay off; `ucmon doctor` says why.
func (m *Model) FollowKernelLog() {
	if !m.cfg.KernelLogEnabled || m.kmsg != nil {
		return
	}
	if w, err := probe.OpenKmsg(); err == nil {
		m.kmsg = w
	}
}

// observe folds new events in and drops groups that have aged out of the
// alert window. hidden reports a disk whose every mount disk_ignore hides:
// its errors are as ignored as its usage.
func (k *kernelLog) observe(evs []probe.KernelEvent, now time.Time, window time.Duration, hidden func(string) bool) {
	if k.groups == nil {
		k.groups = map[string]*kernelGroup{}
		k.ioErrors = map[string]int{}
		k.ioLast = map[string]time.Time{}
	}
	for _, ev := range evs {
		var key string
		switch ev.Kind {
		case probe.KernelUnderVoltage:
			key = "kuv"
			k.underVoltage++
			k.underVoltageLast = ev.At
		case probe.KernelStorageError:
			if hidden(ev.Device) {
				continue
			}
			key = "kio:" + ev.Device
			k.ioErrors[ev.Device]++
			k.ioLast[ev.Device] = ev.At
		default:
			continue // OOM victims go to the oomTracker
		}
		g := k.groups[key]
		if g == nil {
			g = &kernelGroup{kind: ev.Kind, device: ev.Device, first: ev.At}
			k.groups[key] = g
		}
		g.last = ev.At
		g.count++
	}
	for key, g := range k.groups {
		if now.Sub(g.last) >= window {
			delete(k.groups, key)
		}
	}
}

// conds adds a condition per group seen inside the window. mountOf names
// the filesystem on a device, so the alert says "/" and not just mmcblk0p2.
func (k *kernelLog) conds(conds map[string]alertCond, now time.Time, window time.Duration, mountOf func(string) string) {
	for key, g := range k.groups {
		if now.Sub(g.last) >= window {
			continue
		}
		times := ""
		if g.count > 1 {
			times = fmt.Sprintf(" %d× since %s", g.count, g.first.Format("15:04:05"))
		} else {
			times = " at " + g.first.Format("15:04:05")
		}
		switch g.kind {
		case probe.KernelUnderVoltage:
			conds[key] = alertCond{sevWarn,
				"kernel: under-voltage" + times + " — check the power supply and cable"}
		case probe.KernelStorageError:
			dev := g.device
			if mnt := mountOf(dev); mnt != "" {
				dev += " (" + mnt + ")"
			}
			conds[key] = alertCond{sevCrit,
				fmt.Sprintf("kernel: storage I/O errors on %s%s — the card may be failing; back up now", dev, times)}
		}
	}
}

// mountOf returns the mount points on a disk (mmcblk0) or any of its
// partitions (mmcblk0p1, mmcblk0p2), so the alert says what is at risk.
func (m Model) mountOf(disk string) string {
	var mnts []string
	for _, d := range m.disks {
		if name := strings.TrimPrefix(d.Device, "/dev/"); probe.WholeDisk(name) == disk {
			mnts = append(mnts, d.MountPoint)
		}
	}
	sort.Strings(mnts)
	return strings.Join(mnts, ", ")
}

// diskHidden reports whether every mount on a disk is hidden by disk_ignore
// (and it has at least one), from the last unfiltered disk sample.
func (m Model) diskHidden(disk string) bool {
	return m.hiddenDisks[disk]
}

// hiddenDisksOf lists the disks all of whose mounts disk_ignore hides.
func (m Model) hiddenDisksOf(all []probe.DiskInfo) map[string]bool {
	if len(m.cfg.DiskIgnore) == 0 {
		return nil
	}
	shown, hidden := map[string]bool{}, map[string]bool{}
	for _, d := range all {
		disk := probe.WholeDisk(strings.TrimPrefix(d.Device, "/dev/"))
		if m.cfg.HideMount(d.MountPoint) {
			hidden[disk] = true
		} else {
			shown[disk] = true
		}
	}
	for disk := range shown {
		delete(hidden, disk)
	}
	return hidden
}

// handleKmsg applies one drain of the kernel log.
func (m *Model) handleKmsg(msg kmsgMsg) {
	m.done(fetchKmsg)
	w := m.eventWindow()
	for _, ev := range msg.events {
		if ev.Kind == probe.KernelOOMKill {
			m.oom.addVictim(ev.At, ev.PID, ev.Comm, m.memSnap.OOMKillsOK, w)
		}
	}
	m.klog.observe(msg.events, m.alertNow(), w, m.diskHidden)
	if msg.err == nil {
		m.klog.errs = 0
		return
	}
	m.klog.errs++
	if m.klog.errs >= kmsgMaxErrors {
		_ = m.kmsg.Close()
		m.kmsg = nil
		m.setNotice(errStyle.Render(fmt.Sprintf("kernel log: %v — stopped following it", msg.err)))
	}
}

// ioSummary is the Disk tab's one-line session tally, empty when the kernel
// log is not followed or has reported no errors.
func (k kernelLog) ioSummary() string {
	if len(k.ioErrors) == 0 {
		return ""
	}
	devs := make([]string, 0, len(k.ioErrors))
	for d := range k.ioErrors {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	parts := make([]string, len(devs))
	for i, d := range devs {
		parts[i] = fmt.Sprintf("%s %d (last %s)", d, k.ioErrors[d], k.ioLast[d].Format("15:04:05"))
	}
	return "kernel I/O errors this session: " + strings.Join(parts, ", ")
}

func (k kernelLog) underVoltageSummary() string {
	if k.underVoltage == 0 {
		return ""
	}
	return fmt.Sprintf("kernel reported under-voltage %d× this session (last %s)",
		k.underVoltage, k.underVoltageLast.Format("15:04:05"))
}
