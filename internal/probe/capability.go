package probe

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// CapStatus is the outcome of one capability check. Absent and denied are
// kept apart deliberately: both leave the same empty row on a tab, and they
// have nothing in common as problems — one needs different hardware or a
// different kernel, the other needs a group membership or a udev rule.
type CapStatus int

const (
	CapAbsent   CapStatus = iota // the source does not exist on this host
	CapPresent                   // readable, and it produced data
	CapDenied                    // it exists, but this user may not read it
	CapReadOnly                  // readable, but this user may not write it
)

func (s CapStatus) String() string {
	switch s {
	case CapPresent:
		return "present"
	case CapDenied:
		return "denied"
	case CapReadOnly:
		return "read-only"
	default:
		return "absent"
	}
}

// Capability is one source's answer to "why is that row empty". Each probe
// fills one in from the same files it samples, so the answer cannot drift
// from the behaviour it explains; formatting belongs to the caller.
type Capability struct {
	Name    string // what the source provides, e.g. "GPU / VideoCore"
	Source  string // the file, directory or binary that was consulted
	Status  CapStatus
	Detail  string // what was found, or why nothing was
	Affects string // what ucmon cannot show without it
	Fix     string // how to obtain it; empty when there is nothing to do
}

// OK reports whether the capability is fully usable.
func (c Capability) OK() bool { return c.Status == CapPresent }

// Capabilities runs every check, in the order the tabs present the readings.
// backlightDevice is the config's backlight_device override ("" to
// auto-detect), because the device the doctor reports on must be the one the
// +/- keys would write to. Nothing here writes anything or samples more than
// once: it is safe to call on a loaded machine, as an unprivileged user.
func Capabilities(backlightDevice string) []Capability {
	return []Capability{
		CheckTempSensors(),
		CheckCPUFreq(),
		CheckGovernorWrite(),
		CheckPSI(),
		CheckProcIO(),
		CheckBattery(),
		CheckBatteryHealth(),
		CheckVideoCore(),
		CheckBacklightWrite(backlightDevice),
		CheckIW(),
		CheckModemManager(),
		CheckUnprivilegedICMP(),
		CheckStorageWear(),
	}
}

// pathStatus classifies a filesystem source by opening it: readable, present
// but refused, or not there at all.
func pathStatus(path string) (CapStatus, string) {
	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		return CapPresent, ""
	}
	if os.IsPermission(err) {
		return CapDenied, errReason(err)
	}
	return CapAbsent, errReason(err)
}

// errReason is the bare syscall message — os.PathError repeats the path that
// the Source field already carries.
func errReason(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// plural counts a thing without the "(s)" shorthand, which reads badly in a
// report meant to be pasted into a bug thread.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// firstN joins up to n names, marking how many it left out, so a check on a
// host with twenty sensors still prints one line.
func firstN(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + ", …"
}
