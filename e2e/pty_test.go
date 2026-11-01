//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// term runs a command on a real pseudo-terminal, so the TUI goes through the
// same raw-mode, alt-screen and SIGWINCH paths it does on a board. It is a
// minimal expect: everything the program prints is kept, and waitFor polls it
// with ANSI escapes stripped.
type term struct {
	t    *testing.T
	ptmx *os.File
	cmd  *exec.Cmd

	mu      sync.Mutex
	out     bytes.Buffer
	done    chan struct{} // closed when the reader sees EOF
	exited  chan struct{} // closed once the command's Wait returns
	exitErr error         // Wait's result; read only after exited is closed
}

// startTerm opens a pty of the given size and starts name on it as the
// session leader with the pty as its controlling terminal.
func startTerm(t *testing.T, cols, rows int, name string, args ...string) *term {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	// The ioctls go through SyscallConn: os.File.Fd would switch the
	// master to blocking mode, and then closing it (hangup) would not
	// interrupt the reader's read — the hangup would wait for ucmon's next
	// write.
	var n int
	if err := control(ptmx, func(fd int) error {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
			return fmt.Errorf("unlockpt: %w", err)
		}
		var err error
		n, err = unix.IoctlGetInt(fd, unix.TIOCGPTN)
		return err
	}); err != nil {
		t.Fatalf("pty setup: %v", err)
	}
	tty, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pts: %v", err)
	}
	defer func() { _ = tty.Close() }() // the child holds its own copies

	tm := &term{t: t, ptmx: ptmx, done: make(chan struct{}), exited: make(chan struct{})}
	tm.resize(cols, rows)

	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	tm.cmd = cmd

	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				tm.mu.Lock()
				tm.out.Write(buf[:n])
				tm.mu.Unlock()
				tm.answer(buf[:n])
			}
			if err != nil {
				close(tm.done)
				return
			}
		}
	}()
	go func() {
		tm.exitErr = cmd.Wait()
		close(tm.exited)
	}()

	t.Cleanup(func() {
		// Whether the program already exited is known from tm.exited, not by
		// reading cmd.ProcessState while Wait may still be writing it.
		select {
		case <-tm.exited:
		default:
			_ = tm.cmd.Process.Kill()
			select {
			case <-tm.exited:
			case <-time.After(5 * time.Second):
				t.Errorf("pid %d survived SIGKILL for 5s", tm.cmd.Process.Pid)
			}
		}
		_ = ptmx.Close()
		if t.Failed() {
			t.Logf("terminal output (escapes stripped, last 4 KiB):\n%s", tail(tm.screen(), 4096))
		}
	})
	return tm
}

// answer replies to the terminal queries a real emulator answers. At
// startup termenv asks for the background colour (OSC 11) followed by a
// cursor-position report (DSR) as a sentinel, and waits up to 5s for them;
// a terminal that never replies stalls the first frame for that long.
func (tm *term) answer(chunk []byte) {
	if bytes.Contains(chunk, []byte("\x1b]11;?")) {
		_, _ = tm.ptmx.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
	}
	if bytes.Contains(chunk, []byte("\x1b[6n")) {
		_, _ = tm.ptmx.Write([]byte("\x1b[1;1R"))
	}
}

// resize changes the window size; the kernel delivers SIGWINCH to the
// foreground process group, exactly as a terminal emulator would.
func (tm *term) resize(cols, rows int) {
	tm.t.Helper()
	ws := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}
	if err := control(tm.ptmx, func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws) }); err != nil {
		tm.t.Fatalf("TIOCSWINSZ: %v", err)
	}
}

// control runs f on f's descriptor without taking it out of non-blocking
// mode, as os.File.Fd would.
func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// hangup closes the terminal the way a closed window or a dropped SSH session
// does: the kernel hangs up the slave side and sends SIGHUP to the program,
// which is the pty's session leader.
func (tm *term) hangup() {
	tm.t.Helper()
	if err := tm.ptmx.Close(); err != nil {
		tm.t.Fatalf("close ptmx: %v", err)
	}
}

// send types keys. Each string is written separately with a short gap, so a
// multi-byte key is never split and Bubble Tea sees distinct key events.
func (tm *term) send(keys ...string) {
	tm.t.Helper()
	for _, k := range keys {
		if _, err := tm.ptmx.Write([]byte(k)); err != nil {
			tm.t.Fatalf("write %q: %v", k, err)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

// raw is everything printed so far, escapes included.
func (tm *term) raw() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.out.String()
}

// screen is the output with ANSI escapes removed and cursor moves turned
// into newlines, which is enough to substring-match rendered text.
func (tm *term) screen() string { return stripANSI(tm.raw()) }

// mark returns the current output length, so waitForAfter can ignore text
// that was already on screen before an action.
func (tm *term) mark() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.out.Len()
}

func (tm *term) waitFor(want string, timeout time.Duration) {
	tm.t.Helper()
	tm.waitForAfter(0, want, timeout)
}

func (tm *term) waitForAfter(from int, want string, timeout time.Duration) {
	tm.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		tm.mu.Lock()
		s := stripANSI(string(tm.out.Bytes()[from:]))
		tm.mu.Unlock()
		if strings.Contains(s, want) {
			return
		}
		if time.Now().After(deadline) {
			tm.t.Fatalf("timed out after %v waiting for %q on screen", timeout, want)
		}
		select {
		case <-tm.exited:
			tm.t.Fatalf("program exited (%v) while waiting for %q", tm.exitErr, want)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// wait returns the exit error once the program ends, or fails the test.
func (tm *term) wait(timeout time.Duration) error {
	tm.t.Helper()
	select {
	case <-tm.exited:
		// Drain what the program printed on the way out (alt-screen exit).
		select {
		case <-tm.done:
		case <-time.After(time.Second):
		}
		return tm.exitErr
	case <-time.After(timeout):
		tm.t.Fatalf("program still running after %v", timeout)
		return nil
	}
}

var (
	reCursorMove = regexp.MustCompile(`\x1b\[[0-9;]*[HfABCDEGd]`)
	reANSI       = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]`)
)

func stripANSI(s string) string {
	s = reCursorMove.ReplaceAllString(s, "\n")
	s = reANSI.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\r", "")
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
