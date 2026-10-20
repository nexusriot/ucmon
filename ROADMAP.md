# ucmon roadmap

Planned work and known limitations, roughly in priority order. See
[DESIGN.md](DESIGN.md) for how the current features are built.

## Shipped in 0.11.0

The two "explain yourself" items: ucmon degrades gracefully in a dozen
places, and until now every one of them looked the same from the outside.

**`ucmon doctor`** (also `--doctor`) checks every source ucmon reads and
prints it as present, absent, denied or read-only, with what it found, which
rows go missing without it, and how to get it: thermal sensors, cpufreq
policies and the writability of `scaling_governor`, `/proc/pressure`,
`/proc/<pid>/io` beyond this user's own processes, the battery and its
`*_full_design` counters, `vcgencmd`, the backlight, `iw`, `mmcli`,
unprivileged ICMP (`net.ipv4.ping_group_range`) and the eMMC `life_time` /
`pre_eol_info` registers. For the two writable sources it prints the exact
udev rule, exported as `probe.GovernorUdevRule` / `probe.BacklightUdevRule`
so the test asserts the same text the user pastes.

Each check lives in the probe file that already owns the sampling — a check
that reads the same files, in the same package, cannot drift from the
behaviour it explains — and `internal/ui/doctor.go` only formats. The report
needs no privileges, writes nothing and always exits 0: an absent source is a
fact about the board, not an error. It closes with the config settings that
suppress a reading the host could otherwise produce (`ping_enabled`,
`modem_enabled`, `alerts_enabled`, the ignore globs), which is the half of
"why is that empty" no capability check can see.

**Unrecognised config keys are reported.** `apply` ignored anything it did not
know, so `temp_warnn = 90` did nothing and never said so. `LoadFrom` now
collects what `apply` refused — key plus line number, deduplicated, and only
for lines that carry a key at all, since `[section]` headers, comments and
malformed lines are skipped before `apply` sees them. `ucmon --check-config`
lists them and exits non-zero so a provisioning script can gate on it; the TUI
names them in a one-line startup notice; `doctor` repeats them in its config
section. They stay non-fatal everywhere else by design: a config written for a
newer ucmon has to keep working, and `--config junk.toml --once` still
produces a report.

## Shipped in 0.10.1

A correctness round: five findings, all reproduced on a real host before they
were fixed.

**The exporter and the monitor were sampling each other.** `metrics_listen`
runs the Prometheus exporter on its own goroutine beside the TUI, and both
went through `gopsutil`'s `cpu.Percent(0, …)`, which keeps its baseline in a
package global. Every scrape re-primed the baseline the monitor was about to
diff against, so the frame after a scrape reported the exporter's 600 ms
priming window instead of the second it claimed — or 0% outright, when the two
calls landed close enough together that the busy counters had not moved.
`probe.CPUSampler` now owns its baseline, like the net, disk and process
samplers already did, and reads `/proc/stat` directly: two reads per tick
rather than three, and the aggregate, per-core and time-breakdown figures all
come out of one pair of readings. The same configuration was also a **data
race**: `NewModel` applies the temperature thresholds to process-wide
variables, the exporter calls it per scrape, and the renderer reads them every
frame. `go test -race` reports it; the thresholds are now mutex-guarded.

**Per-partition disk I/O was wrong, and absent on LVM.** `/proc/diskstats`
carries a row per partition as well as per disk, but the mount→counter lookup
stripped the partition suffix before looking anything up. Every partition of a
device therefore reported the *whole disk's* rate — `/boot` and `/boot/efi`
came out identical and far too large — and the names that end in a device
index rather than a partition number were mangled into devices that do not
exist: `/dev/dm-0` became `dm-`, `/dev/mmcblk0` became `mmcblk`. A root
filesystem on LVM or dm-crypt reported no disk I/O at all. The exact device
name is tried first now; the stripped base survives only as a fallback.

**The alert banner picked a different alert every frame.** Active conditions
live in a map and `summary()` took the first of the most severe ones, so a
host with four sensors over `temp_hot` cycled the footer between their
messages several times a second. The tie-break is now the message, which is
also the order the Alerts tab lists them in, so the banner names the condition
at the top of that list.

**cpufreq policies sorted as text.** `policy10` and `policy11` landed between
`policy1` and `policy2` on any board with ten or more frequency domains — one
policy on a Pi, twenty-two on a modern laptop — in the CPU tab, the snapshot
report and the expanded-graph cycle alike. Thermal zones already had a numeric
sort for exactly this; the fallback sensor walk now uses it too.

**The connection table misaligned after a resize.** `WindowSizeMsg` rebuilt the
header but not the rows, which carry the same column widths, so the table sat
crooked until the next socket walk — up to `proc_interval` later.

Plus: `ucmon_filesystem_free_bytes` reported `total - used`, which includes the
root-reserved blocks and so disagreed with the free space the Disk tab and the
JSON snapshot show; and the `AC` row in the text report was one column short of
the block it sits in.

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

- **Per-mount disk I/O on the Disk tab.** The mount→counter mapping is correct
  as of 0.10.1, so the Disk tab can now put the read/write rate on the
  filesystem row itself instead of listing raw block devices underneath it.
  "Which filesystem is being written to" is the question; "which of `dm-0`,
  `nvme0n1` and `nvme0n1p3`" is the current answer.
- **Pin the process cursor to a PID, not a row index.** The list re-sorts by
  CPU every `proc_interval`, and the cursor is an index into it, so the
  selected process changes under you between refreshes — and the detail line
  below the table follows it. htop keeps the selection on the process. The
  same change fixes the mouse wheel on that tab: scrolling moves the viewport,
  then the next refresh snaps it back to wherever the index-based cursor
  landed.
- **Group cores by cpufreq policy.** The per-core grid is a flat list, which
  is right for a CM4's four identical cores and wrong for a big.LITTLE board,
  where the interesting question is whether work is on the P-cores or the
  E-cores. `affected_cpus` already says which cores share a policy, and 0.10.1
  made the policy order numeric; the grid just has to use it.
- **Collapse the temperature list.** A Pi exposes one or two sensors; an x86
  host exposes twenty-odd, most of them per-core `coretemp_core_*` repeating
  the same number. `temp_ignore` can hide them, but only after someone finds
  out they need to. Folding a chip's per-core sensors into one row (hottest
  core, plus the count) by default, expandable on demand, would make the tab
  readable on the "generic Linux x86_64" target without configuration.
- **Acknowledge a single alert.** `m` mutes the whole banner, which is the
  only lever there is when one sensor sits permanently above `temp_hot`.
  Acknowledging one condition — suppressed until it clears or escalates,
  everything else still reaching the banner — is what that situation actually
  wants, and the engine already keys conditions by a stable identity.
- **A systemd unit in the `.deb`.** The package installs the binary and
  nothing else, while the exporter's whole point is running unattended.
  Shipping a disabled `ucmon-metrics.service` (plus the udev rule the governor
  and backlight writes need) would make `--metrics` deployable with
  `systemctl enable`.
- **Metrics logging** — `--log <file.jsonl> --interval 10s` appending one
  `SnapshotJSON` per tick for later graphing. Partly superseded by the
  Prometheus exporter and `--json` (0.10.0), so the remaining case is the host
  with no scraper: a plain file something else rotates and plots afterwards.
  JSONL is the right shape now that `SnapshotJSON` exists, and `--once --json`
  already produces exactly one record of it.
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
  read-only otherwise (`(ro)` marker / hint line). `ucmon doctor` prints the
  exact rule for whichever of the two this host refuses.
- **Alert cmd** runs `/bin/sh -c` with a 10 s timeout and no output capture;
  failures are silent by design (the alert itself is already visible). There is
  no cap on concurrent hooks — a condition flapping with `alert_clear_ticks = 1`
  can hold several shells open at once.
- **Config values are literal**: `splitValue` strips one matched pair of
  surrounding quotes and an unquoted trailing `#` comment, but does not
  interpret TOML escape sequences. Write shell hooks as literal strings
  (`alert_cmd = '…'`) when they contain double quotes.
- **A recognised key with an unparseable value is still silent.**
  `--check-config` (0.11.0) catches a misspelled *key*; `temp_warn = hot`
  leaves the field at its default and says nothing. Reporting the value as
  well is the obvious next step for the same machinery.
- **The exporter collects through a model of its own**, so the readings that
  are scoped to a session — `session_peaks` in `/snapshot`, the ping loss
  window — describe that one collection rather than the exporter's uptime. This is
  deliberate: sharing state with the monitor is what made a scrape corrupt the
  TUI's CPU readings before 0.10.1. Compute windows in Prometheus.
- **The process cursor is a row index**, and the list re-sorts every
  `proc_interval`, so the selection moves to whatever occupies that row next.
  Scrolling the Processes tab with the mouse wheel is undone by the same
  refresh, which scrolls the cursor back into view. See "Next up".
- **Minimum terminal size** is 30×6; below that ucmon draws a one-line notice
  rather than a frame, because the chrome alone (header, footer, box border)
  costs 4 rows and an overflowing frame blanks the alt screen.
