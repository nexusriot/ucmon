package probe

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// backlightBase is a var so tests can point it at a fake sysfs tree.
var backlightBase = "/sys/class/backlight"

// BacklightInfo describes a display backlight exposed via sysfs. On the
// uConsole the DSI panel registers here; on a generic laptop it's the panel
// backlight; on a headless host Present is false.
type BacklightInfo struct {
	Present    bool
	Device     string
	Brightness int
	Max        int
	Percent    int  // Brightness / Max * 100
	Writable   bool // true if the brightness file is writable by this process
}

// ReadBacklight returns the first backlight device (or the named device when
// override is non-empty). Missing sysfs simply yields Present=false.
func ReadBacklight(override string) BacklightInfo {
	dev := override
	if dev == "" {
		entries, err := os.ReadDir(backlightBase)
		if err != nil || len(entries) == 0 {
			return BacklightInfo{}
		}
		dev = pickBacklight(entries)
	}

	dir := filepath.Join(backlightBase, dev)
	cur, ok1 := sysReadInt(filepath.Join(dir, "brightness"))
	maxB, ok2 := sysReadInt(filepath.Join(dir, "max_brightness"))
	if !ok1 || !ok2 || maxB <= 0 {
		return BacklightInfo{}
	}

	// Round, don't truncate: SetBacklightPercent maps a percent back to a raw
	// value, and a truncating read-back can land a step below where it was
	// just set, so small backlight_step values would never advance.
	pct := int(math.Round(float64(cur) / float64(maxB) * 100))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	return BacklightInfo{
		Present:    true,
		Device:     dev,
		Brightness: int(cur),
		Max:        int(maxB),
		Percent:    pct,
		Writable:   writable(filepath.Join(dir, "brightness")),
	}
}

// pickBacklight chooses a backlight device when the config names none.
//
// Taking the first directory entry means taking the alphabetically first,
// which on a laptop is "acpi_video0" ahead of "intel_backlight" — and
// acpi_video is the one that commonly ignores writes. The kernel's own
// preference order is native panel drivers first, firmware interfaces last,
// so this follows it. The uConsole has a single device either way.
func pickBacklight(entries []os.DirEntry) string {
	rank := func(name string) int {
		switch {
		case strings.HasPrefix(name, "acpi_video"):
			return 2 // firmware interface: last resort
		case name == "intel_backlight" || name == "amdgpu_bl0" || name == "nv_backlight":
			return 0 // native panel driver
		default:
			return 1
		}
	}
	best := entries[0].Name()
	for _, e := range entries[1:] {
		if rank(e.Name()) < rank(best) {
			best = e.Name()
		}
	}
	return best
}

// SetBacklightPercent writes a 0–100 brightness percentage to the device's
// sysfs brightness file. Returns a descriptive error on permission failure so
// the UI can hint that a udev rule (or running as root) is needed.
func SetBacklightPercent(device string, pct int) error {
	if device == "" {
		return fmt.Errorf("no backlight device")
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	dir := filepath.Join(backlightBase, device)
	maxB, ok := sysReadInt(filepath.Join(dir, "max_brightness"))
	if !ok || maxB <= 0 {
		return fmt.Errorf("cannot read max_brightness")
	}

	val := int(float64(pct) / 100 * float64(maxB))
	if val < 1 && pct > 0 {
		val = 1 // never fully blank the screen on a tiny positive percent
	}

	path := filepath.Join(dir, "brightness")
	if err := os.WriteFile(path, []byte(strconv.Itoa(val)), 0644); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied (needs root or a udev rule on %s)", path)
		}
		return err
	}
	return nil
}

// writable reports whether path can be opened for writing without changing it.
func writable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// BacklightUdevRule grants the "video" group write access to every
// backlight's brightness file, which is what the GPU tab's +/- keys need
// from a non-root user. Printed verbatim by the doctor report.
const BacklightUdevRule = `# /etc/udev/rules.d/90-ucmon-backlight.rules
ACTION=="add", SUBSYSTEM=="backlight", RUN+="/bin/chgrp video /sys/class/backlight/%k/brightness", RUN+="/bin/chmod g+w /sys/class/backlight/%k/brightness"

sudo usermod -aG video "$USER"   # then log out and back in
sudo udevadm control --reload && sudo udevadm trigger --subsystem-match=backlight`

// CheckBacklightWrite reports whether the panel brightness can be read and
// changed. Read-only is the interesting case: the GPU tab shows the level and
// refuses the +/- keys, which looks like a broken key rather than a
// permission.
func CheckBacklightWrite(override string) Capability {
	c := Capability{
		Name:    "backlight brightness",
		Source:  backlightBase,
		Affects: "the GPU tab's +/- keys (and the brightness row)",
	}

	bl := ReadBacklight(override)
	if !bl.Present {
		st, why := pathStatus(backlightBase)
		c.Status = st
		switch st {
		case CapPresent:
			c.Status = CapAbsent
			c.Detail = "no device under " + backlightBase + " reports brightness/max_brightness"
		default:
			c.Detail = why + " — headless host, or a panel with no sysfs backlight"
		}
		return c
	}

	c.Source = filepath.Join(backlightBase, bl.Device, "brightness")
	if !bl.Writable {
		c.Status = CapReadOnly
		c.Detail = fmt.Sprintf("%s reads %d%% but is not writable by this user", bl.Device, bl.Percent)
		c.Fix = BacklightUdevRule
		return c
	}
	c.Status = CapPresent
	c.Detail = fmt.Sprintf("%s at %d%% (%d/%d), writable", bl.Device, bl.Percent, bl.Brightness, bl.Max)
	return c
}
