package probe

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// spawnChild starts a long-lived child we own, so the signalling helpers can be
// exercised for real without touching anything else on the machine.
func spawnChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "sleep 60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a child here: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// waitForStatus polls until the process reports one of want, or gives up.
func waitForStatus(t *testing.T, pid int32, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		d, err := GetProcDetail(pid)
		if err == nil {
			last = d.Status
			for _, w := range want {
				if last == w {
					return last
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

func TestSuspendAndResumeProcess(t *testing.T) {
	child := spawnChild(t)
	pid := int32(child.Process.Pid)

	if err := SuspendProcess(pid); err != nil {
		t.Fatalf("SuspendProcess: %v", err)
	}
	if got := waitForStatus(t, pid, "stop"); got != "stop" {
		t.Errorf("status after SIGSTOP = %q, want stop", got)
	}

	if err := ResumeProcess(pid); err != nil {
		t.Fatalf("ResumeProcess: %v", err)
	}
	if got := waitForStatus(t, pid, "sleep", "idle", "running"); got == "stop" {
		t.Errorf("status after SIGCONT = %q, still stopped", got)
	}
}

func TestKillProcessTerminates(t *testing.T) {
	for _, hard := range []bool{false, true} {
		child := spawnChild(t)
		pid := int32(child.Process.Pid)

		if err := KillProcess(pid, hard); err != nil {
			t.Fatalf("KillProcess(hard=%v): %v", hard, err)
		}
		if _, err := child.Process.Wait(); err != nil {
			t.Errorf("hard=%v: child did not reap: %v", hard, err)
		}
	}
}

// Every signalling helper resolves the pid first, so an impossible pid is a
// clean error rather than a signal sent to something else.
func TestProcOpsRejectImpossiblePID(t *testing.T) {
	const gone = int32(0x7FFFFFFE)
	if err := KillProcess(gone, false); err == nil {
		t.Error("KillProcess on a nonexistent pid succeeded")
	}
	if err := SuspendProcess(gone); err == nil {
		t.Error("SuspendProcess on a nonexistent pid succeeded")
	}
	if err := ResumeProcess(gone); err == nil {
		t.Error("ResumeProcess on a nonexistent pid succeeded")
	}
	if _, err := GetProcDetail(gone); err == nil {
		t.Error("GetProcDetail on a nonexistent pid succeeded")
	}
}

// getpriority(2) returns 20-nice at the syscall boundary; ucmon converts it
// back, so a normal process must read 0 and not 20. Getting this wrong made a
// single "+" press land on nice 19 instead of 1.
func TestGetNiceIsRealNiceness(t *testing.T) {
	self := int32(os.Getpid())
	start := getNice(self)
	if start < -20 || start > 19 {
		t.Fatalf("getNice = %d, outside the kernel's -20..19 range", start)
	}

	target := ClampNice(start + 3)
	if err := SetNice(self, target); err != nil {
		t.Fatalf("SetNice(%d): %v", target, err)
	}
	if got := getNice(self); got != target {
		t.Errorf("getNice = %d after SetNice(%d) — the conversion does not round-trip", got, target)
	}

	// The detail overlay reads the same value, so renicing by 1 must move by 1.
	d, err := GetProcDetail(self)
	if err != nil {
		t.Fatal(err)
	}
	if int(d.Nice) != target {
		t.Errorf("ProcDetail.Nice = %d, want %d", d.Nice, target)
	}
	step := ClampNice(int(d.Nice) + 1)
	if step != target+1 {
		t.Errorf("renicing by +1 from %d computes %d, want %d", d.Nice, step, target+1)
	}

	// An impossible pid degrades to 0 rather than a bogus 20.
	if got := getNice(0x7FFFFFFE); got != 0 {
		t.Errorf("getNice on a nonexistent pid = %d, want 0", got)
	}
}

// GetProcDetail on ourselves is the broadest check that the gopsutil field
// mapping still holds — a dependency bump that renamed something would show up
// here rather than as a blank detail overlay.
func TestGetProcDetailOnSelf(t *testing.T) {
	d, err := GetProcDetail(int32(os.Getpid()))
	if err != nil {
		t.Fatalf("GetProcDetail: %v", err)
	}
	if d.PID != int32(os.Getpid()) {
		t.Errorf("PID = %d", d.PID)
	}
	if d.Name == "" {
		t.Error("Name is empty")
	}
	if d.MemRSS == 0 {
		t.Error("MemRSS is zero for a running process")
	}
	if d.Threads < 1 {
		t.Errorf("Threads = %d", d.Threads)
	}
	if d.CreateTime <= 0 {
		t.Errorf("CreateTime = %d", d.CreateTime)
	}
	if d.Exe == "" {
		t.Error("Exe is empty")
	}
}

// ListConnections and ConnectionsForPID read live sockets; the contract worth
// pinning is that every row is well formed and the protocol labels are the ones
// the Network tab filters on.
func TestListConnectionsShape(t *testing.T) {
	conns, err := ListConnections()
	if err != nil {
		t.Skipf("cannot read connections here: %v", err)
	}
	seenListenFirst := true
	sawNonListen := false
	for _, c := range conns {
		switch c.Proto {
		case "TCP", "UDP", "TCP6", "UDP6":
		default:
			t.Errorf("unexpected proto %q in %+v", c.Proto, c)
		}
		if c.LocalAddr == "" {
			t.Errorf("empty LocalAddr in %+v", c)
		}
		if c.RemoteAddr == "" {
			t.Errorf("empty RemoteAddr in %+v (want '-' when unconnected)", c)
		}
		if c.Status != "LISTEN" {
			sawNonListen = true
		} else if sawNonListen {
			seenListenFirst = false
		}
	}
	if !seenListenFirst {
		t.Error("LISTEN sockets are not sorted first")
	}

	// Our own process may own no sockets; nil is a valid answer.
	for _, c := range ConnectionsForPID(int32(os.Getpid())) {
		if c.Proto == "" {
			t.Errorf("unlabelled proto in %+v", c)
		}
	}
}
