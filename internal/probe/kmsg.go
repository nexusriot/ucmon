package probe

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// KernelEventKind is what a kernel log line means for the board.
type KernelEventKind int

const (
	KernelUnderVoltage KernelEventKind = iota // the PMIC saw the supply sag
	KernelStorageError                        // block, MMC or filesystem I/O error
	KernelOOMKill                             // the OOM killer named its victim
)

// KernelEvent is one kernel log record that ucmon alerts on.
type KernelEvent struct {
	Kind KernelEventKind
	At   time.Time // when the kernel logged it, from the record's timestamp
	// KernelStorageError: the whole disk (mmcblk0), so one failing card is
	// one alert however many of its names the kernel uses, and the exact
	// name the line used (mmcblk0p2, or the mmc0 host).
	Device, Partition string
	PID               int    // KernelOOMKill: the victim (in the kernel's PID namespace)
	Comm              string // KernelOOMKill: the victim's name
	Text              string // the message, without the record header
}

// kmsgPath is a var so tests can point it at a fixture.
var kmsgPath = "/dev/kmsg"

// kmsgMaxPerRead bounds one drain: a dying card can log thousands of I/O
// errors a second, and the count is what matters, not every line.
const kmsgMaxPerRead = 2000

// mmcHostBase is a var so tests can point it at a fixture.
var mmcHostBase = "/sys/class/mmc_host"

var (
	// "Out of memory: Killed process 1234 (x)" (5.x+, also "Memory cgroup
	// out of memory: …"); older kernels log a bare "Killed process 1234 (x)"
	// after an "Out of memory: Kill process …" line, which is skipped so a
	// kill is not counted twice.
	reOOMVictim  = regexp.MustCompile(`(?:^|[Oo]ut of memory: )Killed process (\d+) \(([^)]*)\)`)
	reBlockIOErr = regexp.MustCompile(`(?:I/O|critical medium|critical target) error,? (?:on )?dev ([\w.-]+)`)
	reBufferErr  = regexp.MustCompile(`Buffer I/O error on (?:dev(?:ice)? )?([\w.-]+)`)
	reMMCErr     = regexp.MustCompile(`^(mmcblk\d+(?:p\d+)?): error -\d+`)
	reMMCHost    = regexp.MustCompile(`^(mmc\d+): (?:Timeout waiting for hardware|Card stuck|cache flush error)`)
	// "EXT4-fs error (device mmcblk0p2): …", "BTRFS error (device sda1): …",
	// "EXT4-fs warning (device sda1): ext4_end_bio: I/O error …"
	reFSDevErr = regexp.MustCompile(`^(?:EXT[234]-fs|BTRFS) (?:error|critical|warning) \(device ([\w.-]+)\)(?:: .*\berror\b|)`)
	// "FAT-fs (mmcblk0p1): error, …", "EXT4-fs (sda1): I/O error while writing superblock"
	reFSParenErr = regexp.MustCompile(`^(?:EXT[234]-fs|FAT-fs|F2FS-fs|XFS) \(([\w.-]+)\): .*\berror\b`)
	// ext4 repeats its stored error record ~5 min after mount and then daily:
	// an old, already-reported error, not a new one.
	reFSErrSummary = regexp.MustCompile(`error count since last fsck|(?:initial|last) error at time`)

	// Partitions and the disk they belong to: mmcblk0p2/nvme0n1p2 → p-suffixed,
	// sda1/vdb3 → digit-suffixed.
	rePPartition     = regexp.MustCompile(`^((?:mmcblk|nvme\d+n|loop|md|nbd)\d+)p\d+$`)
	reDigitPartition = regexp.MustCompile(`^((?:sd|vd|hd|xvd)[a-z]+)\d+$`)
	// Devices whose errors say nothing about the board's storage: loop
	// images (a corrupt snap), optical drives, RAM disks, network blocks.
	reVirtualDisk = regexp.MustCompile(`^(?:loop|sr|zram|ram|nbd)\d+`)
)

// WholeDisk maps a partition name to its disk (mmcblk0p2 → mmcblk0, sda1 →
// sda); a whole-disk name comes back unchanged.
func WholeDisk(dev string) string {
	if m := rePPartition.FindStringSubmatch(dev); m != nil {
		return m[1]
	}
	if m := reDigitPartition.FindStringSubmatch(dev); m != nil {
		return m[1]
	}
	return dev
}

// ClassifyKernelLine turns one kernel message (without the record header)
// into an event, or reports that it is not one ucmon alerts on.
func ClassifyKernelLine(text string) (KernelEvent, bool) {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "under-voltage detected") || strings.Contains(lower, "undervoltage detected") {
		return KernelEvent{Kind: KernelUnderVoltage, Text: text}, true
	}
	if m := reOOMVictim.FindStringSubmatch(text); m != nil {
		pid, _ := strconv.Atoi(m[1])
		return KernelEvent{Kind: KernelOOMKill, PID: pid, Comm: m[2], Text: text}, true
	}
	if reFSErrSummary.MatchString(text) {
		return KernelEvent{}, false
	}
	for _, re := range []*regexp.Regexp{reBlockIOErr, reBufferErr, reMMCErr, reMMCHost, reFSDevErr, reFSParenErr} {
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		if reVirtualDisk.MatchString(m[1]) {
			return KernelEvent{}, false
		}
		return KernelEvent{Kind: KernelStorageError, Device: WholeDisk(m[1]), Partition: m[1], Text: text}, true
	}
	return KernelEvent{}, false
}

// mmcHostDisk resolves an MMC host (mmc0) to the block device on it
// (mmcblk0). A host with none is not storage — on a Pi, mmc1 is usually the
// SDIO WiFi chip — so its timeouts are not a failing card.
func mmcHostDisk(host string) (string, bool) {
	slots, _ := filepath.Glob(filepath.Join(mmcHostBase, host, host+":*", "block", "*"))
	if len(slots) == 0 {
		return "", false
	}
	return filepath.Base(slots[0]), true
}

// parseKmsgRecord splits a /dev/kmsg record "prio,seq,usec,flags;text" into
// its timestamp (µs of the kernel's log clock, see monotonicNow) and message. Continuation lines
// (" KEY=value") carry no message of their own, and records user space wrote
// into the log (facility > 0: systemd, or anything with write access) are not
// the kernel's word on anything.
func parseKmsgRecord(line string) (usec int64, text string, ok bool) {
	if line == "" || line[0] == ' ' {
		return 0, "", false
	}
	head, text, ok := strings.Cut(line, ";")
	if !ok {
		return 0, "", false
	}
	f := strings.SplitN(head, ",", 4)
	if len(f) < 3 {
		return 0, "", false
	}
	if prio, err := strconv.Atoi(f[0]); err != nil || prio>>3 != 0 {
		return 0, "", false
	}
	usec, _ = strconv.ParseInt(f[2], 10, 64)
	return usec, text, true
}

// recordTime turns a record's timestamp into wall-clock time, so an event read
// late (a paused monitor, a backlog) keeps the time it happened.
func recordTime(usec int64, now time.Time, mono time.Duration) time.Time {
	age := mono - time.Duration(usec)*time.Microsecond
	if usec <= 0 || age < 0 {
		return now
	}
	return now.Add(-age)
}

// monotonicNow reads the clock the kernel stamps log records with. That is
// the scheduler clock, which NTP does not slew: CLOCK_MONOTONIC_RAW tracks it,
// while CLOCK_MONOTONIC drifts away from it — measured at 8 s after three
// days of uptime, enough to date an event wrongly and to miss the OOM kill a
// log line names.
func monotonicNow() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); err != nil {
		return 0
	}
	return time.Duration(ts.Nano())
}

// KmsgWatcher follows the kernel log from the moment it was opened.
type KmsgWatcher struct {
	fd    int    // raw: os.File.Fd would switch it back to blocking mode
	carry []byte // a partial line from the previous read (pipes, files)
	buf   []byte
}

// OpenKmsg opens the kernel log, skipping what is already in it: errors from
// before ucmon started are history, not something happening now. It fails
// where the log is unreadable (kernel.dmesg_restrict=1 without CAP_SYSLOG,
// or a container with no /dev/kmsg).
func OpenKmsg() (*KmsgWatcher, error) { return OpenKmsgAt(kmsgPath) }

// OpenKmsgAt follows a kernel log at another path: a FIFO or a file holding
// /dev/kmsg-format records.
func OpenKmsgAt(path string) (*KmsgWatcher, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// /dev/kmsg seeks to the end of the ring with SEEK_END; a FIFO (the e2e
	// stand-in) cannot seek and has no history anyway.
	if _, err := unix.Seek(fd, 0, unix.SEEK_END); err != nil && !errors.Is(err, unix.ESPIPE) {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "seek", Path: path, Err: err}
	}
	return &KmsgWatcher{fd: fd, buf: make([]byte, 8192)}, nil
}

// Close releases the log.
func (w *KmsgWatcher) Close() error { return unix.Close(w.fd) }

// Read drains every record available now and returns the ones ucmon alerts
// on. It never blocks; on an error it still returns what it parsed first.
func (w *KmsgWatcher) Read() ([]KernelEvent, error) {
	var events []KernelEvent
	now, mono := time.Now(), monotonicNow()
	for range kmsgMaxPerRead {
		n, err := unix.Read(w.fd, w.buf)
		switch {
		case errors.Is(err, unix.EAGAIN), err == nil && n == 0:
			// Caught up (EAGAIN), or a FIFO with no writer (EOF).
			return events, nil
		case errors.Is(err, unix.EPIPE):
			// The ring wrapped past us: records were lost, and the next
			// read resumes at the oldest one still there.
			continue
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return events, err
		}
		data := append(w.carry, w.buf[:n]...)
		last := bytes.LastIndexByte(data, '\n')
		if last < 0 {
			// Only a FIFO or file can split a record; a writer that never
			// ends its line must not grow this without bound.
			if len(data) > len(w.buf) {
				data = data[len(data)-len(w.buf):]
			}
			w.carry = data
			continue
		}
		w.carry = append([]byte(nil), data[last+1:]...)
		for _, line := range strings.Split(string(data[:last]), "\n") {
			usec, text, ok := parseKmsgRecord(line)
			if !ok {
				continue
			}
			ev, ok := ClassifyKernelLine(text)
			if !ok {
				continue
			}
			if ev.Kind == KernelStorageError && strings.HasPrefix(ev.Device, "mmc") && !strings.HasPrefix(ev.Device, "mmcblk") {
				disk, ok := mmcHostDisk(ev.Device)
				if !ok {
					continue
				}
				ev.Device = disk
			}
			ev.At = recordTime(usec, now, mono)
			events = append(events, ev)
		}
	}
	return events, nil
}

// CheckKernelLog reports whether the kernel log can be followed.
func CheckKernelLog() Capability {
	c := Capability{
		Name:    "kernel log",
		Source:  kmsgPath,
		Affects: "kernel-log alerts (under-voltage, storage I/O errors, OOM victim names)",
	}
	w, err := OpenKmsg()
	if err == nil {
		_ = w.Close()
		c.Status = CapPresent
		c.Detail = "readable; followed from startup"
		return c
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		c.Status = CapDenied
		c.Detail = fmt.Sprintf("%v — kernel.dmesg_restrict is set", err)
		c.Fix = "echo kernel.dmesg_restrict=0 | sudo tee /etc/sysctl.d/50-ucmon-dmesg.conf && sudo sysctl --system — or run ucmon with CAP_SYSLOG"
	default:
		c.Status = CapAbsent
		c.Detail = err.Error()
	}
	return c
}
