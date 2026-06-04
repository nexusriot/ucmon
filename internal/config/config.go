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
	cfg := Default()
	path := Path()
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
}
