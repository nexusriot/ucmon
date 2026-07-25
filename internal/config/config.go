// Package config loads ucmon's optional user configuration from
// $XDG_CONFIG_HOME/ucmon/config.toml (falling back to ~/.config/ucmon/...).
//
// The parser accepts the flat TOML key/value subset (key = value, with #
// comments and optional [section] headers that are ignored). No third-party
// TOML dependency is pulled in, keeping the build pure-Go for cross-compilation.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the resolved settings. Missing keys keep their default.
type Config struct {
	StartTab string // cpu|mem|proc|disk|net|power|gpu

	FastInterval time.Duration // base tick: CPU / memory / network throughput
	ProcInterval time.Duration // process & connection list
	SlowInterval time.Duration // disk, power, GPU/SoC

	TempWarn float64 // °C, yellow threshold
	TempHot  float64 // °C, orange threshold
	TempCrit float64 // °C, red threshold

	BacklightStep   int    // percent change per +/- keypress
	BacklightDevice string // optional /sys/class/backlight device override

	// Alerting (Alerts tab + header banner). Thresholds drive edge-triggered
	// alerts; CPU-temperature alerts reuse TempHot (warn) / TempCrit (crit).
	AlertsEnabled  bool    // master switch
	MemWarnPct     float64 // RAM used %% that raises a warning
	SwapWarnPct    float64 // swap used %% that raises a warning
	DiskWarnPct    float64 // filesystem used %% that raises a warning
	BattLowPct     int     // discharging battery %% that raises a warning
	BattCritPct    int     // discharging battery %% that raises a critical alert
	LoadWarnFactor float64 // load1 ≥ factor×cores raises a warning
	AlertLogCap    int     // max alert-log entries retained in memory

	// Alert delivery / flap damping.
	AlertClearTicks int    // consecutive OK ticks before a condition clears
	AlertBell       bool   // terminal bell on a new critical alert
	AlertCmd        string // shell command run on each raise/clear ("" = off)
	AlertLogFile    string // append raise/clear lines to this file ("" = off)

	// WiFi signal alert: warn when a wireless iface signal ≤ this dBm (0 = off).
	WifiLowDBm int

	// Connectivity latency monitor (Network tab).
	PingEnabled     bool    // ping the default gateway on the proc interval
	PingTarget      string  // extra external target (host or IP, "" = none)
	PingLossWarnPct float64 // packet-loss %% that raises a warning (0 = off)

	// PSI pressure alert: warn when some avg10 ≥ this %% for cpu/mem/io (0 = off).
	PSIWarnPct float64
}

// Default returns the built-in configuration used when no file is present.
func Default() Config {
	return Config{
		StartTab:      "cpu",
		FastInterval:  1 * time.Second,
		ProcInterval:  3 * time.Second,
		SlowInterval:  5 * time.Second,
		TempWarn:      50,
		TempHot:       65,
		TempCrit:      80,
		BacklightStep: 10,

		AlertsEnabled:  true,
		MemWarnPct:     90,
		SwapWarnPct:    50,
		DiskWarnPct:    90,
		BattLowPct:     15,
		BattCritPct:    5,
		LoadWarnFactor: 2.0,
		AlertLogCap:    200,

		AlertClearTicks: 3,
		AlertBell:       true,

		PingEnabled: true,
	}
}

// Path returns the location ucmon looks for its config file.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ucmon", "config.toml")
}

// Load reads the config file at Path(). A missing file is not an error: the
// defaults are returned. Malformed individual lines are skipped silently so a
// typo never prevents the monitor from starting.
func Load() Config {
	return LoadFrom(Path())
}

// LoadFrom reads the config file at an explicit path (the --config flag).
func LoadFrom(path string) Config {
	cfg := Default()
	if path == "" {
		return cfg
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") { // [section] headers are ignored
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, "\"'") // strip optional quotes
		apply(&cfg, key, val)
	}
	return cfg
}

func apply(cfg *Config, key, val string) {
	switch key {
	case "start_tab", "starttab", "tab":
		cfg.StartTab = strings.ToLower(val)
	case "fast_interval", "fastinterval", "cpu_interval":
		if d, ok := parseInterval(val); ok {
			cfg.FastInterval = d
		}
	case "proc_interval", "procinterval", "process_interval":
		if d, ok := parseInterval(val); ok {
			cfg.ProcInterval = d
		}
	case "slow_interval", "slowinterval", "disk_interval":
		if d, ok := parseInterval(val); ok {
			cfg.SlowInterval = d
		}
	case "temp_warn", "tempwarn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempWarn = f
		}
	case "temp_hot", "temphot":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempHot = f
		}
	case "temp_crit", "tempcrit":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempCrit = f
		}
	case "backlight_step", "backlightstep":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BacklightStep = n
		}
	case "backlight_device", "backlightdevice":
		cfg.BacklightDevice = val
	case "alerts_enabled", "alertsenabled", "alerts":
		cfg.AlertsEnabled = parseBool(val, cfg.AlertsEnabled)
	case "mem_warn_pct", "memwarnpct", "mem_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.MemWarnPct = f
		}
	case "swap_warn_pct", "swapwarnpct", "swap_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.SwapWarnPct = f
		}
	case "disk_warn_pct", "diskwarnpct", "disk_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.DiskWarnPct = f
		}
	case "batt_low_pct", "battlowpct", "batt_low":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BattLowPct = n
		}
	case "batt_crit_pct", "battcritpct", "batt_crit":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BattCritPct = n
		}
	case "load_warn_factor", "loadwarnfactor", "load_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.LoadWarnFactor = f
		}
	case "alert_log_cap", "alertlogcap":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AlertLogCap = n
		}
	case "alert_clear_ticks", "alertclearticks":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AlertClearTicks = n
		}
	case "alert_bell", "alertbell":
		cfg.AlertBell = parseBool(val, cfg.AlertBell)
	case "alert_cmd", "alertcmd":
		cfg.AlertCmd = val
	case "alert_log_file", "alertlogfile":
		cfg.AlertLogFile = expandHome(val)
	case "wifi_low_dbm", "wifilowdbm", "wifi_low":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.WifiLowDBm = n
		}
	case "ping_enabled", "pingenabled", "ping":
		cfg.PingEnabled = parseBool(val, cfg.PingEnabled)
	case "ping_target", "pingtarget":
		cfg.PingTarget = val
	case "ping_loss_warn_pct", "pinglosswarnpct", "ping_loss_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.PingLossWarnPct = f
		}
	case "psi_warn_pct", "psiwarnpct", "psi_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.PSIWarnPct = f
		}
	}
}

// expandHome resolves a leading "~/" against the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path[1:], "/"))
		}
	}
	return path
}

// parseBool accepts the common truthy/falsy spellings; an unrecognized value
// keeps the supplied default.
func parseBool(val string, def bool) bool {
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// parseInterval accepts a Go duration ("500ms", "2s") or a bare number that is
// interpreted as seconds ("3", "1.5").
func parseInterval(val string) (time.Duration, bool) {
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d, true
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second)), true
	}
	return 0, false
}

// Sanitize clamps the intervals to sane lower bounds so a bad config can't
// peg the CPU with a sub-millisecond tick.
func (c *Config) Sanitize() {
	if c.FastInterval < 200*time.Millisecond {
		c.FastInterval = 200 * time.Millisecond
	}
	if c.ProcInterval < c.FastInterval {
		c.ProcInterval = c.FastInterval
	}
	if c.SlowInterval < c.FastInterval {
		c.SlowInterval = c.FastInterval
	}
	if c.BacklightStep < 1 {
		c.BacklightStep = 1
	}
	if c.BacklightStep > 100 {
		c.BacklightStep = 100
	}
	if c.AlertLogCap < 16 {
		c.AlertLogCap = 16
	}
	if c.LoadWarnFactor <= 0 {
		c.LoadWarnFactor = 2.0
	}
	// BattCrit should not exceed BattLow so the two tiers stay ordered.
	if c.BattCritPct > c.BattLowPct {
		c.BattCritPct = c.BattLowPct
	}
	if c.AlertClearTicks < 1 {
		c.AlertClearTicks = 1
	}
	if c.AlertClearTicks > 60 {
		c.AlertClearTicks = 60
	}
	// A positive dBm threshold is meaningless (signal levels are negative).
	if c.WifiLowDBm > 0 {
		c.WifiLowDBm = -c.WifiLowDBm
	}
	if c.PingLossWarnPct < 0 {
		c.PingLossWarnPct = 0
	}
	if c.PingLossWarnPct > 100 {
		c.PingLossWarnPct = 100
	}
	if c.PSIWarnPct < 0 {
		c.PSIWarnPct = 0
	}
}
