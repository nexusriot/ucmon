package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromMissingFileReturnsDefaults(t *testing.T) {
	cfg := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	def := Default()
	if cfg != def {
		t.Errorf("missing file: got %+v, want defaults %+v", cfg, def)
	}
}

func TestLoadFromParsesAllKeys(t *testing.T) {
	path := writeConfig(t, `
# comment
[section]           ; ignored
start_tab = "net"
fast_interval = "500ms"
proc_interval = 2
slow_interval = "10s"
temp_warn  = 45
temp_hot   = 60
temp_crit  = 75
backlight_step = 5
backlight_device = "panel0"
alerts_enabled = yes
mem_warn_pct  = 80
swap_warn_pct = 40
disk_warn_pct = 85
batt_low_pct  = 20
batt_crit_pct = 10
load_warn_factor = 1.5
alert_log_cap = 100
alert_clear_ticks = 5
alert_bell = off
alert_cmd = "notify-send ucmon"
alert_log_file = "/tmp/ucmon-alerts.log"
wifi_low_dbm = -75
ping_enabled = true
ping_target = "1.1.1.1"
ping_loss_warn_pct = 20
psi_warn_pct = 40
not_a_key = whatever
broken line without equals
`)
	cfg := LoadFrom(path)

	if cfg.StartTab != "net" {
		t.Errorf("StartTab = %q", cfg.StartTab)
	}
	if cfg.FastInterval != 500*time.Millisecond {
		t.Errorf("FastInterval = %v", cfg.FastInterval)
	}
	if cfg.ProcInterval != 2*time.Second {
		t.Errorf("ProcInterval = %v (bare number should mean seconds)", cfg.ProcInterval)
	}
	if cfg.SlowInterval != 10*time.Second {
		t.Errorf("SlowInterval = %v", cfg.SlowInterval)
	}
	if cfg.TempWarn != 45 || cfg.TempHot != 60 || cfg.TempCrit != 75 {
		t.Errorf("temps = %v/%v/%v", cfg.TempWarn, cfg.TempHot, cfg.TempCrit)
	}
	if cfg.BacklightStep != 5 || cfg.BacklightDevice != "panel0" {
		t.Errorf("backlight = %d/%q", cfg.BacklightStep, cfg.BacklightDevice)
	}
	if !cfg.AlertsEnabled {
		t.Error("AlertsEnabled should parse 'yes'")
	}
	if cfg.MemWarnPct != 80 || cfg.SwapWarnPct != 40 || cfg.DiskWarnPct != 85 {
		t.Errorf("mem/swap/disk = %v/%v/%v", cfg.MemWarnPct, cfg.SwapWarnPct, cfg.DiskWarnPct)
	}
	if cfg.BattLowPct != 20 || cfg.BattCritPct != 10 {
		t.Errorf("batt = %d/%d", cfg.BattLowPct, cfg.BattCritPct)
	}
	if cfg.LoadWarnFactor != 1.5 || cfg.AlertLogCap != 100 {
		t.Errorf("load/cap = %v/%d", cfg.LoadWarnFactor, cfg.AlertLogCap)
	}
	if cfg.AlertClearTicks != 5 {
		t.Errorf("AlertClearTicks = %d", cfg.AlertClearTicks)
	}
	if cfg.AlertBell {
		t.Error("AlertBell should parse 'off'")
	}
	if cfg.AlertCmd != "notify-send ucmon" {
		t.Errorf("AlertCmd = %q", cfg.AlertCmd)
	}
	if cfg.AlertLogFile != "/tmp/ucmon-alerts.log" {
		t.Errorf("AlertLogFile = %q", cfg.AlertLogFile)
	}
	if cfg.WifiLowDBm != -75 {
		t.Errorf("WifiLowDBm = %d", cfg.WifiLowDBm)
	}
	if !cfg.PingEnabled || cfg.PingTarget != "1.1.1.1" || cfg.PingLossWarnPct != 20 {
		t.Errorf("ping = %v/%q/%v", cfg.PingEnabled, cfg.PingTarget, cfg.PingLossWarnPct)
	}
	if cfg.PSIWarnPct != 40 {
		t.Errorf("PSIWarnPct = %v", cfg.PSIWarnPct)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := expandHome("~/x.log"); got != filepath.Join(home, "x.log") {
		t.Errorf("expandHome(~/x.log) = %q", got)
	}
	if got := expandHome("/abs/x.log"); got != "/abs/x.log" {
		t.Errorf("expandHome(abs) = %q", got)
	}
}

func TestSanitizeClamps(t *testing.T) {
	c := Config{
		FastInterval:    time.Millisecond,
		ProcInterval:    time.Millisecond,
		SlowInterval:    time.Millisecond,
		BacklightStep:   0,
		AlertLogCap:     1,
		LoadWarnFactor:  -1,
		BattLowPct:      10,
		BattCritPct:     50, // above low — must be pulled down
		AlertClearTicks: 0,
		WifiLowDBm:      75, // positive dBm is meaningless — flip sign
		PingLossWarnPct: 150,
		PSIWarnPct:      -5,
	}
	c.Sanitize()

	if c.FastInterval < 200*time.Millisecond {
		t.Errorf("FastInterval = %v", c.FastInterval)
	}
	if c.ProcInterval < c.FastInterval || c.SlowInterval < c.FastInterval {
		t.Errorf("intervals not ordered: %v %v %v", c.FastInterval, c.ProcInterval, c.SlowInterval)
	}
	if c.BacklightStep != 1 {
		t.Errorf("BacklightStep = %d", c.BacklightStep)
	}
	if c.AlertLogCap != 16 {
		t.Errorf("AlertLogCap = %d", c.AlertLogCap)
	}
	if c.LoadWarnFactor != 2.0 {
		t.Errorf("LoadWarnFactor = %v", c.LoadWarnFactor)
	}
	if c.BattCritPct != c.BattLowPct {
		t.Errorf("BattCritPct = %d, want clamped to %d", c.BattCritPct, c.BattLowPct)
	}
	if c.AlertClearTicks != 1 {
		t.Errorf("AlertClearTicks = %d", c.AlertClearTicks)
	}
	if c.WifiLowDBm != -75 {
		t.Errorf("WifiLowDBm = %d, want -75", c.WifiLowDBm)
	}
	if c.PingLossWarnPct != 100 {
		t.Errorf("PingLossWarnPct = %v", c.PingLossWarnPct)
	}
	if c.PSIWarnPct != 0 {
		t.Errorf("PSIWarnPct = %v", c.PSIWarnPct)
	}
}

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"500ms", 500 * time.Millisecond, true},
		{"2s", 2 * time.Second, true},
		{"3", 3 * time.Second, true},
		{"1.5", 1500 * time.Millisecond, true},
		{"-1", 0, false},
		{"junk", 0, false},
	}
	for _, c := range cases {
		got, ok := parseInterval(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseInterval(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
