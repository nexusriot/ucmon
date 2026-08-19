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

// The uConsole's axp20x driver exposes charge/current counters rather than the
// energy/power pair, so the fallback branch is the one that actually runs on
// the target hardware.
func TestBatteryRateChargeCurrentFallback(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	fakeSupply(t, base, "axp20x-battery", map[string]string{
		"type":               "Battery",
		"capacity":           "50",
		"status":             "Discharging",
		"voltage_now":        "3700000", // 3.7 V
		"current_now":        "1000000", // 1 A
		"charge_now":         "2000000", // 2 Ah
		"charge_full":        "4000000",
		"charge_full_design": "5000000",
	})

	bat, _, _ := readPowerSupply()
	if !bat.Present {
		t.Fatal("battery not detected")
	}
	if bat.PowerW != 3.7 { // 1 A x 3.7 V
		t.Errorf("PowerW = %v, want 3.7", bat.PowerW)
	}
	if bat.TimeLeft != 2*time.Hour { // 2 Ah at 1 A
		t.Errorf("TimeLeft = %v, want 2h", bat.TimeLeft)
	}
	if bat.HealthPct != 80 { // 4000000 / 5000000
		t.Errorf("HealthPct = %v, want 80", bat.HealthPct)
	}
}

// Charging estimates the time to full, not to empty, and a negative counter
// (some drivers sign the discharge direction) must not invert the rate.
func TestBatteryRateChargingAndSignedCounters(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	fakeSupply(t, base, "bat", map[string]string{
		"type":        "Battery",
		"capacity":    "25",
		"status":      "Charging",
		"voltage_now": "3700000",
		"current_now": "-2000000", // signed discharge convention, 2 A
		"charge_now":  "1000000",  // 1 Ah
		"charge_full": "5000000",  // 5 Ah -> 4 Ah to go -> 2h
	})

	bat, _, _ := readPowerSupply()
	if bat.PowerW != 7.4 { // |−2 A| x 3.7 V
		t.Errorf("PowerW = %v, want 7.4 from a signed counter", bat.PowerW)
	}
	if bat.TimeLeft != 2*time.Hour {
		t.Errorf("TimeLeft = %v, want 2h to full", bat.TimeLeft)
	}
}

// A zero rate gives no estimate rather than a division by zero, and a status
// that is neither charging nor discharging gets none either.
func TestBatteryRateNoEstimate(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	for _, c := range []struct {
		name  string
		files map[string]string
	}{
		{"zero current", map[string]string{
			"type": "Battery", "capacity": "50", "status": "Discharging",
			"voltage_now": "3700000", "current_now": "0", "charge_now": "2000000",
		}},
		{"full", map[string]string{
			"type": "Battery", "capacity": "100", "status": "Full",
			"voltage_now": "3700000", "current_now": "1000000", "charge_now": "4000000",
			"charge_full": "4000000",
		}},
		{"no counters at all", map[string]string{
			"type": "Battery", "capacity": "50", "status": "Discharging",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(base, c.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			powerSupplyBase = dir
			fakeSupply(t, dir, "bat", c.files)

			bat, _, _ := readPowerSupply()
			if bat.TimeLeft != 0 {
				t.Errorf("TimeLeft = %v, want no estimate", bat.TimeLeft)
			}
		})
	}
}

// Only the first battery is used, and a non-battery supply must not be mistaken
// for one. Entries are read in lexical order.
func TestReadPowerSupplyFirstBatteryWins(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	fakeSupply(t, base, "b-second", map[string]string{"type": "Battery", "capacity": "20"})
	fakeSupply(t, base, "a-first", map[string]string{"type": "Battery", "capacity": "80"})
	fakeSupply(t, base, "c-mains", map[string]string{"type": "Mains", "online": "0"})

	bat, acKnown, acOnline := readPowerSupply()
	if bat.Name != "a-first" || bat.Capacity != 80 {
		t.Errorf("got %+v, want the lexically first battery", bat)
	}
	if !acKnown || acOnline {
		t.Errorf("acKnown=%v acOnline=%v, want known and offline", acKnown, acOnline)
	}
}

// No /sys/class/power_supply at all (a VM, a container) yields a clean absence.
func TestReadPowerSupplyMissingTree(t *testing.T) {
	old := powerSupplyBase
	powerSupplyBase = filepath.Join(t.TempDir(), "nope")
	defer func() { powerSupplyBase = old }()

	bat, acKnown, acOnline := readPowerSupply()
	if bat.Present || acKnown || acOnline {
		t.Errorf("got %+v acKnown=%v acOnline=%v", bat, acKnown, acOnline)
	}
	if bat.Capacity != -1 {
		t.Errorf("Capacity = %d, want -1 for unknown", bat.Capacity)
	}
}
