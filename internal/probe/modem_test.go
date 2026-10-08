package probe

import (
	"os"
	"path/filepath"
	"testing"
)

const mmcliModemOutput = `
  --------------------------------
  General  |              path: /org/freedesktop/ModemManager1/Modem/0
           |         device id: abc123
  --------------------------------
  Hardware |      manufacturer: QUALCOMM INCORPORATED
           |             model: QUECTEL Mobile Broadband Module
           |         firmware revision: EC25EFAR06A11M4G
           |      equipment id: 867698041234567
  --------------------------------
  Status   |             state: connected
           |      power state: on
           |      access tech: lte
           |   signal quality: 71% (recent)
  --------------------------------
  3GPP     |              imei: 867698041234567
           |     operator code: 23415
           |     operator name: Vodafone UK
           |      registration: home
`

func TestParseModem(t *testing.T) {
	m := parseModem(mmcliModemOutput)
	if !m.Present {
		t.Fatal("modem not detected in well-formed mmcli output")
	}
	if m.State != "connected" {
		t.Errorf("State = %q, want connected", m.State)
	}
	if !m.Registered() {
		t.Error("a connected modem must count as registered")
	}
	if m.Operator != "Vodafone UK" {
		t.Errorf("Operator = %q", m.Operator)
	}
	if m.OperatorCode != "23415" {
		t.Errorf("OperatorCode = %q", m.OperatorCode)
	}
	if m.AccessTech != "lte" {
		t.Errorf("AccessTech = %q", m.AccessTech)
	}
	if m.SignalPct != 71 || !m.SignalRecent {
		t.Errorf("signal = %d%% recent=%v, want 71 recent", m.SignalPct, m.SignalRecent)
	}
	if m.Model == "" || m.Manufacturer == "" {
		t.Errorf("hardware identity missing: %+v", m)
	}
}

// ModemManager reuses "current:" for bands and modes; only a real radio
// technology may land in AccessTech.
func TestParseModemIgnoresNonRadioCurrentKey(t *testing.T) {
	out := `
  Status   |     state: registered
  Bands    |   current: egsm, dcs, utran-1, eutran-1, eutran-3
`
	m := parseModem(out)
	if m.AccessTech != "" {
		t.Errorf("AccessTech = %q, want empty (a band list is not a radio tech)", m.AccessTech)
	}
	if m.State != "registered" || !m.Registered() {
		t.Errorf("State = %q registered=%v", m.State, m.Registered())
	}
}

func TestParseModemRejectsEmptyOutput(t *testing.T) {
	for _, s := range []string{"", "no modems were found\n", "   |   state: --\n"} {
		if m := parseModem(s); m.Present {
			t.Errorf("parseModem(%q) claimed a modem is present", s)
		}
	}
}

func TestFirstModemIndex(t *testing.T) {
	list := "    /org/freedesktop/ModemManager1/Modem/2 [QUALCOMM] EC25\n"
	idx, ok := firstModemIndex(list)
	if !ok || idx != "2" {
		t.Errorf("firstModemIndex = %q,%v want 2,true", idx, ok)
	}
	if _, ok := firstModemIndex("No modems were found\n"); ok {
		t.Error("an empty modem list yielded an index")
	}
}

func TestParseSignalRSSI(t *testing.T) {
	out := `
  5G    |    rsrp: --
  LTE   |    rssi: -73.00 dBm
        |    rsrq: -11.00 dB
`
	v, ok := parseSignalRSSI(out)
	if !ok || v != -73 {
		t.Errorf("parseSignalRSSI = %v,%v want -73,true", v, ok)
	}
	if _, ok := parseSignalRSSI("  | rsrp: -98.00 dBm\n"); ok {
		t.Error("a report with no rssi line yielded a value")
	}
}

func TestParseSignalQuality(t *testing.T) {
	cases := []struct {
		in     string
		pct    int
		recent bool
		ok     bool
	}{
		{"71% (recent)", 71, true, true},
		{"40% (cached)", 40, false, true},
		{"0% (recent)", 0, true, true},
		{"--", 0, false, false},
		{"150%", 0, false, false},
	}
	for _, c := range cases {
		pct, recent, ok := parseSignalQuality(c.in)
		if pct != c.pct || recent != c.recent || ok != c.ok {
			t.Errorf("parseSignalQuality(%q) = %d,%v,%v want %d,%v,%v",
				c.in, pct, recent, ok, c.pct, c.recent, c.ok)
		}
	}
}

// A host with no mmcli must degrade silently, like vcgencmd and iw do.
func TestReadModemAbsentBinary(t *testing.T) {
	orig := mmcliPaths
	mmcliPaths = []string{filepath.Join(t.TempDir(), "mmcli")}
	defer func() { mmcliPaths = orig }()

	t.Setenv("PATH", t.TempDir())
	if m := ReadModem(); m.Present {
		t.Errorf("ReadModem() reported a modem with no mmcli on the system: %+v", m)
	}
}

// With a stub mmcli on PATH the whole read path runs end to end.
func TestReadModemViaStub(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "mmcli")
	script := `#!/bin/sh
case "$*" in
  *--list-modems*) echo "    /org/freedesktop/ModemManager1/Modem/0 [QUALCOMM] EC25" ;;
  *--signal-get*)  echo "  LTE | rssi: -65.00 dBm" ;;
  *) printf '%s\n' "  Status | state: connected" "  Status | access tech: lte" \
       "  Status | signal quality: 80% (recent)" "  3GPP | operator name: Test Net" ;;
esac
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := mmcliPaths
	mmcliPaths = []string{stub}
	defer func() { mmcliPaths = orig }()

	m := ReadModem()
	if !m.Present {
		t.Fatal("stub mmcli produced no modem")
	}
	if m.Operator != "Test Net" || m.SignalPct != 80 {
		t.Errorf("modem = %+v", m)
	}
	if !m.HasRSSI || m.RSSIdBm != -65 {
		t.Errorf("RSSI = %v (has=%v), want -65", m.RSSIdBm, m.HasRSSI)
	}
}
