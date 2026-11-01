//go:build e2e

// Package e2e drives the real ucmon binary end to end inside the sealed
// container e2e/run.sh starts: no network, a read-only root, an unprivileged
// user, real kernel mounts described by a fixture /etc/fstab, and a real
// pseudo-terminal for the TUI. Unit tests fake /proc and /sys; these do not —
// they check what a user of the shipped binary sees.
//
// Run with `make e2e` (or e2e/run.sh). The build tag keeps `go test ./...`
// from picking the package up on a developer machine, where none of the
// sandbox it asserts on exists.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// The mounts run.sh creates, as declared in testdata/fstab.
	mntLocked = "/mnt/locked"   // fstab: rw — mounted ro: the remount case
	mntMeant  = "/mnt/meant-ro" // fstab: ro — mounted ro by design
	mntData   = "/mnt/data"     // fstab: rw — mounted rw
)

func bin() string {
	if b := os.Getenv("UCMON_BIN"); b != "" {
		return b
	}
	return "/usr/local/bin/ucmon"
}

func TestMain(m *testing.M) {
	if os.Getenv("UCMON_E2E_HOG") == "1" {
		hog()
	}
	if os.Getenv("UCMON_E2E_SANDBOX") != "1" {
		fmt.Fprintln(os.Stderr, "e2e: refusing to run outside the sandbox; use e2e/run.sh or `make e2e`")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// hog is the OOM victim: the test binary re-executed with UCMON_E2E_HOG=1.
// It volunteers as the OOM killer's first choice, then touches memory until
// the container's cgroup limit is hit and the kernel kills it.
func hog() {
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0)
	for {
		b := make([]byte, 16<<20)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
		hogHeld = append(hogHeld, b)
	}
}

// hogHeld keeps the hog's pages referenced so the GC cannot hand them back.
var hogHeld [][]byte

// baseConfig keeps every run offline and quick. Tests append to it.
const baseConfig = `
ping_enabled  = false   # hermetic: there is no network to ping
fast_interval = 250ms
proc_interval = 500ms
slow_interval = 500ms
`

func writeConfig(t *testing.T, extra string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ucmon.toml")
	if err := os.WriteFile(p, []byte(baseConfig+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type result struct {
	stdout, stderr string
	code           int
}

// run executes ucmon to completion and fails the test on a panic.
func run(t *testing.T, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin(), args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.code = ee.ExitCode()
	default:
		t.Fatalf("ucmon %v: %v", args, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("ucmon %v: did not finish within 30s", args)
	}
	noPanic(t, r.stdout+r.stderr)
	return r
}

func noPanic(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "panic:") || strings.Contains(out, "goroutine ") && strings.Contains(out, "[running]") {
		t.Fatalf("ucmon panicked:\n%s", out)
	}
}

type snapshot struct {
	Version string `json:"version"`
	Disks   []struct {
		Mount       string `json:"mount"`
		ReadOnly    bool   `json:"read_only"`
		RemountedRO bool   `json:"remounted_ro"`
	} `json:"disks"`
	Alerts []struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"active_alerts"`
}

func onceJSON(t *testing.T, cfg string) snapshot {
	t.Helper()
	r := run(t, "--config", cfg, "--once", "--json")
	if r.code != 0 {
		t.Fatalf("--once --json exited %d: %s", r.code, r.stderr)
	}
	var s snapshot
	if err := json.Unmarshal([]byte(r.stdout), &s); err != nil {
		t.Fatalf("--once --json is not JSON: %v\n%s", err, r.stdout)
	}
	return s
}

// requireVolumes skips when the host's Docker volumes live on a filesystem
// ucmon does not list (tmpfs, or a nodev type), which would make every
// mount assertion vacuous rather than wrong.
func requireVolumes(t *testing.T, s snapshot) {
	t.Helper()
	for _, d := range s.Disks {
		if d.Mount == mntLocked {
			return
		}
	}
	t.Skipf("%s is not listed as a disk: the Docker volume driver's filesystem is not a block device here", mntLocked)
}

// --- the sandbox itself ------------------------------------------------------

// The suite is only hermetic if the sandbox is: fail loudly if run.sh's
// flags were dropped rather than quietly testing against the host.
func TestSandboxIsSealed(t *testing.T) {
	ifaces, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifaces {
		if i.Name() != "lo" {
			t.Errorf("network interface %q present: the container is not --network none", i.Name())
		}
	}
	if os.Geteuid() == 0 {
		t.Error("running as root: ucmon's unprivileged paths are not being exercised")
	}
	if err := os.WriteFile("/usr/local/bin/probe", nil, 0o644); err == nil {
		t.Error("root filesystem is writable: the container is not --read-only")
	}
	if err := os.WriteFile(filepath.Join(mntLocked, "probe"), nil, 0o644); err == nil || !errors.Is(err, syscall.EROFS) {
		t.Errorf("%s is writable (err=%v): the read-only volume is missing", mntLocked, err)
	}
	if !memoryLimited() {
		t.Error("no cgroup memory limit: the OOM test would exhaust the host instead")
	}
	if fi, err := os.Stat("/dev/kmsg"); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("/dev/kmsg is not run.sh's FIFO (%v): tests would see the host's kernel log", err)
	}
}

// writeKmsg feeds kernel records to the ucmon reading run.sh's /dev/kmsg
// FIFO. Opening for write without blocking fails until a reader has it open,
// so it retries until the TUI is following the log. "{now}" in a record
// becomes the current CLOCK_MONOTONIC_RAW in µs, which the kernel's log clock
// tracks: ucmon dates events by it.
func writeKmsg(t *testing.T, records ...string) {
	t.Helper()
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); err != nil {
		t.Fatal(err)
	}
	now := strconv.FormatInt(ts.Nano()/1000, 10)
	for i, r := range records {
		records[i] = strings.ReplaceAll(r, "{now}", now)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile("/dev/kmsg", os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer func() { _ = f.Close() }()
			for _, r := range records {
				if _, err := f.WriteString(r + "\n"); err != nil {
					t.Fatalf("write /dev/kmsg: %v", err)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reader on /dev/kmsg after 10s: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// memoryLimited reports whether this container's cgroup (v2) has a memory
// cap, which the OOM test needs to kill its hog without hurting the host.
func memoryLimited() bool {
	b, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	return err == nil && strings.TrimSpace(string(b)) != "max"
}

// run.sh reruns the signal tests with pidfd_open refused by seccomp. Check
// each run gets the path it claims to, so a profile that silently stopped
// applying (or a default that started refusing) cannot pass as coverage.
func TestPidfdMode(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err == nil {
		_ = unix.Close(fd)
	}
	want := map[string]error{"": nil, "eperm": unix.EPERM, "enosys": unix.ENOSYS}
	mode := os.Getenv("UCMON_E2E_PIDFD")
	w, ok := want[mode]
	if !ok {
		t.Fatalf("unknown UCMON_E2E_PIDFD=%q", mode)
	}
	if !errors.Is(err, w) {
		t.Fatalf("pidfd_open = %v, want %v for mode %q", err, w, mode)
	}
}

// --- CLI contract --------------------------------------------------------------

func TestVersion(t *testing.T) {
	r := run(t, "--version")
	if r.code != 0 || !regexp.MustCompile(`^ucmon \d+\.\d+\.\d+\n$`).MatchString(r.stdout) {
		t.Fatalf("--version = %q (exit %d)", r.stdout, r.code)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	if r := run(t, "--no-such-flag"); r.code == 0 {
		t.Fatal("an unknown flag exited 0")
	}
}

func TestCheckConfig(t *testing.T) {
	r := run(t, "--config", writeConfig(t, "temp_warnn = 90\n"), "--check-config")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "temp_warnn") {
		t.Fatalf("a typo'd key: exit %d, output %q", r.code, r.stdout+r.stderr)
	}
	if r := run(t, "--config", writeConfig(t, ""), "--check-config"); r.code != 0 {
		t.Fatalf("a clean config exited %d: %s", r.code, r.stdout+r.stderr)
	}
}

// Inside the sandbox nearly every optional source is missing (no vcgencmd,
// no battery, no backlight, no ICMP). That is the degraded board doctor
// exists to explain, and it must still exit 0.
func TestDoctorOnADegradedHost(t *testing.T) {
	r := run(t, "doctor")
	if r.code != 0 {
		t.Fatalf("doctor exited %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{"[absent]", "GPU / VideoCore", "backlight brightness", "storage wear"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("doctor output lacks %q", want)
		}
	}
}

// --- read-only remount detection, against real kernel mounts --------------

func TestOnceReportsRemountedFilesystems(t *testing.T) {
	cfg := writeConfig(t, "")
	s := onceJSON(t, cfg)
	requireVolumes(t, s)

	got := map[string][2]bool{}
	for _, d := range s.Disks {
		got[d.Mount] = [2]bool{d.ReadOnly, d.RemountedRO}
	}
	want := map[string][2]bool{
		mntLocked: {true, true},   // fstab rw, mounted ro
		mntMeant:  {true, false},  // ro by design
		mntData:   {false, false}, // healthy
	}
	for m, w := range want {
		if g, ok := got[m]; !ok || g != w {
			t.Errorf("%s: (read_only, remounted_ro) = %v (listed=%v), want %v", m, g, ok, w)
		}
	}

	var crit int
	for _, a := range s.Alerts {
		if strings.Contains(a.Message, "remounted read-only") {
			if !strings.Contains(a.Message, mntLocked) || a.Severity != "CRIT" {
				t.Errorf("unexpected remount alert: %+v", a)
			}
			crit++
		}
	}
	if crit != 1 {
		t.Errorf("%d remount alerts, want exactly 1 (for %s): %+v", crit, mntLocked, s.Alerts)
	}

	text := run(t, "--config", cfg, "--once").stdout
	for _, want := range []string{mntLocked, "[REMOUNTED READ-ONLY]", "[ro]"} {
		if !strings.Contains(text, want) {
			t.Errorf("text report lacks %q", want)
		}
	}
}

// disk_ignore must hide a mount from the report and the alert engine alike.
func TestDiskIgnoreSilencesRemount(t *testing.T) {
	s := onceJSON(t, writeConfig(t, ""))
	requireVolumes(t, s)

	s = onceJSON(t, writeConfig(t, `disk_ignore = "/mnt/locked"`+"\n"))
	for _, d := range s.Disks {
		if d.Mount == mntLocked {
			t.Errorf("%s still listed despite disk_ignore", mntLocked)
		}
	}
	for _, a := range s.Alerts {
		if strings.Contains(a.Message, mntLocked) {
			t.Errorf("alert for an ignored mount: %+v", a)
		}
	}
}

// --- Prometheus exporter ---------------------------------------------------

func TestMetricsExporter(t *testing.T) {
	const addr = "127.0.0.1:19100"
	cmd := exec.Command(bin(), "--config", writeConfig(t, ""), "--metrics", addr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	get := func(path string) (int, string) { return httpGet(t, addr, path) }

	if !waitHealthy(addr, 10*time.Second) {
		t.Fatalf("exporter never became healthy; stderr: %s", stderr.String())
	}

	code, metrics := get("/metrics")
	if code != 200 {
		t.Fatalf("/metrics = %d", code)
	}
	promtoolCheck(t, metrics)
	for _, want := range []string{
		"ucmon_cpu_usage_percent", "ucmon_memory_used_percent", "ucmon_alerts_active",
		"# TYPE ucmon_filesystem_remounted_readonly gauge",
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if strings.Contains(metrics, `mount="`+mntLocked+`"`) &&
		!strings.Contains(metrics, `ucmon_filesystem_remounted_readonly{mount="`+mntLocked+`",fstype=`) {
		t.Errorf("%s exported without its remount gauge", mntLocked)
	}

	code, body := get("/snapshot")
	if code != 200 || !json.Valid([]byte(body)) {
		t.Errorf("/snapshot = %d, valid JSON %v", code, json.Valid([]byte(body)))
	}
	if code, _ := get("/no-such-path"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}

	// A second exporter on the same port must fail, not serve nothing.
	if r := run(t, "--metrics", addr); r.code == 0 {
		t.Error("a second exporter on an occupied port exited 0")
	}

	// SIGTERM is how systemd and Docker stop it: a clean, prompt exit.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("exporter exited with %v on SIGTERM; stderr: %s", err, stderr.String())
		}
		exited <- err // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("exporter ignored SIGTERM")
	}
	noPanic(t, stderr.String())
}

// httpClient bounds every request: an exporter that accepts and then hangs
// must fail the test, not stall it until the suite timeout.
var httpClient = &http.Client{Timeout: 10 * time.Second}

func httpGet(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	resp, err := httpClient.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// waitHealthy polls /healthz until the exporter answers or the timeout ends.
func waitHealthy(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if resp, err := httpClient.Get("http://" + addr + "/healthz"); err == nil {
			_ = resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// promtoolAllowed lists findings that are deliberate. Renaming a metric
// breaks every dashboard built on it, so a lint about naming is accepted here
// knowingly; anything else promtool reports — a parse error, a duplicate
// series, a bad label — fails the test.
var promtoolAllowed = []string{
	`ucmon_ping_rtt_milliseconds use base unit "seconds" instead of "milliseconds"`,
}

func promtoolCheck(t *testing.T, metrics string) {
	t.Helper()
	cmd := exec.Command("promtool", "check", "metrics")
	cmd.Stdin = strings.NewReader(metrics)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	var problems []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		allowed := false
		for _, a := range promtoolAllowed {
			allowed = allowed || strings.TrimSpace(line) == a
		}
		if !allowed && strings.TrimSpace(line) != "" {
			problems = append(problems, line)
		}
	}
	if len(problems) > 0 {
		t.Errorf("promtool rejects /metrics (%v):\n%s", err, strings.Join(problems, "\n"))
	}
}

// --- the TUI on a real terminal -------------------------------------------

func startTUI(t *testing.T, cfg string, args ...string) *term {
	t.Helper()
	start := time.Now()
	tm := startTerm(t, 120, 40, bin(), append([]string{"--config", cfg}, args...)...)
	tm.waitFor("CPU", 10*time.Second)
	// The terminal answers ucmon's startup queries (see term.answer), so
	// the first frame is prompt; seconds here means it is waiting on a
	// reply again — the 5s termenv timeout a silent terminal costs.
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("first frame took %v on a responsive terminal", d)
	}
	return tm
}

// quit sends ctrl+c, ucmon's quit key (q only closes overlays). In raw mode
// it arrives as the byte 0x03, not as SIGINT.
func quit(t *testing.T, tm *term) {
	t.Helper()
	tm.send("\x03")
	if err := tm.wait(10 * time.Second); err != nil {
		t.Fatalf("ucmon exited with %v after ctrl+c", err)
	}
	// Bubble Tea must leave the alternate screen, or the user's shell is
	// left drawing on a blank buffer.
	if !strings.Contains(tm.raw(), "\x1b[?1049l") {
		t.Error("the alternate screen was not exited on quit")
	}
	noPanic(t, tm.screen())
}

func TestTUIStartsCyclesTabsAndQuits(t *testing.T) {
	tm := startTUI(t, writeConfig(t, ""))
	for _, k := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		tm.send(k)
	}
	m := tm.mark()
	tm.send("?")
	tm.waitForAfter(m, "filter connections", 5*time.Second) // a help overlay line
	tm.send("?")
	quit(t, tm)
}

// Resizing — down past the minimum frame and back — must never panic: the
// layout maths runs on every SIGWINCH.
func TestTUISurvivesResizes(t *testing.T) {
	tm := startTUI(t, writeConfig(t, ""), "--tab", "proc")
	for _, sz := range [][2]int{{80, 24}, {40, 10}, {20, 4}, {200, 60}, {30, 6}, {120, 40}} {
		tm.resize(sz[0], sz[1])
		time.Sleep(150 * time.Millisecond)
	}
	m := tm.mark()
	tm.resize(20, 4) // narrower than the full notice: the short form shows
	tm.waitForAfter(m, "need 30×6", 5*time.Second)
	tm.resize(120, 40)
	quit(t, tm)
}

func TestTUIDiskTabShowsRemount(t *testing.T) {
	cfg := writeConfig(t, "")
	requireVolumes(t, onceJSON(t, cfg))
	tm := startTUI(t, cfg, "--tab", "disk")
	tm.waitFor("REMOUNTED READ-ONLY", 10*time.Second)
	quit(t, tm)
}

// The full kill flow on a real process: search for it, K, confirm. The
// signal goes through the pidfd path, inside a seccomp-filtered container.
func TestTUIKillsTheSelectedProcess(t *testing.T) {
	victim, victimDone := startVictim(t, "31337")
	tm := startTUI(t, writeConfig(t, ""), "--tab", "proc")
	selectProc(tm, "31337", victim.Process.Pid)

	tm.send("K")
	tm.waitFor(fmt.Sprintf("SIGKILL to sleep (pid %d)?", victim.Process.Pid), 5*time.Second)
	tm.send("y")

	select {
	case err := <-victimDone:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Errorf("victim ended with %v, want SIGKILL", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the confirmed SIGKILL never reached the process")
	}
	quit(t, tm)
}

// startVictim starts `sleep <arg>` for a test to act on; arg doubles as a
// unique string to search the process list for.
func startVictim(t *testing.T, arg string) (*exec.Cmd, chan error) {
	t.Helper()
	victim := exec.Command("sleep", arg)
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- victim.Wait() }()
	t.Cleanup(func() { _ = victim.Process.Kill() })
	return victim, done
}

// selectProc filters the process tab down to the victim, leaving the cursor
// on it.
// It waits for the victim's command line in the selected-row detail line,
// drawn after the filter applied: a bare PID like "23" is already on screen
// in a percentage or the clock, and waiting for it confirmed nothing.
func selectProc(tm *term, search string, pid int) {
	tm.t.Helper()
	m := tm.mark()
	tm.send("/")
	tm.send(strings.Split(search, "")...)
	tm.send("\r")
	tm.waitForAfter(m, "sleep "+search, 10*time.Second)
	tm.waitForAfter(m, fmt.Sprint(pid), 5*time.Second)
}

// procStat reads the scheduler state (field 3) and nice value (field 19)
// from /proc/<pid>/stat, i.e. what the kernel says, not what ucmon shows.
func procStat(t *testing.T, pid int) (state string, nice string) {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("read stat of %d: %v", pid, err)
	}
	// comm (field 2) may hold spaces; the fields after it start past ") ".
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+2:]))
	return f[0], f[16]
}

func waitProcState(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, _ := procStat(t, pid)
		if state == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is in state %q, want %q", pid, state, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The detail panel's actions, checked against the kernel: SIGSTOP and
// SIGCONT really stop and resume the process, + really renices it, and -
// fails visibly without CAP_SYS_NICE instead of pretending. When the process
// dies, the panel says so rather than acting on whatever takes the PID.
func TestTUIProcessDetailActsOnTheProcess(t *testing.T) {
	victim, victimDone := startVictim(t, "31338")
	pid := victim.Process.Pid
	tm := startTUI(t, writeConfig(t, ""), "--tab", "proc")
	selectProc(tm, "31338", pid)
	m := tm.mark()
	tm.send("\r")
	tm.waitForAfter(m, "p SIGSTOP", 5*time.Second)

	m = tm.mark()
	tm.send("p")
	tm.waitForAfter(m, fmt.Sprintf("sent SIGSTOP to %d", pid), 5*time.Second)
	waitProcState(t, pid, "T")

	m = tm.mark()
	tm.send("c")
	tm.waitForAfter(m, fmt.Sprintf("sent SIGCONT to %d", pid), 5*time.Second)
	waitProcState(t, pid, "S")

	m = tm.mark()
	tm.send("+")
	tm.waitForAfter(m, "nice → 1", 5*time.Second)
	if _, nice := procStat(t, pid); nice != "1" {
		t.Errorf("nice after + is %s, want 1", nice)
	}

	m = tm.mark()
	tm.send("-")
	tm.waitForAfter(m, "renice 0 failed", 5*time.Second)
	if _, nice := procStat(t, pid); nice != "1" {
		t.Errorf("nice after a refused - is %s, want it left at 1", nice)
	}

	// Killed and reaped behind ucmon's back: the panel must freeze.
	_ = victim.Process.Kill()
	<-victimDone
	tm.waitFor("process exited — esc to close", 10*time.Second)
	tm.send("\x1b")
	quit(t, tm)
}

// Every way a session ends must save history, and SIGHUP — a closed window
// or a dropped SSH session, the usual way on a handheld — once lost it: the
// default SIGHUP action killed the process before the save.
func TestTUIEndsCleanlyAndSavesHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(tm *term)
	}{
		{"hangup", func(tm *term) { tm.hangup() }},
		{"SIGTERM", func(tm *term) { _ = tm.cmd.Process.Signal(syscall.SIGTERM) }},
		{"SIGINT", func(tm *term) { _ = tm.cmd.Process.Signal(syscall.SIGINT) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := filepath.Join(t.TempDir(), "history.json")
			tm := startTUI(t, writeConfig(t, fmt.Sprintf("history_file = %q\n", hist)))
			time.Sleep(time.Second) // a few samples to keep
			tc.end(tm)
			if err := tm.wait(10 * time.Second); err != nil {
				t.Errorf("ucmon exited with %v", err)
			}
			b, err := os.ReadFile(hist)
			if err != nil || !json.Valid(b) {
				t.Errorf("history after %s: err %v, valid JSON %v", tc.name, err, json.Valid(b))
			}
		})
	}
}

// Started with no terminal at all (from cron, or a service unit without
// --once), the TUI must fail at once with a reason, not hang or spin.
func TestTUIWithoutATerminalFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin(), "--config", writeConfig(t, ""))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no controlling tty
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("ucmon with no terminal was still running after 10s")
	}
	if err == nil || !strings.Contains(string(out), "TTY") {
		t.Errorf("no terminal: err %v, output %q; want a failure naming the TTY", err, out)
	}
	noPanic(t, string(out))
}

// metrics_listen in the config runs the exporter alongside the TUI — a
// different code path from --metrics — and a failed exporter must not take
// the monitor down with it.
func TestTUIWithExporter(t *testing.T) {
	const addr = "127.0.0.1:19101"
	tm := startTUI(t, writeConfig(t, fmt.Sprintf("metrics_listen = %q\n", addr)))
	if !waitHealthy(addr, 10*time.Second) {
		t.Fatal("the exporter from metrics_listen never became healthy")
	}
	code, metrics := httpGet(t, addr, "/metrics")
	if code != 200 || !strings.Contains(metrics, "ucmon_cpu_usage_percent") {
		t.Errorf("/metrics under the TUI = %d, %d bytes", code, len(metrics))
	}
	promtoolCheck(t, metrics)
	quit(t, tm)

	// The port is now taken by another exporter: the TUI still runs.
	other := exec.Command(bin(), "--config", writeConfig(t, ""), "--metrics", addr)
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	if !waitHealthy(addr, 10*time.Second) {
		t.Fatal("the port-holding exporter never became healthy")
	}
	tm = startTUI(t, writeConfig(t, fmt.Sprintf("metrics_listen = %q\n", addr)))
	// The failure shows in the TUI (stderr is hidden behind the alt
	// screen), the monitor keeps running, and the error is printed again
	// once the terminal is restored.
	tm.waitFor("metrics exporter: listen tcp "+addr, 10*time.Second)
	m := tm.mark()
	tm.send("2")
	tm.waitForAfter(m, "Swap", 5*time.Second)
	quit(t, tm)
	raw := tm.raw()
	if i := strings.LastIndex(raw, "\x1b[?1049l"); i < 0 || !strings.Contains(stripANSI(raw[i:]), "ucmon: metrics exporter:") {
		t.Error("the exporter's failure was not printed after the TUI exited")
	}
	if code, body := httpGet(t, addr, "/metrics"); code != 200 || !strings.Contains(body, "ucmon_snapshot_age_seconds") {
		t.Errorf("the port-holding exporter stopped answering properly: %d", code)
	}
}

// w writes the report into report_dir; an unwritable report_dir is an error
// on screen, not a crash.
func TestTUIWritesReport(t *testing.T) {
	dir := t.TempDir()
	tm := startTUI(t, writeConfig(t, fmt.Sprintf("report_dir = %q\n", dir)))
	time.Sleep(time.Second) // let the slow probes (disks) fill in
	m := tm.mark()
	tm.send("w")
	tm.waitForAfter(m, "snapshot written: ", 5*time.Second)
	quit(t, tm)

	files, _ := filepath.Glob(filepath.Join(dir, "ucmon-report-*.txt"))
	if len(files) != 1 {
		t.Fatalf("report files in report_dir: %v, want exactly one", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ucmon ", "CPU", "Memory"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report lacks %q", want)
		}
	}

	tm = startTUI(t, writeConfig(t, fmt.Sprintf("report_dir = %q\n", filepath.Join(mntLocked, "reports"))))
	m = tm.mark()
	tm.send("w")
	tm.waitForAfter(m, "snapshot failed: ", 5*time.Second)
	quit(t, tm)
}

// A real OOM kill, inside the container's memory cgroup: the kernel's
// oom_kill counter in /proc/vmstat counts memcg kills too, so ucmon must
// raise its critical alert and show the kill on the Memory tab.
func TestTUIAlertsOnOOMKill(t *testing.T) {
	if !memoryLimited() {
		t.Skip("no cgroup v2 memory limit; refusing to provoke an OOM kill unbounded")
	}
	logf := filepath.Join(t.TempDir(), "alerts.log")
	tm := startTUI(t, writeConfig(t, fmt.Sprintf("alert_log_file = %q\n", logf)), "--tab", "mem")
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(logf)
			t.Logf("alert log:\n%s", b)
		}
	})
	tm.waitFor("OOM kills", 10*time.Second)
	time.Sleep(500 * time.Millisecond) // a baseline sample before the kill

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	victim := exec.Command(self)
	victim.Env = append(os.Environ(), "UCMON_E2E_HOG=1")
	err = victim.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the memory hog ended with %v, want the OOM killer's SIGKILL", err)
	}

	tm.waitFor("this session", 10*time.Second) // the Memory tab counts it
	// The banner shows one alert, and the sandbox's own remount critical
	// may hold it; the Alerts tab lists every active one.
	m := tm.mark()
	tm.send("8")
	tm.waitForAfter(m, "out of memory: the kernel killed a process", 10*time.Second)
	quit(t, tm)

	if b, _ := os.ReadFile(logf); !strings.Contains(string(b), "RAISE CRIT out of memory") {
		t.Errorf("alert log lacks the OOM raise:\n%s", b)
	}
}

// Kernel log records reach the TUI through the real non-blocking read path:
// storage I/O errors raise one critical (however many lines), under-voltage
// a warning, and the Disk and Power tabs keep the session tally.
func TestTUIKernelLogAlerts(t *testing.T) {
	logf := filepath.Join(t.TempDir(), "alerts.log")
	tm := startTUI(t, writeConfig(t, fmt.Sprintf("alert_log_file = %q\n", logf)), "--tab", "disk")
	writeKmsg(t,
		"6,1,{now},-;usb 1-1: new high-speed USB device number 3",
		"3,2,{now},-;I/O error, dev mmcblk9, sector 1234 op 0x1:(WRITE) flags 0x800",
		" SUBSYSTEM=block",
		"3,3,{now},-;Buffer I/O error on dev mmcblk9p2, logical block 0, lost async page write",
		// Not the board's storage, and not the kernel speaking: ignored.
		"3,4,{now},-;blk_update_request: I/O error, dev loop7, sector 0 op 0x0:(READ)",
		"11,5,{now},-;I/O error, dev mmcblk8, sector 1",
		"2,6,{now},-;hwmon hwmon1: Undervoltage detected!",
	)
	// The partition's error counts against its card: one alert, two lines.
	tm.waitFor("kernel I/O errors this session: mmcblk9 2", 10*time.Second)

	m := tm.mark()
	tm.send("8")
	tm.waitForAfter(m, "kernel: storage I/O errors on mmcblk9", 10*time.Second)
	tm.waitForAfter(m, "kernel: under-voltage", 5*time.Second)
	m = tm.mark()
	tm.send("6")
	tm.waitForAfter(m, "under-voltage 1× this session", 5*time.Second)
	quit(t, tm)

	b, _ := os.ReadFile(logf)
	for _, want := range []string{"RAISE CRIT kernel: storage I/O errors on mmcblk9", "RAISE WARN kernel: under-voltage"} {
		if strings.Count(string(b), want) != 1 {
			t.Errorf("alert log should hold %q exactly once:\n%s", want, b)
		}
	}
	for _, never := range []string{"loop7", "mmcblk8"} {
		if strings.Contains(string(b), never) || strings.Contains(tm.screen(), never) {
			t.Errorf("%s should have been ignored", never)
		}
	}
}

// alert_cmd and alert_log_file fire from the TUI: a threshold every host
// trips (memory above 1%) raises an alert on the first tick.
func TestTUIAlertHookAndLog(t *testing.T) {
	dir := t.TempDir()
	hook := filepath.Join(dir, "hook.env")
	logf := filepath.Join(dir, "alerts.log")
	cfg := writeConfig(t, fmt.Sprintf(`
mem_warn_pct   = 1
alert_cmd      = 'env | grep ^UCMON_ALERT_ >> %s'
alert_log_file = %q
`, hook, logf))

	tm := startTUI(t, cfg)
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, _ := os.ReadFile(hook)
		if strings.Contains(string(b), "UCMON_ALERT_STATE=raise") &&
			strings.Contains(string(b), "UCMON_ALERT_SEV=WARN") &&
			strings.Contains(string(b), "UCMON_ALERT_MSG=") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alert_cmd never ran with the UCMON_ALERT_* environment; got %q", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	quit(t, tm)

	b, err := os.ReadFile(logf)
	if err != nil || !strings.Contains(string(b), "RAISE") {
		t.Errorf("alert log = %q, %v; want a RAISE line", b, err)
	}
}

// history_file survives a restart: the first run writes it on quit, the
// second loads it without complaint.
func TestTUIHistoryPersists(t *testing.T) {
	hist := filepath.Join(t.TempDir(), "history.json")
	cfg := writeConfig(t, fmt.Sprintf("history_file = %q\n", hist))

	tm := startTUI(t, cfg)
	time.Sleep(time.Second) // a few samples to keep
	quit(t, tm)

	b, err := os.ReadFile(hist)
	if err != nil || len(b) == 0 || !json.Valid(b) {
		t.Fatalf("history file after quit: %d bytes, err %v, valid JSON %v", len(b), err, json.Valid(b))
	}
	quit(t, startTUI(t, cfg))
}
