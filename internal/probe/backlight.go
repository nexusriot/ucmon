package probe

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
		dev = entries[0].Name()
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
