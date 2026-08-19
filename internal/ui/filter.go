package ui

import "github.com/nexusriot/ucmon/internal/probe"

// keepTemps drops the sensors hidden by temp_ignore. It runs where the
// snapshot lands rather than in the view, so history, session peaks, the alert
// engine and the --once report all see the same filtered set.
func (m Model) keepTemps(in []probe.TempReading) []probe.TempReading {
	if len(m.cfg.TempIgnore) == 0 {
		return in
	}
	out := in[:0:0]
	for _, t := range in {
		if !m.cfg.HideSensor(t.Label) {
			out = append(out, t)
		}
	}
	return out
}

// keepDisks drops the filesystems hidden by disk_ignore, matching on the
// mount point.
func (m Model) keepDisks(in []probe.DiskInfo) []probe.DiskInfo {
	if len(m.cfg.DiskIgnore) == 0 {
		return in
	}
	out := in[:0:0]
	for _, d := range in {
		if !m.cfg.HideMount(d.MountPoint) {
			out = append(out, d)
		}
	}
	return out
}

// keepIfaces drops the interfaces hidden by net_ignore.
func (m Model) keepIfaces(in []probe.IfaceInfo) []probe.IfaceInfo {
	if len(m.cfg.NetIgnore) == 0 {
		return in
	}
	out := in[:0:0]
	for _, ii := range in {
		if !m.cfg.HideIface(ii.Name) {
			out = append(out, ii)
		}
	}
	return out
}
