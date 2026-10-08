# ucmon roadmap

Planned work and known limitations, roughly in priority order. See
[DESIGN.md](DESIGN.md) for how the current features are built.

## Shipped in 0.10.0

A review round: six correctness/performance findings, then the whole Tier 1-3
feature list that came out of it.

**Correctness.** A third of the connection table was Unix sockets mislabelled
as TCP — `protoLabel` keyed on the socket *type*, which is `SOCK_STREAM` for
both, so `/run/dbus/system_bus_socket` sat in the table with a filesystem path
in the LOCAL column and status `NONE` (91 of 257 rows on a desktop). The host
table is now inet-only and the family is checked first. The socket walk, which
readlinks every `/proc/<pid>/fd` on the host and is the single most expensive
probe, had no in-flight guard while the three cheaper rounds did. Sparklines of
percentages auto-scaled, so a core idling at 2–3% and one pinned at 97–98% drew
the identical zigzag and a core flat at 100% drew the *lowest* row; percentages
now use a fixed 0–100 axis. `space` silenced the alert bell, hook and log along
with sampling — alerting now keeps running while readings are frozen. A
brightness keypress colliding with a scheduled fetch lit the "⏳ slow" badge;
user-initiated refreshes no longer count as lateness. Plus: `--once` printed
`battery -1%` when no capacity is exposed, the Network tab clipped a row for one
tick when `iw` data landed, and auto-detect picked `acpi_video0` over
`intel_backlight` because it sorts first.

**Performance.** The process walk went through gopsutil's accessors, each of
which re-reads and re-parses its file: `/proc/<pid>/stat` twice,
`/proc/<pid>/status` twice, a full `/etc/passwd` scan per process, and
`/proc/meminfo` parsed *once per process*. Measured over 533 processes that was
~90 ms of work for ~7 ms of actual file reads. `ProcSampler` now reads `/proc`
directly: **~12 ms for the same walk, a 7× improvement**, on exactly the boards
the lateness badge was built for. Reading `stat` also settles PID reuse for
free (column 22 is the start time), closing a known limitation below.

**Features.** **CPU time breakdown** (user/sys/nice/iowait/irq/steal) —
"80% busy" describes a saturated box and one parked behind a slow card
identically, and a dominant iowait share is now called out. **Interface error
and drop counters**, which the sampler already received and discarded.
**Temperature thresholds calibrated from the kernel's own thermal trip
points**, because 50/65/80 was chosen for a Pi and is wrong by tens of degrees
elsewhere. **Per-process disk I/O** as a sortable column — the answer to "what
is thrashing my card". **SD/eMMC wear**: life-time bands, pre-EOL reserve state
and read-only lockout, the characteristic failure of these boards and the one
reading nothing else hints at. **`--once --json` and a Prometheus exporter**
(`--metrics :9100`, or `metrics_listen` alongside the TUI), lazily sampled and
rate-limited. **Predictive alerts** on the rate of change: battery drain with a
time-to-empty, filesystem fill with a time-to-full, temperature climb with a
time-to-critical. **Expanded graphs** (`z`): a full-frame braille chart of one
metric with a real value axis and min/max/avg. **Persistent history** so
sparklines and peaks survive a restart. **4G/LTE modem status** via `mmcli` for
the uConsole extension board. **Peaks reset** (`Z`), and the previously
hard-coded history length, process cap and report directory are now config keys.

## Shipped in 0.6.0 (for context)

CLI flags + `--once` headless report; cpufreq clocks/governor (read + `g`
cycle); PSI on the CPU/Mem/Disk tabs; help overlay (`?`); pause (`space`);
cmdline search; session peaks; SSID/channel/bitrates via `iw`; gateway/target
latency monitor (unprivileged ICMP + TCP fallback) with loss tracking;
process detail overlay with renice/SIGSTOP/SIGCONT; alert flap damping, bell,
`alert_cmd` hook, on-disk alert log; connection state filter, counts, and
TCP6/UDP6 labels; battery health % and power-draw sparkline; interval-accurate
process CPU% and disk I/O rates; first test suite (57 tests).

## Next up

- **Metrics logging** — `--log <file.csv>` appending one row per fast tick for
  later graphing. Partly superseded by the Prometheus exporter and `--json`
  (0.10.0), so the remaining case is the host with no scraper: a plain file
  something else rotates and plots afterwards. JSONL is the better shape now
  that `SnapshotJSON` exists.
- **Services tab** — failed/active systemd units via `systemctl` shell-out
  (same graceful-absence pattern as vcgencmd), with start/restart/stop behind
  the existing y/N confirm flow.
- **Kernel log tail tab** — `dmesg --follow` style view with severity
  coloring; under-voltage and OOM events land exactly there, pairing with the
  Power tab and alert engine (e.g. alert on new `Under-voltage detected!`).
- **Staggered sampling** — proc/slow fetches all fire on the same tick when
  their multiples align (every 15 s by default), a small thundering herd on a
  CM4. In-flight guards (0.8.0) stop the herd from *compounding*, the header
  badge makes it visible, and the 0.10.0 process-walk rewrite removed most of
  the spike, but the schedules still want offsetting.
- **Peaks "since" timestamp** — `Z` resets them (0.10.0), but the header does
  not yet say what window the peaks cover.

## Later / bigger bets

- **Braille graphs everywhere** — `z` expands *one* metric to a braille chart
  (0.10.0); btop-style multi-row charts inline on a tall terminal are still
  open, gated on available height so the uConsole's wide-but-short panel keeps
  1-row sparklines.
- **Modem control** — 0.10.0 reads the modem; connecting, disconnecting and
  switching APN through `mmcli` would need the y/N confirm flow and a careful
  look at what a wrong keypress costs on a metered SIM.
- **Replay** — the ring buffers now persist across restarts (0.10.0); the
  remaining half is `--replay <file>` to scrub a recorded session.
- **Per-process network rates** — needs packet accounting (eBPF or pcap) or
  `/proc/net` diffing per socket; heavier than the current pure-procfs stance,
  so design carefully before committing.
- **Theming** — configurable color palette / mono mode beyond termenv's
  automatic `NO_COLOR` handling.
- **Mouse support** — click tab headers to switch; wheel already scrolls the
  viewports.

## Known limitations

- **WiFi quality scale** assumes the conventional cfg80211 maximum of 70 in
  `/proc/net/wireless`; drivers with other scales skew the percentage (the
  dBm reading is always trustworthy). `iw` reports could calibrate it.
- **Latency monitor**: ICMP needs `net.ipv4.ping_group_range` to include the
  user (default-open on Raspberry Pi OS / systemd ≥ 247). The TCP fallback
  reads a gateway that silently DROPs ports 80/443 as loss. IPv6 targets are
  not supported yet.
- **PSI** requires `CONFIG_PSI=y` (present on Raspberry Pi OS ≥ 5.10 kernels;
  some distros need `psi=1` on the kernel cmdline). The tabs simply omit the
  rows when absent.
- **Battery health** needs `*_full_design` counters; the uConsole axp20x
  driver exposes them on recent kernels only.
- **Storage wear** needs the eMMC 5.0 `life_time` / `pre_eol_info` registers.
  Plain SD cards expose neither — the health row then reports identity and
  size and says "no wear data" rather than guessing. NVMe wear would need a
  SMART/NVMe-admin passthrough, which is a privileged ioctl, not a sysfs read.
- **Per-process disk I/O** reads `/proc/<pid>/io`, which needs
  `CAP_SYS_PTRACE` for processes you do not own; those rows show `-`. The
  counters are also block-layer totals, so a write served from page cache
  appears when it is flushed, not when it is issued.
- **Predictive alerts** extrapolate a straight line from a two-minute window.
  That is the right call for a battery or a filling filesystem and the wrong
  one for a bursty writer, which will raise and clear as the burst passes —
  `alert_clear_ticks` damps the flapping but does not make the slope wiser.
- **The Prometheus exporter** is unauthenticated and unencrypted, like every
  other node exporter. Bind it to a private address or put it behind something
  if the board is reachable from outside.
- **Governor / backlight writes** need root or a udev rule; ucmon shows both
  read-only otherwise (`(ro)` marker / hint line).
- **Alert cmd** runs `/bin/sh -c` with a 10 s timeout and no output capture;
  failures are silent by design (the alert itself is already visible). There is
  no cap on concurrent hooks — a condition flapping with `alert_clear_ticks = 1`
  can hold several shells open at once.
- **Config values are literal**: `splitValue` strips one matched pair of
  surrounding quotes and an unquoted trailing `#` comment, but does not
  interpret TOML escape sequences. Write shell hooks as literal strings
  (`alert_cmd = '…'`) when they contain double quotes.
- **Minimum terminal size** is 30×6; below that ucmon draws a one-line notice
  rather than a frame, because the chrome alone (header, footer, box border)
  costs 4 rows and an overflowing frame blanks the alt screen.
