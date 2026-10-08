package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(cfg, def) {
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

// Inline "#" comments are TOML, and every example in the README uses them.
// Taking them as part of the value made every documented key a no-op.
func TestInlineCommentsAreStripped(t *testing.T) {
	cfg := LoadFrom(writeConfig(t, `
start_tab        = "net"   # cpu | mem | proc | disk | net | power | gpu
fast_interval    = "2s"    # CPU / memory / network throughput tick
temp_crit        = 70      # °C — red threshold
backlight_step   = 5       # percent per keypress
ping_target      = 1.1.1.1 # cloudflare
alerts_enabled   = false   # master switch
`))
	if cfg.StartTab != "net" {
		t.Errorf("StartTab = %q", cfg.StartTab)
	}
	if cfg.FastInterval != 2*time.Second {
		t.Errorf("FastInterval = %v, want 2s", cfg.FastInterval)
	}
	if cfg.TempCrit != 70 {
		t.Errorf("TempCrit = %v, want 70", cfg.TempCrit)
	}
	if cfg.BacklightStep != 5 {
		t.Errorf("BacklightStep = %d, want 5", cfg.BacklightStep)
	}
	if cfg.PingTarget != "1.1.1.1" {
		t.Errorf("PingTarget = %q, want 1.1.1.1", cfg.PingTarget)
	}
	if cfg.AlertsEnabled {
		t.Error("AlertsEnabled should be false")
	}
}

// Only one matched pair of surrounding quotes comes off; quotes inside the
// value belong to it, and a "#" inside quotes is not a comment.
func TestQuoteHandling(t *testing.T) {
	cases := []struct{ body, want string }{
		{`alert_cmd = 'notify-send "ucmon" "$UCMON_ALERT_MSG"'`, `notify-send "ucmon" "$UCMON_ALERT_MSG"`},
		{`alert_cmd = "logger -t ucmon '$UCMON_ALERT_MSG'"`, `logger -t ucmon '$UCMON_ALERT_MSG'`},
		{`alert_cmd = 'printf "#%s\n" "$UCMON_ALERT_SEV"'`, `printf "#%s\n" "$UCMON_ALERT_SEV"`},
		{`alert_cmd = echo plain`, `echo plain`},
		{`alert_cmd = 'trailing'   # a comment`, `trailing`},
	}
	for _, c := range cases {
		if got := LoadFrom(writeConfig(t, c.body)).AlertCmd; got != c.want {
			t.Errorf("%s\n  got  %q\n  want %q", c.body, got, c.want)
		}
	}
}

// Out-of-range alert thresholds silently never fire or fire forever, so a typo
// falls back to the default rather than being clamped into uselessness.
func TestSanitizeBoundsAlertThresholds(t *testing.T) {
	cfg := LoadFrom(writeConfig(t, "mem_warn_pct = -5\nswap_warn_pct = 0\ndisk_warn_pct = 900\nbatt_low_pct = 300\nbatt_crit_pct = -9\npsi_warn_pct = 400\n"))
	cfg.Sanitize()
	def := Default()

	if cfg.MemWarnPct != def.MemWarnPct {
		t.Errorf("MemWarnPct = %v, want the default %v", cfg.MemWarnPct, def.MemWarnPct)
	}
	if cfg.SwapWarnPct != def.SwapWarnPct {
		t.Errorf("SwapWarnPct = %v, want the default %v", cfg.SwapWarnPct, def.SwapWarnPct)
	}
	if cfg.DiskWarnPct != def.DiskWarnPct {
		t.Errorf("DiskWarnPct = %v, want the default %v", cfg.DiskWarnPct, def.DiskWarnPct)
	}
	if cfg.BattLowPct != 100 {
		t.Errorf("BattLowPct = %d, want 100", cfg.BattLowPct)
	}
	if cfg.BattCritPct != 0 {
		t.Errorf("BattCritPct = %d, want 0", cfg.BattCritPct)
	}
	if cfg.PSIWarnPct != 100 {
		t.Errorf("PSIWarnPct = %v, want 100", cfg.PSIWarnPct)
	}
}

// TempColor and the alert engine both assume warn <= hot <= crit.
func TestSanitizeOrdersTempThresholds(t *testing.T) {
	cases := []struct{ warn, hot, crit, wWarn, wHot, wCrit float64 }{
		{50, 65, 80, 50, 65, 80}, // already ordered
		{90, 70, 50, 50, 50, 50}, // fully inverted
		{50, 65, 60, 50, 60, 60}, // only crit lowered — crit is authoritative
		{70, 60, 80, 60, 60, 80}, // warn above hot
	}
	for _, c := range cases {
		cfg := Config{TempWarn: c.warn, TempHot: c.hot, TempCrit: c.crit}
		cfg.Sanitize()
		if cfg.TempWarn != c.wWarn || cfg.TempHot != c.wHot || cfg.TempCrit != c.wCrit {
			t.Errorf("Sanitize(%v/%v/%v) = %v/%v/%v, want %v/%v/%v",
				c.warn, c.hot, c.crit, cfg.TempWarn, cfg.TempHot, cfg.TempCrit,
				c.wWarn, c.wHot, c.wCrit)
		}
		if !(cfg.TempWarn <= cfg.TempHot && cfg.TempHot <= cfg.TempCrit) {
			t.Errorf("invariant broken: %v/%v/%v", cfg.TempWarn, cfg.TempHot, cfg.TempCrit)
		}
	}
}

// Path honours XDG_CONFIG_HOME and falls back to ~/.config, which is what the
// README documents.
func TestPathRespectsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := Path(), filepath.Join(xdg, "ucmon", "config.toml"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}

	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	if got, want := Path(), filepath.Join(home, ".config", "ucmon", "config.toml"); got != want {
		t.Errorf("Path() without XDG = %q, want %q", got, want)
	}
}

// Load reads whatever Path points at; a missing file is the defaults, and a
// real file there is picked up without an explicit --config.
func TestLoadUsesPath(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if got := Load(); !reflect.DeepEqual(got, Default()) {
		t.Errorf("Load() with no file did not return the defaults")
	}

	dir := filepath.Join(xdg, "ucmon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("start_tab = \"net\"   # picked up without --config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load().StartTab; got != "net" {
		t.Errorf("Load().StartTab = %q, want net", got)
	}
}

// An empty path is the "no config" signal from main and must not be treated as
// the current directory.
func TestLoadFromEmptyPath(t *testing.T) {
	if got := LoadFrom(""); !reflect.DeepEqual(got, Default()) {
		t.Error("LoadFrom(\"\") did not return the defaults")
	}
}

// Section headers and both comment styles are skipped, and a line without "="
// cannot crash the parser.
func TestLoadFromSkipsNonSettings(t *testing.T) {
	cfg := LoadFrom(writeConfig(t, `
# a hash comment
; a semicolon comment
[alerts]
this line has no equals sign
   
start_tab = "power"
= value with no key
`))
	if cfg.StartTab != "power" {
		t.Errorf("StartTab = %q, want power", cfg.StartTab)
	}
}

// With neither XDG_CONFIG_HOME nor HOME there is nowhere to look; Path must
// return "" so LoadFrom falls through to the defaults instead of reading a
// relative path out of the working directory.
func TestPathWithNoHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	got := Path()
	if got != "" && !filepath.IsAbs(got) {
		t.Errorf("Path() = %q, want \"\" or an absolute path", got)
	}
	if !reflect.DeepEqual(LoadFrom(""), Default()) {
		t.Error("an empty path did not fall back to the defaults")
	}
}

func TestLoadRateAndExporterKeys(t *testing.T) {
	path := writeConfig(t, `
batt_drain_warn_pct_h = 40
disk_full_warn_hours  = 3
temp_rise_warn_c_per_min = 1.5
modem_enabled   = true
history_len     = 400
procs_cap       = 50
report_dir      = "/tmp/reports"
history_file    = "/tmp/hist.json"
metrics_listen  = ":9100"
temp_from_trip_points = false
`)
	cfg := LoadFrom(path)

	if cfg.BattDrainWarnPctH != 40 {
		t.Errorf("BattDrainWarnPctH = %v", cfg.BattDrainWarnPctH)
	}
	if cfg.DiskFullWarnHours != 3 {
		t.Errorf("DiskFullWarnHours = %v", cfg.DiskFullWarnHours)
	}
	if cfg.TempRiseWarnCPerMin != 1.5 {
		t.Errorf("TempRiseWarnCPerMin = %v", cfg.TempRiseWarnCPerMin)
	}
	if !cfg.ModemEnabled {
		t.Error("ModemEnabled not set")
	}
	if cfg.HistoryLen != 400 || cfg.ProcsCap != 50 {
		t.Errorf("HistoryLen/ProcsCap = %d/%d", cfg.HistoryLen, cfg.ProcsCap)
	}
	if cfg.ReportDir != "/tmp/reports" || cfg.HistoryFile != "/tmp/hist.json" {
		t.Errorf("paths = %q / %q", cfg.ReportDir, cfg.HistoryFile)
	}
	if cfg.MetricsListen != ":9100" {
		t.Errorf("MetricsListen = %q", cfg.MetricsListen)
	}
	if cfg.TempFromTripPoints {
		t.Error("temp_from_trip_points = false was not honoured")
	}
}

// Setting any temperature threshold by hand must switch off the trip-point
// calibration, or the config would be silently overwritten.
func TestTempExplicitMarksAHandWrittenThreshold(t *testing.T) {
	if cfg := LoadFrom(writeConfig(t, "temp_crit = 70\n")); !cfg.TempExplicit {
		t.Error("temp_crit did not mark the thresholds as explicit")
	}
	if cfg := LoadFrom(writeConfig(t, "temp_warn = 45\n")); !cfg.TempExplicit {
		t.Error("temp_warn did not mark the thresholds as explicit")
	}
	if cfg := LoadFrom(writeConfig(t, "mem_warn_pct = 80\n")); cfg.TempExplicit {
		t.Error("an unrelated key marked the thresholds as explicit")
	}
	if !Default().TempFromTripPoints {
		t.Error("trip-point calibration should be on by default")
	}
}

func TestSanitizeClampsNewKeys(t *testing.T) {
	cfg := Default()
	cfg.BattDrainWarnPctH = -5
	cfg.DiskFullWarnHours = -1
	cfg.TempRiseWarnCPerMin = -2
	cfg.HistoryLen = 2
	cfg.ProcsCap = 0
	cfg.Sanitize()

	if cfg.BattDrainWarnPctH != 0 || cfg.DiskFullWarnHours != 0 || cfg.TempRiseWarnCPerMin != 0 {
		t.Errorf("negative rate thresholds survived: %+v", cfg)
	}
	if cfg.HistoryLen < 10 {
		t.Errorf("HistoryLen = %d, want a usable floor", cfg.HistoryLen)
	}
	if cfg.ProcsCap < 10 {
		t.Errorf("ProcsCap = %d, want a usable floor", cfg.ProcsCap)
	}

	// A history longer than the ring is ever read from costs memory per
	// interface, sensor and core for nothing.
	cfg.HistoryLen = 1 << 20
	cfg.ProcsCap = 1 << 20
	cfg.Sanitize()
	if cfg.HistoryLen > 5000 {
		t.Errorf("HistoryLen = %d, want a ceiling", cfg.HistoryLen)
	}
	if cfg.ProcsCap > 10000 {
		t.Errorf("ProcsCap = %d, want a ceiling", cfg.ProcsCap)
	}
}
