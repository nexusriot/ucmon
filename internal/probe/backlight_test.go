package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBacklight(t *testing.T, base, dev string, brightness, max string) {
	t.Helper()
	dir := filepath.Join(base, dev)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brightness"), []byte(brightness+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "max_brightness"), []byte(max+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBacklight(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "128", "255")

	bl := ReadBacklight("") // auto-detect first device
	if !bl.Present {
		t.Fatal("backlight not detected")
	}
	if bl.Device != "panel0" || bl.Brightness != 128 || bl.Max != 255 {
		t.Errorf("got %+v", bl)
	}
	if bl.Percent != 50 { // 128/255 → 50.19 → int 50
		t.Errorf("Percent = %d, want 50", bl.Percent)
	}
	if !bl.Writable {
		t.Error("temp file should be writable")
	}

	if bl := ReadBacklight("missing-device"); bl.Present {
		t.Error("named override for a missing device must not be Present")
	}
}

func TestReadBacklightAbsent(t *testing.T) {
	old := backlightBase
	backlightBase = filepath.Join(t.TempDir(), "nope")
	defer func() { backlightBase = old }()

	if bl := ReadBacklight(""); bl.Present {
		t.Error("Present without sysfs dir")
	}
}

func TestSetBacklightPercent(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "255", "255")

	if err := SetBacklightPercent("panel0", 50); err != nil {
		t.Fatalf("SetBacklightPercent: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(base, "panel0", "brightness"))
	if strings.TrimSpace(string(b)) != "127" { // 50% of 255
		t.Errorf("brightness file = %q, want 127", string(b))
	}

	// Values are clamped to 0–100.
	if err := SetBacklightPercent("panel0", 150); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(base, "panel0", "brightness"))
	if strings.TrimSpace(string(b)) != "255" {
		t.Errorf("clamped brightness = %q, want 255", string(b))
	}

	// A tiny positive percentage must never fully blank the panel.
	if err := SetBacklightPercent("panel0", 1); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(base, "panel0", "brightness"))
	if strings.TrimSpace(string(b)) == "0" {
		t.Error("1% wrote raw 0 — would blank the screen")
	}

	if err := SetBacklightPercent("", 50); err == nil {
		t.Error("empty device should error")
	}
	if err := SetBacklightPercent("missing", 50); err == nil {
		t.Error("missing device should error")
	}
}

// SetBacklightPercent and ReadBacklight must round-trip: a truncating read-back
// used to land a step below what was just written, so pressing "+" with a small
// backlight_step oscillated instead of climbing.
func TestBacklightPercentRoundTrip(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "255", "255")

	for pct := 0; pct <= 100; pct++ {
		if err := SetBacklightPercent("panel0", pct); err != nil {
			t.Fatalf("set %d%%: %v", pct, err)
		}
		bl := ReadBacklight("panel0")
		if !bl.Present {
			t.Fatalf("%d%%: device vanished", pct)
		}
		if bl.Percent != pct {
			t.Errorf("set %d%% read back %d%% (raw %d/%d)", pct, bl.Percent, bl.Brightness, bl.Max)
		}
	}
}

// Every step must strictly advance the reported percentage, at any step size.
func TestBacklightStepAlwaysAdvances(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "0", "255")

	prev := ReadBacklight("panel0").Percent
	for i := 0; i < 100; i++ {
		if err := SetBacklightPercent("panel0", prev+1); err != nil {
			t.Fatal(err)
		}
		got := ReadBacklight("panel0").Percent
		if got <= prev {
			t.Fatalf("step from %d%% landed on %d%% — brightness is stuck", prev, got)
		}
		prev = got
	}
}

// With no override ReadBacklight prefers a real panel driver over the ACPI
// firmware interface. Taking the lexically first entry picked acpi_video0 on
// a laptop, which is routinely the one that ignores writes.
func TestReadBacklightPrefersPanelOverACPI(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "10", "100")
	fakeBacklight(t, base, "acpi_video0", "80", "100")

	bl := ReadBacklight("")
	if bl.Device != "panel0" || bl.Percent != 10 {
		t.Errorf("auto-detect chose %+v, want panel0 at 10%%", bl)
	}
	if bl := ReadBacklight("acpi_video0"); bl.Device != "acpi_video0" || bl.Percent != 80 {
		t.Errorf("override chose %+v, want acpi_video0 at 80%%", bl)
	}
}

func TestPickBacklightOrder(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	// A native driver outranks both the generic and the firmware interface,
	// whatever order the directory lists them in.
	fakeBacklight(t, base, "acpi_video0", "1", "100")
	fakeBacklight(t, base, "intel_backlight", "2", "100")
	fakeBacklight(t, base, "zz_other", "3", "100")

	if got := ReadBacklight("").Device; got != "intel_backlight" {
		t.Errorf("picked %q, want intel_backlight", got)
	}

	// With only a firmware interface present it is still better than nothing.
	base2 := t.TempDir()
	backlightBase = base2
	fakeBacklight(t, base2, "acpi_video0", "40", "100")
	if got := ReadBacklight("").Device; got != "acpi_video0" {
		t.Errorf("picked %q, want acpi_video0 as the only device", got)
	}
}
