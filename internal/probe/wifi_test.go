package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWirelessStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wireless")
	content := "Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE\n" +
		" face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22\n" +
		" wlan0: 0000   56.  -54.  -256        0      0      0      0      0        0\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := wirelessPath
	wirelessPath = path
	defer func() { wirelessPath = old }()

	stats := readWirelessStats()
	w, ok := stats["wlan0"]
	if !ok {
		t.Fatalf("wlan0 missing: %v", stats)
	}
	if !w.Present || w.SignalDBm != -54 {
		t.Errorf("got %+v", w)
	}
	if w.QualityPct != 80 {
		t.Errorf("QualityPct = %v, want 80 (56/70)", w.QualityPct)
	}
}

const iwSample = `Connected to aa:bb:cc:dd:ee:ff (on wlan0)
	SSID: NightCity Mesh
	freq: 5180
	RX: 123456789 bytes (98765 packets)
	TX: 12345678 bytes (8765 packets)
	signal: -52 dBm
	rx bitrate: 433.3 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 1
	tx bitrate: 390.0 MBit/s VHT-MCS 8 80MHz short GI VHT-NSS 1

	bss flags:	short-slot-time
	dtim period:	1
	beacon int:	100
`

func TestParseIWLink(t *testing.T) {
	l := parseIWLink(iwSample)
	if !l.Present {
		t.Fatal("Present should be true")
	}
	if l.SSID != "NightCity Mesh" {
		t.Errorf("SSID = %q", l.SSID)
	}
	if l.FreqMHz != 5180 || l.Channel != 36 {
		t.Errorf("freq/ch = %d/%d, want 5180/36", l.FreqMHz, l.Channel)
	}
	if l.SignalDBm != -52 {
		t.Errorf("SignalDBm = %d", l.SignalDBm)
	}
	if l.RxMbps != 433.3 || l.TxMbps != 390.0 {
		t.Errorf("bitrates = %v/%v", l.RxMbps, l.TxMbps)
	}

	if l := parseIWLink("Not connected.\n"); l.Present {
		t.Error("disconnected output should not be Present")
	}
}

func TestFreqToChannel(t *testing.T) {
	cases := [][2]int{
		{2412, 1},
		{2437, 6},
		{2472, 13},
		{2484, 14},
		{5180, 36},
		{5745, 149},
		{5955, 1},  // 6 GHz channel 1
		{6115, 33}, // 6 GHz
		{123, 0},   // unknown
	}
	for _, c := range cases {
		if got := FreqToChannel(c[0]); got != c[1] {
			t.Errorf("FreqToChannel(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}
