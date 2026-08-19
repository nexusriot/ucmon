package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// readmeConfigBlock returns the key/value lines of the ```toml example in the
// README, with the leading "# " stripped from commented-out settings.
func readmeConfigBlock(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Skipf("README not readable: %v", err)
	}
	m := regexp.MustCompile("(?s)```toml\n(.*?)\n```").FindSubmatch(b)
	if m == nil {
		t.Fatal("no ```toml block in README.md")
	}

	var out []string
	for _, line := range strings.Split(string(m[1]), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "# ") // commented-out settings still count
		if line == "" || !strings.Contains(line, "=") || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		t.Fatal("no settings found in the README example")
	}
	return out
}

// Every setting the README documents must be a key apply() recognises.
// Documenting a key that does not exist is silently a no-op.
func TestREADMESettingsAreRecognised(t *testing.T) {
	for _, line := range readmeConfigBlock(t) {
		eq := strings.IndexByte(line, '=')
		key := strings.ToLower(strings.TrimSpace(line[:eq]))

		// The documented value often equals the default, so probe with values
		// that differ from it across every field type this parser accepts.
		recognised := false
		for _, probeVal := range []string{"1", "false", "2s", "____probe____"} {
			cfg := Default()
			apply(&cfg, key, probeVal)
			if !reflect.DeepEqual(cfg, Default()) {
				recognised = true
				break
			}
		}
		if !recognised {
			t.Errorf("README documents %q, which no config key matches", key)
		}
	}
}

// The README writes every setting as `key = value  # explanation`. Values must
// come out without the comment and without the surrounding quotes — reading
// them literally is what made every documented key a no-op before 0.8.0.
func TestREADMEValuesParseCleanly(t *testing.T) {
	for _, line := range readmeConfigBlock(t) {
		eq := strings.IndexByte(line, '=')
		key := strings.TrimSpace(line[:eq])
		got := splitValue(line[eq+1:])

		if strings.Contains(got, "#") && !strings.Contains(key, "cmd") {
			t.Errorf("%s: inline comment leaked into the value: %q", key, got)
		}
		if len(got) >= 2 {
			if q := got[0]; (q == '"' || q == '\'') && got[len(got)-1] == q {
				t.Errorf("%s: value kept its surrounding quotes: %q", key, got)
			}
		}
		if got == "" {
			t.Errorf("%s: parsed to an empty value", key)
		}
	}
}

// Loading the README block verbatim must not leave a documented setting at a
// value the parser mangled: durations and numbers must all be usable.
func TestREADMEBlockLoads(t *testing.T) {
	body := strings.Join(readmeConfigBlock(t), "\n")
	cfg := LoadFrom(writeConfig(t, body))

	if cfg.StartTab != "cpu" {
		t.Errorf("StartTab = %q, want cpu", cfg.StartTab)
	}
	if cfg.FastInterval <= 0 || cfg.ProcInterval <= 0 || cfg.SlowInterval <= 0 {
		t.Errorf("intervals = %v/%v/%v", cfg.FastInterval, cfg.ProcInterval, cfg.SlowInterval)
	}
	// Simple string settings carry neither the comment nor any quoting.
	for name, got := range map[string]string{
		"alert_log_file":   cfg.AlertLogFile,
		"ping_target":      cfg.PingTarget,
		"backlight_device": cfg.BacklightDevice,
	} {
		if strings.ContainsAny(got, "#\"'") {
			t.Errorf("%s kept comment or quote characters: %q", name, got)
		}
	}
	// A shell hook may contain quotes of its own, but never its own wrapper
	// and never the trailing explanation.
	if strings.Contains(cfg.AlertCmd, "run on each") {
		t.Errorf("alert_cmd kept its inline comment: %q", cfg.AlertCmd)
	}
	if n := len(cfg.AlertCmd); n >= 2 {
		if q := cfg.AlertCmd[0]; (q == '"' || q == '\'') && cfg.AlertCmd[n-1] == q {
			t.Errorf("alert_cmd kept its surrounding quotes: %q", cfg.AlertCmd)
		}
	}
	if !strings.HasPrefix(cfg.AlertCmd, "notify-send") {
		t.Errorf("alert_cmd = %q, want the documented notify-send hook", cfg.AlertCmd)
	}

	cfg.Sanitize()
	if !(cfg.TempWarn <= cfg.TempHot && cfg.TempHot <= cfg.TempCrit) {
		t.Errorf("documented temp thresholds are not ordered: %v/%v/%v",
			cfg.TempWarn, cfg.TempHot, cfg.TempCrit)
	}
}
