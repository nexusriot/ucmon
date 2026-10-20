package probe

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// skipIfRoot guards the permission cases: root ignores the mode bits, so a
// 0000 file is readable and a 0444 file is writable for it.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission denial cannot be reproduced")
	}
}

func TestCapStatusString(t *testing.T) {
	for st, want := range map[CapStatus]string{
		CapPresent:    "present",
		CapAbsent:     "absent",
		CapDenied:     "denied",
		CapReadOnly:   "read-only",
		CapStatus(42): "absent",
	} {
		if got := st.String(); got != want {
			t.Errorf("CapStatus(%d) = %q, want %q", st, got, want)
		}
	}
}

// Every check must say what it looked at and what the user loses without it,
// or the report answers "why is that empty" with another question.
func TestCapabilitiesAreSelfDescribing(t *testing.T) {
	caps := Capabilities("")
	if len(caps) < 13 {
		t.Fatalf("Capabilities returned %d entries, want the full set", len(caps))
	}

	seen := map[string]bool{}
	for _, c := range caps {
		if c.Name == "" || c.Source == "" || c.Detail == "" || c.Affects == "" {
			t.Errorf("incomplete capability: %+v", c)
		}
		if seen[c.Name] {
			t.Errorf("duplicate capability name %q", c.Name)
		}
		seen[c.Name] = true
		if c.OK() != (c.Status == CapPresent) {
			t.Errorf("%s: OK() disagrees with Status %v", c.Name, c.Status)
		}
	}

	// The two writable sources are the ones with a printable remedy.
	for _, name := range []string{"cpufreq governor write", "backlight brightness"} {
		if !seen[name] {
			t.Errorf("missing capability %q", name)
		}
	}
}

func TestPathStatus(t *testing.T) {
	dir := t.TempDir()
	if st, _ := pathStatus(dir); st != CapPresent {
		t.Errorf("existing dir: status = %v", st)
	}
	st, why := pathStatus(filepath.Join(dir, "nope"))
	if st != CapAbsent {
		t.Errorf("missing path: status = %v", st)
	}
	if why == "" || strings.Contains(why, dir) {
		// The Source field already carries the path; repeating it makes the
		// report lines wrap for nothing.
		t.Errorf("reason should be the bare syscall message, got %q", why)
	}

	skipIfRoot(t)
	locked := filepath.Join(dir, "locked")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if st, _ := pathStatus(locked); st != CapDenied {
		t.Errorf("unreadable file: status = %v, want denied", st)
	}
}

func TestPluralAndFirstN(t *testing.T) {
	if got := plural(1, "policy", "policies"); got != "1 policy" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(3, "policy", "policies"); got != "3 policies" {
		t.Errorf("plural(3) = %q", got)
	}
	if got := firstN([]string{"a", "b"}, 4); got != "a, b" {
		t.Errorf("firstN under the cap = %q", got)
	}
	if got := firstN([]string{"a", "b", "c"}, 2); got != "a, b, …" {
		t.Errorf("firstN over the cap = %q", got)
	}
}

func TestCheckTempSensors(t *testing.T) {
	c := checkTempSensors([]TempReading{{Label: "cpu_thermal", Temp: 42}})
	if c.Status != CapPresent || !strings.Contains(c.Detail, "cpu_thermal") {
		t.Errorf("with a sensor: %+v", c)
	}

	old := thermalBase
	thermalBase = filepath.Join(t.TempDir(), "nope")
	defer func() { thermalBase = old }()

	c = checkTempSensors(nil)
	if c.Status != CapAbsent || c.Detail == "" {
		t.Errorf("without sensors: %+v", c)
	}
}

func TestCheckPSI(t *testing.T) {
	base := t.TempDir()
	old := psiBase
	psiBase = base
	defer func() { psiBase = old }()

	if err := os.WriteFile(filepath.Join(base, "cpu"),
		[]byte("some avg10=1.50 avg60=1.00 avg300=0.20 total=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := CheckPSI(); c.Status != CapPresent || !strings.Contains(c.Detail, "1.50") {
		t.Errorf("with pressure files: %+v", c)
	}

	// A kernel without CONFIG_PSI has no directory at all, and no permission
	// change can produce one — the remedy has to name the kernel option.
	psiBase = filepath.Join(base, "nope")
	c := CheckPSI()
	if c.Status != CapAbsent {
		t.Fatalf("missing /proc/pressure: status = %v", c.Status)
	}
	if !strings.Contains(c.Detail, "CONFIG_PSI") || !strings.Contains(c.Fix, "psi=1") {
		t.Errorf("absent PSI must explain the kernel option: %+v", c)
	}
}

func TestCheckCPUFreqAndGovernorWrite(t *testing.T) {
	base := t.TempDir()
	old := cpufreqBase
	cpufreqBase = base
	defer func() { cpufreqBase = old }()

	fakePolicy(t, base, "policy0", map[string]string{
		"scaling_cur_freq":            "1200000",
		"cpuinfo_min_freq":            "600000",
		"cpuinfo_max_freq":            "1800000",
		"affected_cpus":               "0 1 2 3",
		"scaling_governor":            "ondemand",
		"scaling_available_governors": "ondemand performance powersave",
	})

	c := CheckCPUFreq()
	if c.Status != CapPresent || !strings.Contains(c.Detail, "ondemand") {
		t.Errorf("with a policy: %+v", c)
	}
	if w := CheckGovernorWrite(); w.Status != CapPresent || w.Fix != "" {
		t.Errorf("writable governor: %+v", w)
	}

	// A read-only scaling_governor is the case the `g` key fails on, and the
	// report has to hand over the rule that fixes it.
	skipIfRoot(t)
	if err := os.Chmod(filepath.Join(base, "policy0", "scaling_governor"), 0o444); err != nil {
		t.Fatal(err)
	}
	w := CheckGovernorWrite()
	if w.Status != CapReadOnly {
		t.Fatalf("read-only governor: status = %v", w.Status)
	}
	if !strings.Contains(w.Fix, "udev") || !strings.Contains(w.Fix, "scaling_governor") {
		t.Errorf("fix must be the udev rule, got %q", w.Fix)
	}
	if w.Fix != GovernorUdevRule {
		t.Error("fix should be the exported rule verbatim")
	}

	// No cpufreq at all: nothing to grant access to, so no rule either.
	cpufreqBase = filepath.Join(base, "nope")
	if c := CheckCPUFreq(); c.Status != CapAbsent {
		t.Errorf("missing cpufreq: %+v", c)
	}
	if w := CheckGovernorWrite(); w.Status != CapAbsent || w.Fix != "" {
		t.Errorf("missing cpufreq governor: %+v", w)
	}
}

func TestCheckBacklightWrite(t *testing.T) {
	base := t.TempDir()
	old := backlightBase
	backlightBase = base
	defer func() { backlightBase = old }()

	fakeBacklight(t, base, "panel0", "128", "255")
	if c := CheckBacklightWrite(""); c.Status != CapPresent || !strings.Contains(c.Detail, "panel0") {
		t.Errorf("writable backlight: %+v", c)
	}

	skipIfRoot(t)
	if err := os.Chmod(filepath.Join(base, "panel0", "brightness"), 0o444); err != nil {
		t.Fatal(err)
	}
	c := CheckBacklightWrite("")
	if c.Status != CapReadOnly {
		t.Fatalf("read-only brightness: status = %v", c.Status)
	}
	if c.Fix != BacklightUdevRule || !strings.Contains(c.Fix, "SUBSYSTEM==\"backlight\"") {
		t.Errorf("fix must be the backlight udev rule, got %q", c.Fix)
	}

	backlightBase = filepath.Join(base, "nope")
	if c := CheckBacklightWrite(""); c.Status != CapAbsent || c.Fix != "" {
		t.Errorf("no backlight: %+v", c)
	}
}

func TestCheckVideoCoreAbsent(t *testing.T) {
	old := vcgencmdPaths
	vcgencmdPaths = []string{filepath.Join(t.TempDir(), "vcgencmd")}
	defer func() { vcgencmdPaths = old }()
	t.Setenv("PATH", t.TempDir())

	c := CheckVideoCore()
	if c.Status != CapAbsent {
		t.Fatalf("no vcgencmd: status = %v", c.Status)
	}
	if !strings.Contains(c.Affects, "GPU tab") || !strings.Contains(c.Fix, "apt install") {
		t.Errorf("absent vcgencmd must name the tab and the package: %+v", c)
	}
}

func TestCheckIW(t *testing.T) {
	dir := t.TempDir()
	oldPaths, oldWireless := iwPaths, wirelessPath
	defer func() { iwPaths, wirelessPath = oldPaths, oldWireless }()

	iwPaths = []string{filepath.Join(dir, "iw")}
	wirelessPath = filepath.Join(dir, "wireless")
	t.Setenv("PATH", dir)

	if c := CheckIW(); c.Status != CapAbsent || !strings.Contains(c.Fix, "iw") {
		t.Errorf("no iw binary: %+v", c)
	}

	if err := os.WriteFile(iwPaths[0], []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c := CheckIW(); c.Status != CapAbsent || !strings.Contains(c.Detail, "no wireless interface") {
		t.Errorf("iw without wireless hardware: %+v", c)
	}

	if err := os.WriteFile(wirelessPath, []byte(
		"Inter-| sta-|   Quality\n face | tus | link level noise\n wlan0: 0000   70.  -40.  -256\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := CheckIW()
	if c.Status != CapPresent || !strings.Contains(c.Detail, "wlan0") {
		t.Errorf("iw with a wireless interface: %+v", c)
	}
}

func TestCheckModemManagerAbsent(t *testing.T) {
	old := mmcliPaths
	mmcliPaths = []string{filepath.Join(t.TempDir(), "mmcli")}
	defer func() { mmcliPaths = old }()
	t.Setenv("PATH", t.TempDir())

	c := CheckModemManager()
	if c.Status != CapAbsent || !strings.Contains(c.Fix, "modemmanager") {
		t.Errorf("no mmcli: %+v", c)
	}
}

func TestCheckStorageWear(t *testing.T) {
	base := t.TempDir()
	old := blockBase
	blockBase = base
	defer func() { blockBase = old }()

	// A plain SD card: identity only, no wear counters. That is the common
	// uConsole case, and it must not read as a fault.
	dev := filepath.Join(base, "mmcblk0", "device")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "mmcblk0", "size"), []byte("62333952\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "name"), []byte("SC64G\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := CheckStorageWear()
	if c.Status != CapAbsent || !strings.Contains(c.Detail, "normal for SD") {
		t.Errorf("SD card without wear data: %+v", c)
	}

	// An eMMC publishes the EXT_CSD bands.
	if err := os.WriteFile(filepath.Join(dev, "life_time"), []byte("0x02 0x03\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := CheckStorageWear(); c.Status != CapPresent || !strings.Contains(c.Detail, "mmcblk0") {
		t.Errorf("eMMC with life_time: %+v", c)
	}
}

func TestCheckBatteryAndHealth(t *testing.T) {
	base := t.TempDir()
	old := powerSupplyBase
	powerSupplyBase = base
	defer func() { powerSupplyBase = old }()

	bat := filepath.Join(base, "BAT0")
	if err := os.MkdirAll(bat, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bat, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("type", "Battery")
	write("capacity", "72")
	write("status", "Discharging")

	c := CheckBattery()
	if c.Status != CapPresent || !strings.Contains(c.Detail, "72%") {
		t.Errorf("battery present: %+v", c)
	}
	// No *_full_design: the gauge works, the health percentage cannot.
	h := CheckBatteryHealth()
	if h.Status != CapAbsent || !strings.Contains(h.Detail, "full_design") {
		t.Errorf("battery without design capacity: %+v", h)
	}

	write("energy_full", "40000000")
	write("energy_full_design", "50000000")
	if h := CheckBatteryHealth(); h.Status != CapPresent || !strings.Contains(h.Detail, "80%") {
		t.Errorf("battery with design capacity: %+v", h)
	}

	powerSupplyBase = filepath.Join(base, "nope")
	if c := CheckBattery(); c.Status != CapAbsent {
		t.Errorf("no power supply tree: %+v", c)
	}
	if h := CheckBatteryHealth(); h.Status != CapAbsent || !strings.Contains(h.Detail, "no battery") {
		t.Errorf("health without a battery: %+v", h)
	}
}

func TestCheckProcIO(t *testing.T) {
	base := t.TempDir()
	old := procBase
	procBase = base
	defer func() { procBase = old }()

	self := filepath.Join(base, strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(self, 0o755); err != nil {
		t.Fatal(err)
	}
	io := "rchar: 1\nread_bytes: 4096\nwrite_bytes: 8192\n"
	if err := os.WriteFile(filepath.Join(self, "io"), []byte(io), 0o644); err != nil {
		t.Fatal(err)
	}

	// Own process only: the Processes tab shows dashes for everything else.
	c := CheckProcIO()
	if c.Status != CapDenied || !strings.Contains(c.Fix, "cap_sys_ptrace") {
		t.Errorf("without pid 1: %+v", c)
	}

	init := filepath.Join(base, "1")
	if err := os.MkdirAll(init, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(init, "io"), []byte(io), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := CheckProcIO(); c.Status != CapPresent || c.Fix != "" {
		t.Errorf("with pid 1 readable: %+v", c)
	}

	// No io file at all is a kernel build option, not a permission.
	if err := os.Remove(filepath.Join(self, "io")); err != nil {
		t.Fatal(err)
	}
	if c := CheckProcIO(); c.Status != CapAbsent || !strings.Contains(c.Detail, "CONFIG_TASK_IO_ACCOUNTING") {
		t.Errorf("without io accounting: %+v", c)
	}
}

func TestCheckICMPVerdicts(t *testing.T) {
	if c := checkICMP(nil, 0, 2147483647, true, 1000); c.Status != CapPresent {
		t.Errorf("working socket: %+v", c)
	} else if !strings.Contains(c.Detail, "gid 1000") {
		t.Errorf("present detail should name the range and gid: %q", c.Detail)
	}

	c := checkICMP(unix.EACCES, 1, 0, true, 1000)
	if c.Status != CapDenied {
		t.Fatalf("EACCES: status = %v, want denied", c.Status)
	}
	if !strings.Contains(c.Fix, "ping_group_range") || !strings.Contains(c.Detail, "1 0") {
		t.Errorf("denied ICMP must show the range and the sysctl: %+v", c)
	}

	// Anything that is not a permission refusal is the socket type missing,
	// which no sysctl brings back.
	if c := checkICMP(unix.EAFNOSUPPORT, 0, 0, false, 0); c.Status != CapAbsent {
		t.Errorf("EAFNOSUPPORT: status = %v, want absent", c.Status)
	}
}
