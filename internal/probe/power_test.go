package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeSupply(t *testing.T, base, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadPowerSupply(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	fakeSupply(t, base, "axp20x-battery", map[string]string{
		"type":               "Battery",
		"capacity":           "42",
		"status":             "Discharging",
		"technology":         "Li-ion",
		"voltage_now":        "7400000",  // 7.4 V
		"power_now":          "2000000",  // 2 W
		"energy_now":         "10000000", // 10 Wh
		"energy_full":        "20000000",
		"energy_full_design": "25000000",
	})
	fakeSupply(t, base, "axp20x-usb", map[string]string{
		"type":   "USB",
		"online": "1",
	})

	bat, acKnown, acOnline := readPowerSupply()
	if !bat.Present {
		t.Fatal("battery not detected")
	}
	if bat.Capacity != 42 || bat.Status != "Discharging" || bat.Technology != "Li-ion" {
		t.Errorf("got %+v", bat)
	}
	if bat.VoltageV != 7.4 {
		t.Errorf("VoltageV = %v", bat.VoltageV)
	}
	if bat.PowerW != 2 {
		t.Errorf("PowerW = %v", bat.PowerW)
	}
	if bat.TimeLeft != 5*time.Hour { // 10 Wh at 2 W
		t.Errorf("TimeLeft = %v, want 5h", bat.TimeLeft)
	}
	if bat.HealthPct != 80 { // 20 / 25
		t.Errorf("HealthPct = %v, want 80", bat.HealthPct)
	}
	if !acKnown || !acOnline {
		t.Errorf("AC = %v/%v, want known+online", acKnown, acOnline)
	}
}

func TestBatteryHealthChargeFallback(t *testing.T) {
	base := t.TempDir()
	fakeSupply(t, base, "BAT0", map[string]string{
		"charge_full":        "3000000",
		"charge_full_design": "4000000",
	})
	if got := batteryHealth(filepath.Join(base, "BAT0")); got != 75 {
		t.Errorf("health = %v, want 75", got)
	}
	if got := batteryHealth(filepath.Join(base, "nope")); got != 0 {
		t.Errorf("missing counters should yield 0, got %v", got)
	}
}

func TestEstimate(t *testing.T) {
	// Discharging: time to empty = now / rate.
	if d := estimate("Discharging", 1000, 2000, true, true, 500); d != 2*time.Hour {
		t.Errorf("discharging = %v, want 2h", d)
	}
	// Charging: time to full = (full - now) / rate.
	if d := estimate("Charging", 1000, 2000, true, true, 500); d != 2*time.Hour {
		t.Errorf("charging = %v, want 2h", d)
	}
	if d := estimate("Full", 1000, 2000, true, true, 500); d != 0 {
		t.Errorf("full = %v, want 0", d)
	}
	if d := estimate("Discharging", 1000, 2000, true, true, 0); d != 0 {
		t.Errorf("zero rate = %v, want 0", d)
	}
}
