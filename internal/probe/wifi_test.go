package probe

import (
	"os"
	"path/filepath"
	"strings"
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

// fakeIW points iwPaths at a stub script so ReadIWLinks' full exec+parse
// pipeline runs on any host, including one carrying the real binary.
func fakeIW(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "iw")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := iwPaths
	iwPaths = []string{bin}
	t.Cleanup(func() { iwPaths = old })
}

// fakeWireless points wirelessPath at a /proc/net/wireless with the given
// interface names, which is how ReadIWLinks discovers what to ask iw about.
func fakeWireless(t *testing.T, names ...string) {
	t.Helper()
	body := "Inter-| sta-|   Quality        |   Discarded packets\n" +
		" face | tus | link level noise |  nwid  crypt\n"
	for _, n := range names {
		body += " " + n + ": 0000   70.  -40.  -256        0\n"
	}
	path := filepath.Join(t.TempDir(), "wireless")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := wirelessPath
	wirelessPath = path
	t.Cleanup(func() { wirelessPath = old })
}

func TestReadIWLinksWithFakeIW(t *testing.T) {
	fakeIW(t, `#!/bin/sh
echo "Connected to aa:bb:cc:dd:ee:ff (on $2)"
echo "        SSID: TestNet"
echo "        freq: 5180"
echo "        signal: -52 dBm"
echo "        rx bitrate: 433.3 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 1"
echo "        tx bitrate: 390.0 MBit/s VHT-MCS 8"
`)
	fakeWireless(t, "wlan0")

	links := ReadIWLinks(nil)
	l, ok := links["wlan0"]
	if !ok {
		t.Fatalf("no link for wlan0, got %+v", links)
	}
	if l.SSID != "TestNet" || l.FreqMHz != 5180 || l.Channel != 36 {
		t.Errorf("got %+v, want SSID TestNet on 5180 MHz / ch 36", l)
	}
	if l.SignalDBm != -52 || l.RxMbps != 433.3 || l.TxMbps != 390 {
		t.Errorf("got %+v", l)
	}
}

// An interface that is up but not associated reports nothing rather than a
// half-filled link.
func TestReadIWLinksNotConnected(t *testing.T) {
	fakeIW(t, "#!/bin/sh\necho \"Not connected.\"\n")
	fakeWireless(t, "wlan0")

	if links := ReadIWLinks(nil); len(links) != 0 {
		t.Errorf("an unassociated interface produced %+v", links)
	}
}

// A failing iw (missing permission, unsupported device) is skipped per
// interface without losing the ones that worked.
func TestReadIWLinksPartialFailure(t *testing.T) {
	fakeIW(t, `#!/bin/sh
[ "$2" = "wlan1" ] && exit 1
echo "Connected to aa:bb:cc:dd:ee:ff (on $2)"
echo "        SSID: OnlyOne"
`)
	fakeWireless(t, "wlan0", "wlan1")

	links := ReadIWLinks(nil)
	if _, bad := links["wlan1"]; bad {
		t.Error("a failing iw invocation still produced a link")
	}
	if l, ok := links["wlan0"]; !ok || l.SSID != "OnlyOne" {
		t.Errorf("the working interface was lost: %+v", links)
	}
}

// No wireless interfaces means iw is never invoked at all.
func TestReadIWLinksNoWirelessInterfaces(t *testing.T) {
	fakeIW(t, "#!/bin/sh\nexit 99\n")
	fakeWireless(t)

	if links := ReadIWLinks(nil); len(links) != 0 {
		t.Errorf("got %+v, want no links", links)
	}
}

func TestIwBinPathAbsent(t *testing.T) {
	old := iwPaths
	iwPaths = []string{filepath.Join(t.TempDir(), "nope")}
	defer func() { iwPaths = old }()
	t.Setenv("PATH", t.TempDir())
	if got := iwBinPath(); got != "" {
		t.Errorf("iwBinPath = %q with no iw anywhere", got)
	}
	if links := ReadIWLinks(nil); len(links) != 0 {
		t.Errorf("ReadIWLinks without iw returned %+v", links)
	}
}

// An interface hidden by net_ignore must not cost a fork and a 2s timeout on
// every slow tick just to have its answer discarded by the UI filter.
func TestReadIWLinksSkipsFilteredInterfaces(t *testing.T) {
	dir := t.TempDir()
	wl := filepath.Join(dir, "wireless")
	body := "Inter-| sta-|   Quality\n face | tus | link\n" +
		" wlan0: 0000   65.  -45.  -256\n wlan1: 0000   40.  -70.  -256\n"
	if err := os.WriteFile(wl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stub iw that records which interfaces it was asked about.
	calls := filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "iw")
	script := "#!/bin/sh\necho \"$2\" >> " + calls + "\n" +
		"printf '%s\\n' 'Connected to aa:bb:cc:dd:ee:ff (on '\"$2\"')' '	SSID: Net'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	origW, origI := wirelessPath, iwPaths
	wirelessPath, iwPaths = wl, []string{stub}
	defer func() { wirelessPath, iwPaths = origW, origI }()

	links := ReadIWLinks(func(name string) bool { return name == "wlan0" })
	if _, ok := links["wlan1"]; ok {
		t.Error("a filtered interface was queried and returned")
	}
	if _, ok := links["wlan0"]; !ok {
		t.Fatal("the kept interface was not queried")
	}

	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("stub iw was never run: %v", err)
	}
	if strings.Contains(string(got), "wlan1") {
		t.Errorf("iw was forked for a filtered interface (calls: %q)", got)
	}
}
