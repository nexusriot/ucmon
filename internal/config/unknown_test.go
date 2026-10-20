package config

import (
	"reflect"
	"testing"
	"time"
)

// A misspelled key must be named, with the line it was written on, and
// nothing else in the file may be mistaken for one.
func TestLoadFromCollectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, `# ucmon config
[general]
start_tab   = "net"
temp_warnn  = 90          # typo for temp_warn
proc_interval = 2s

; a semicolon comment
ping_targets = "1.1.1.1"  # plural: not a key
`)
	cfg := LoadFrom(path)

	want := []UnknownKey{
		{Key: "temp_warnn", Line: 4},
		{Key: "ping_targets", Line: 8},
	}
	if !reflect.DeepEqual(cfg.Unknown, want) {
		t.Errorf("Unknown = %+v, want %+v", cfg.Unknown, want)
	}
	if names := cfg.UnknownKeyNames(); !reflect.DeepEqual(names, []string{"temp_warnn", "ping_targets"}) {
		t.Errorf("UnknownKeyNames = %v", names)
	}

	// Non-fatal: everything the parser did recognise still applied, and the
	// typo left its field at the default.
	if cfg.StartTab != "net" {
		t.Errorf("StartTab = %q, want net", cfg.StartTab)
	}
	if cfg.ProcInterval != 2*time.Second {
		t.Errorf("ProcInterval = %v, want 2s", cfg.ProcInterval)
	}
	if cfg.TempWarn != Default().TempWarn || cfg.TempExplicit {
		t.Errorf("a typo must not touch TempWarn: %v (explicit %v)", cfg.TempWarn, cfg.TempExplicit)
	}
}

// Section headers, comments, blank lines and lines with no key at all are
// skipped before apply ever sees them, so none of them is an unknown key.
func TestLoadFromIgnoresNonKeyLines(t *testing.T) {
	path := writeConfig(t, `
[alerts]
[a.nested.section]
# commented_out = 1
; also_commented = 1

this is not toml
= nonsense
   = also nonsense
alerts_enabled = false
`)
	cfg := LoadFrom(path)
	if len(cfg.Unknown) != 0 {
		t.Errorf("Unknown = %+v, want none", cfg.Unknown)
	}
	if cfg.AlertsEnabled {
		t.Error("alerts_enabled = false did not apply")
	}
}

// A key repeated (a typo pasted twice) is one problem, reported at the line
// that introduced it.
func TestLoadFromDeduplicatesUnknownKeys(t *testing.T) {
	path := writeConfig(t, "temp_warnn = 90\ntemp_warnn = 95\nTEMP_WARNN = 99\n")
	cfg := LoadFrom(path)
	if len(cfg.Unknown) != 1 || cfg.Unknown[0] != (UnknownKey{Key: "temp_warnn", Line: 1}) {
		t.Errorf("Unknown = %+v, want one entry at line 1", cfg.Unknown)
	}
}

// A clean file — and a missing one — report nothing, so the startup notice
// and --check-config stay quiet on a healthy install.
func TestLoadFromCleanConfigHasNoUnknownKeys(t *testing.T) {
	path := writeConfig(t, "start_tab = \"cpu\"\nalert_cmd = 'notify-send \"ucmon\"'\n")
	if cfg := LoadFrom(path); len(cfg.Unknown) != 0 {
		t.Errorf("Unknown = %+v, want none", cfg.Unknown)
	}
	if cfg := LoadFrom(""); cfg.Unknown != nil {
		t.Errorf("empty path: Unknown = %+v", cfg.Unknown)
	}
}

// apply is the single source of truth for "is this a key", and every
// documented alias has to answer yes.
func TestApplyReportsRecognition(t *testing.T) {
	cfg := Default()
	for _, key := range []string{"start_tab", "starttab", "tab", "temp_warn", "metrics", "alert_cmd"} {
		if !apply(&cfg, key, "1") {
			t.Errorf("apply(%q) reported the key as unknown", key)
		}
	}
	for _, key := range []string{"temp_warnn", "ping_targets", "", "start tab"} {
		if apply(&cfg, key, "1") {
			t.Errorf("apply(%q) reported an unknown key as recognised", key)
		}
	}
}
