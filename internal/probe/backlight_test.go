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
