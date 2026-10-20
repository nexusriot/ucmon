# ucmon - uConsole/Raspberry Pi TUI System Monitor

## Overview

ucmon is a terminal-based system monitoring tool designed for the ClockworkPi uConsole and Raspberry Pi devices. It provides real-time monitoring of CPU, processes, disk, and network in a compact TUI interface optimized for small screens.

Built with Go using the Charmbracelet Bubble Tea TUI framework (Elm architecture).

## Architecture

```
ucmon/
├── cmd/ucmon/main.go           # Entry point + CLI flags (--once/--json/--metrics/--doctor/--check-config/--config/--tab/--version)
├── internal/
│   ├── probe/                  # Data collection layer
│   │   ├── cpu.go              # CPUSampler (interval utilisation + time breakdown), temperature sensors
│   │   ├── cpufreq.go          # cpufreq policies: clocks + governor read/set (sysfs)
│   │   ├── mem.go              # RAM & swap
│   │   ├── procs.go            # ProcSampler: direct /proc walk, interval CPU%/IO, detail, signals
│   │   ├── disk.go             # Disk usage & I/O counters
│   │   ├── net.go              # Network interfaces & connections (TCP6/UDP6 aware)
│   │   ├── wifi.go             # WiFi quality (/proc/net/wireless) + SSID/bitrate (iw)
│   │   ├── latency.go          # Gateway/target RTT: unprivileged ICMP + TCP fallback
│   │   ├── psi.go              # Pressure stall information (/proc/pressure)
│   │   ├── power.go            # Battery (incl. health), RPi throttle, load average
│   │   ├── storage.go          # SD/eMMC wear, pre-EOL, read-only lockout (sysfs)
│   │   ├── modem.go            # WWAN modem status via mmcli (ModemManager)
│   │   ├── gpu.go              # VideoCore clocks/temp/volts/mem (vcgencmd)
│   │   ├── backlight.go        # Display backlight read/set (/sys/class/backlight)
│   │   ├── capability.go       # Capability type + the doctor check set (each probe owns its own check)
│   │   └── util.go             # Formatting helpers
│   ├── config/                 # Optional TOML-subset config loader
│   │   ├── config.go           # Parsing, defaults, Sanitize, unrecognised-key collection
│   │   └── ignore.go           # net/disk/temp ignore globs
│   └── ui/                     # Presentation layer
│       ├── model.go            # Bubble Tea model (state, Update, View)
│       ├── alerts.go           # Alert engine (flap damping) + delivery (bell/cmd/file)
│       ├── behind.go           # Fetch guards + sampling-lateness badge
│       ├── filter.go           # Applies the ignore lists to incoming snapshots
│       ├── notice.go           # Self-expiring transient status lines
│       ├── detail.go           # Process detail overlay (renice, STOP/CONT, kill)
│       ├── help.go             # Key-binding help overlay (?)
│       ├── peaks.go            # Session peak tracker
│       ├── once.go             # RunOnce / RunOnceJSON: headless one-shot snapshot
│       ├── doctor.go           # Formats the capability report (ucmon doctor)
│       ├── config_check.go     # --check-config report + the startup notice
│       ├── metrics.go          # JSON snapshot + Prometheus text exposition
│       ├── exporter.go         # HTTP server for --metrics / metrics_listen
│       ├── history.go          # sparkline + peak persistence across restarts
│       ├── trend.go            # rate-of-change tracking behind predictive alerts
│       ├── zoom.go             # full-screen braille graph for one metric (z)
│       ├── export.go           # Plain-text snapshot report writer
│       ├── styles.go           # Lipgloss style definitions
│       ├── spark.go            # Unicode sparkline chart renderer
│       ├── bar.go              # Horizontal bar chart renderer
│       └── table.go            # Text table & ANSI helpers
├── .github/workflows/ci.yml    # test+race / lint / cross-compile / CLI smoke
├── Makefile                    # Canonical build/packaging tooling (all four targets)
├── DEBIAN/control              # Debian package metadata
├── build-deb.sh                # Legacy Debian package builder (amd64/arm64/riscv64)
├── build-deb-arm64.sh          # ARM64 shortcut
├── build-deb-licheerv.sh       # riscv64 shortcut
├── ROADMAP.md                  # Planned features & known limitations
└── DESIGN.md                   # This file
```

Unit tests live beside each package (`*_test.go`); probes that read sysfs or
procfs take their base path from a package-level var so tests can point them
at fake trees under `t.TempDir()`, and probes that shell out take their binary
path the same way (`iwPaths`) or are intercepted with a stub on `PATH`
(`vcgencmd`). Destructive helpers are exercised against a child process the
test spawns itself, so SIGSTOP/SIGCONT/SIGTERM/SIGKILL and renice are covered
without touching anything else on the machine.

CI (`.github/workflows/ci.yml`) runs four jobs on every push and pull request:

| Job | What it protects |
|-----|------------------|
| **test** | `gofmt` check, `go vet`, `go test -race` with a coverage profile and a summary |
| **lint** | pinned `staticcheck` plus `golangci-lint` (`unused`, `ineffassign`, `unparam`, `unconvert`, `staticcheck`, `govet`) |
| **cross-compile** | `make` for all four targets, asserting each binary is statically linked — the pure-Go/`CGO_ENABLED=0` stance breaks here long before anyone flashes a board |
| **smoke** | builds and runs the CLI: `--version` matches what the Makefile derives, every flag is documented, `--once` emits every report section, the ignore lists filter it, `ping_enabled = false` is honoured, `doctor` and `--check-config` run headless, and unknown flags or unparseable configs fail or degrade cleanly |

The smoke job exists because `main` is the one function unit tests cannot
reach; it covers flag parsing, config loading and the whole headless report.

## Design Decisions

### TUI Framework: Bubble Tea

Chosen for consistency with the existing project family (ducknetview). The Elm architecture (Model → Update → View) provides:

- Clean separation of state management and rendering
- Non-blocking async data collection via Cmd/Msg pattern
- Predictable UI updates on terminal resize, key events, and data ticks

### Data Collection: gopsutil, except for the process walk

`shirou/gopsutil/v4` provides cross-platform access to system metrics. For temperature sensors on Raspberry Pi / uConsole, we also read `/sys/class/thermal/thermal_zone*` directly as a fallback, since gopsutil may not detect all ARM thermal zones.

**The process walk reads `/proc` directly.** gopsutil's accessors each re-read
and re-parse the file they need, and one refresh needed eight of them per PID:
`/proc/<pid>/stat` twice (Ppid + CPUPercent), `/proc/<pid>/status` twice
(Status + Username), a full `/etc/passwd` scan per process (pure-Go
`user.LookupId`), and `/proc/meminfo` parsed once per process
(`MemoryPercent`). Measured over 533 processes that was **~90 ms of work for
~7 ms of actual file reads**; on a CM4 it is several hundred milliseconds
against a 3 s interval, which is exactly what the lateness badge was built to
report.

`ProcSampler` now reads `stat` and `cmdline`, stats the directory for the
owner uid, and reads `io` when permitted — and keeps the previous refresh's
raw counters, so CPU% and per-process block I/O are rates over the interval
(htop-style) rather than lifetime averages. The same measurement takes
**~12 ms**. The uid→name map is cached per sampler; `MemTotal` is read once
per refresh instead of once per process.

Reading `stat` also settles **PID reuse** for free: column 22 is the process
start time, so a recycled PID is detected and gets a fresh first sample
instead of a delta against a different process's counters. `comm` is capped at
15 characters by the kernel, so a name that arrives at the cap is refined from
argv[0].

`GetProcDetail` stays on gopsutil: it runs for a single PID on the proc tick,
where the per-call parsing overhead is irrelevant, and it carries exe, cwd,
fd count and children for free.

**CPU utilisation is sampled per instance too.** `CPUSampler` reads
`/proc/stat` and diffs it against its own previous reading; it does not use
`gopsutil`'s `cpu.Percent(0, …)`, which keeps its baseline in a package
global. One global baseline means two callers in one process measure each
other's intervals, and ucmon has two whenever `metrics_listen` is set: the
exporter builds a model per scrape, so every scrape re-primed the baseline the
monitor was about to diff against and the next frame reported the exporter's
600 ms window — or 0%, when the two landed close enough together. Owning the
state also costs two `/proc/stat` reads per tick instead of the three
`cpu.Percent` (per-core) + `cpu.Percent` (total) + `cpu.Times` (breakdown)
needed before, and gives the aggregate, the per-core figures and the time
breakdown from one pair of readings. Utilisation keeps gopsutil's Linux
definition — the share of the interval that was neither idle nor in iowait —
so the number on screen did not move when the sampler did.

The process-wide temperature thresholds behind `TempColor` are mutex-guarded
for the same reason: `NewModel` applies them, the exporter calls it on its own
goroutine per scrape, and the renderer reads them every frame.

**Delta-based rates** (network, disk I/O) divide counter deltas by the real
elapsed time tracked inside each sampler — never by an assumed interval,
since the disk sampler runs on the slow cadence. Mounted filesystems are
matched to their I/O counters by their *exact* device name first:
`/proc/diskstats` carries a row per partition as well as per disk, so
stripping the partition suffix up front gave every partition of a device the
whole-disk rate and left a root filesystem on LVM or dm-crypt (`/dev/dm-0`)
with no I/O at all. The stripped base survives only as a fallback.

### Temperature thresholds

ucmon's 50/65/80 °C defaults were chosen for a Raspberry Pi, whose firmware
starts throttling at 80 °C. They travel badly: a laptop that declares a 109 °C
critical trip shows red nearly 30 °C before its hardware minds, and a board
that throttles at 70 °C would never show red at all. When the config names no
threshold, `probe.ReadTripPoints` reads `trip_point_*_{type,temp}` from the
same thermal zone directory the sensors come from and `TripThresholds` derives
red from the shutdown trip, orange from where throttling begins, and yellow a
proportional step below. Setting any of `temp_warn`/`temp_hot`/`temp_crit`
marks the config explicit and switches the calibration off;
`temp_from_trip_points = false` does the same.

### Latency monitor

`probe/latency.go` measures RTT to the default IPv4 gateway (parsed from
`/proc/net/route`, re-discovered every 30 s) and one optional external target,
on the proc interval. Mechanism order:

1. **Unprivileged ICMP echo** via a `SOCK_DGRAM/IPPROTO_ICMP` socket
   (`golang.org/x/sys/unix`); the kernel assigns/matches the echo id per
   socket, so any reply read is ours. Allowed by default on Raspberry Pi OS
   (`net.ipv4.ping_group_range` is opened by systemd ≥ 247).
2. **TCP connect timing** to ports 80/443 when ICMP sockets are unavailable —
   a refused connection (RST) is a completed round trip and counts as a
   measurement; only timeouts count as loss. Caveat: a gateway that silently
   DROPs both probes reads as loss.

Every step of a measurement — name resolution included — draws from a single
deadline derived from the sample timeout, so a wedged resolver costs one sample
instead of blocking for its full retry budget while the next tick queues
another round. Resolution goes through `net.DefaultResolver.LookupIP` with that
context; the ICMP and TCP attempts share whatever time is left.

Loss is tracked per target over a rolling 50-sample window; the alert only
evaluates once ≥ 10 samples exist.

### Connections: inet only

`ListConnections` asks gopsutil for `"inet"`, not `"all"`. `"all"` adds
`AF_UNIX`, and a Unix socket carries the same `SOCK_STREAM`/`SOCK_DGRAM` type
as an inet one — so keying the protocol label on the type alone filed
`/run/dbus/system_bus_socket` in the table as **TCP**, with a filesystem path
in the LOCAL column, status `NONE` and frequently pid 0. On a desktop that was
91 of 257 rows. `protoLabel` now checks the family first and labels Unix
sockets honestly (the per-process view still lists them, where they say
something about that one process), and the host table skips them: it is also
~30% faster for not parsing a `/proc/net/unix` with hundreds of lines.

### Rendering

- **Sparkline charts** (from ducknetview's `spark.go`): Unicode block characters `▁▂▃▄▅▆▇█`. `Spark` auto-scales to the data's own range, which is right for unbounded series (bytes/s, MHz, RTT). `SparkPct` draws on a fixed 0–100 axis and is used for every percentage. Auto-scaling a bounded reading is actively misleading: a core idling between 2% and 3% and one pinned between 97% and 98% both span one percentage point, so both drew the identical full-height zigzag, and a core flat at 100% drew the *lowest* row — the same picture as one flat at 0%
- **Expanded graphs** (`internal/ui/zoom.go`): `z` promotes one series to a full-frame braille chart. A braille cell is 2×4 dots, so the same character count carries eight times the resolution of a block sparkline; the plot gets a real value axis, min/max/avg, and `←`/`→` to step through every series the tab offers. The one-row sparklines stay the default — the uConsole's 1280×480 panel is wide but short
- **Bar charts** (inspired by s3duck-tui's `SummaryGraph`): `█` filled / `░` empty blocks for disk usage percentages with color coding (green → yellow → orange → red)
- **Viewport scrolling** (from Bubble Tea's `bubbles/viewport`): for process and connection lists that exceed terminal height
- **Search/filter** (from ducknetview pattern): `/` activates inline search, `ctrl+u` clears, `enter` applies. While a search box is focused it owns every key but `ctrl+c`, so digits, arrows, `tab` and `/` reach the text input instead of the global map
- **Frame budget**: every tab body goes through `Model.box`, which hard-clips the content to the box's inner width *and* to `bodyHeight()` before lipgloss renders it. Clipping only the height is not enough — lipgloss soft-wraps an over-wide line, and each wrap adds a physical row the height budget never counted, so the frame outgrows the terminal and Bubble Tea's alt-screen renderer blanks the display. The header is clamped for the same reason. Below `minWidth`×`minHeight` (30×6) ucmon draws a one-line "terminal too small" notice instead of a frame

## Tab Layout

### Tab 1: CPU / Temperature (High Priority)

- Total CPU usage with bar chart, sparkline history, and session peak
- Per-core CPU usage with individual bars and sparklines
- **cpufreq**: one line per policy — current MHz (with sparkline), min–max
  range, governor, affected cpus — from
  `/sys/devices/system/cpu/cpufreq/policy*`. `g` cycles every policy to the
  next available governor (sysfs write; read-only without root/udev, marked
  `(ro)`)
- **Time breakdown** of the interval (user/sys/nice/iowait/irq/steal) from
  `cpu.Times` deltas. One utilisation number describes a box saturating its
  cores and a box parked in iowait behind a slow card identically; on these
  boards it is usually the card, so a dominant iowait share is called out
- **CPU pressure** (PSI `some` avg10/avg60) when the kernel exposes it
- Temperature sensor readings with color-coded values, bars, sparkline
  history, and the session-peak sensor
- Temperature thresholds are the calibrated `temp_warn`/`temp_hot`/`temp_crit`
  boundaries described above — green below warn, then yellow, orange and red.
  They are seeded from the board's own trip points unless the config names a
  value or sets `temp_from_trip_points = false`, in which case the built-in
  50/65/80 °C applies

CPU tab also shows a one-line load-average summary (1/5/15-min, color-coded against CPU count).

### Tab 2: Memory

- RAM: total / used / free / available / cached / buffers, with a color-coded usage bar, sparkline history, and session peak
- Memory pressure (PSI some + full)
- Swap: total / used / free with bar and sparkline (or "not configured")
- Refreshed every 1 second

### Tab 3: Processes

- Columns: PID, USER, NAME, CPU%, MEM%, RSS, DISK I/O, S (up to `procs_cap`
  rows, scrollable). The I/O column is dropped below 92 columns, where the
  process name is worth more
- **DISK I/O** is the per-process block-I/O rate over the interval, from
  `/proc/<pid>/io`: the answer to "what is thrashing my card". An unreadable
  counter (another user's process without `CAP_SYS_PTRACE`) shows `-`, which
  is a different statement from a confident zero
- CPU% is the usage since the previous refresh (persistent `ProcSampler`)
- Movable cursor (`↑↓` / PgUp/PgDn / Home/End) with the selected row highlighted
- Sort cycle via `s`: CPU → MEM → I/O → PID → NAME (active sort in the header)
- Tree view toggle via `t`: renders a parent/child process tree (built from
  PPID) with `├─`/`└─` branch glyphs; siblings ordered by the active sort.
  Falls back to the flat list while a search filter is active.
- Detail line shows the selected process's full command line
- `enter` opens the **detail overlay**: user, status, start time/uptime, nice,
  threads, open fds, children, RSS/VMS/swap, per-process disk I/O totals,
  exe/cwd, wrapped cmdline, and its sockets. Keys inside: `+`/`-` renice
  (negative needs root), `p`/`c` SIGSTOP/SIGCONT, `k`/`K` kill, `esc` close.
  Refreshes on the proc tick while open.
- `k` (SIGTERM) / `K` (SIGKILL) open a `y/N` confirmation; the signal is sent
  via `probe.KillProcess` (gopsutil `Terminate`/`Kill`) and a status line reports
  the result
- Search/filter matches name, PID, user, and command line; refreshed every 3 seconds

### Tab 4: Disk Usage

- Partition list with device, mount point, filesystem type, total/used/free sizes
- Color-coded usage bar per partition (green → red based on usage percentage)
- Disk I/O rates (read/write bytes per second) per block device, computed over
  the real elapsed time between slow-tick samples
- I/O pressure (PSI some + full) in the title line
- **Device health**: SD/eMMC wear from `/sys/block/*/device` — the eMMC 5.0
  life-time bands (10% granularity, worst of type A/B), the pre-EOL reserve
  state (normal/warning/urgent) and the read-only lockout a controller sets
  when a card is finished. Graded harsher than filesystem usage: a full
  filesystem is an inconvenience, a worn-out card is an unbootable device

### Tab 5: Network

- Active interface summary with address, RX/TX rates and sparkline history.
  `j`/`k` move the selection, which is stored by interface *name*: an interface
  going down shifts the list, and a positional cursor would silently re-point at
  whichever interface took that slot
- Throughput history is keyed by interface name and evicted when a name leaves
  the snapshot, so a host churning container veths or a reconnecting VPN does
  not accumulate a ring buffer per name for the life of the process
- WiFi interfaces show a color-coded link-quality % and signal dBm inline; the
  selected interface expands to a quality bar (parsed from `/proc/net/wireless`,
  no external tooling) plus SSID / channel / tx-rx bitrates when `iw` is
  installed (sampled on the slow tick, graceful absence)
- **Ping block**: one line per target (default gateway + optional
  `ping_target`) with color-coded RTT, sparkline, rolling loss %, and the
  measurement method (icmp/tcp)
- Per-interface **error and drop counters** (totals since boot plus the live
  rate), coloured by whether frames are being lost right now. A link passing
  traffic while dropping frames is invisible in throughput alone
- **Modem block** when `modem_enabled` is set: registration state, operator,
  access technology and signal/RSSI from `mmcli`, with the same graceful
  absence as `vcgencmd` and `iw`
- Connection summary line with total/listen/established/other counts and the
  active state filter (`f` cycles all → LISTEN → ESTABLISHED)
- Connection table: protocol (TCP/UDP with a `6` suffix for IPv6), local/remote
  address, status, PID, process name
- LISTEN connections sorted first
- Search/filter support for connection table (combines with the state filter)

### Tab 6: Power & System Health

- Battery: charge %, charging status, voltage, instantaneous watts, estimated time to empty/full, technology, **pack health** (energy/charge `full` vs `full_design`) — read directly from `/sys/class/power_supply` (works with the uConsole axp20x driver and standard laptop batteries); AC/USB mains presence; session-minimum charge; signed power-draw sparkline (negative = discharging)
- Load average: 1/5/15-minute, color-coded against CPU count, with sparkline history and session peak
- Raspberry Pi throttle / under-voltage: parses `vcgencmd get_throttled`, showing both current and since-boot flags for under-voltage, ARM frequency capping, throttling, and soft temperature limit. Gracefully reports unavailable when `vcgencmd` is absent (non-Pi hosts)
- Refreshed on the slow interval

### Tab 7: GPU / VideoCore & Display

- Display **backlight**: device, current %, raw brightness/max, and a bar, read
  from `/sys/class/backlight`. `+`/`-` adjust brightness by `backlight_step`
  percent. When the brightness file isn't writable (no root / udev rule) the
  value is shown read-only with a hint instead of erroring
- **VideoCore** (Raspberry Pi / uConsole, via `vcgencmd`): SoC core temperature
  (color-coded against the same thresholds as CPU temps), core voltage, the
  CPU↔GPU memory split, and per-domain clocks (arm/core/v3d/h264/isp/hevc).
  Reports unavailable on non-Pi hosts
- Refreshed on the slow interval

### Tab 8: Alerts

- An **edge-triggered alert engine** (`internal/ui/alerts.go`) evaluates the
  latest probe snapshots against configurable thresholds once per fast tick.
  Conditions are keyed by a stable identity (e.g. `temp:cpu_thermal`,
  `disk:/`) so a sustained problem logs once on raise and once on clear rather
  than every tick; a rise in severity (warn → crit) is logged as a new event,
  a de-escalation is tracked silently.
- **Flap damping**: a condition must stay absent for `alert_clear_ticks`
  consecutive updates (default 3) before it clears, so a value oscillating
  around its threshold cannot spam raise/clear pairs — it simply stays active
  until it settles.
- Monitored conditions: CPU/SoC temperature (reusing `temp_hot`/`temp_crit`),
  RAM and swap usage, per-filesystem usage (squashfs skipped to avoid loop-mount
  noise), discharging battery level (low/critical), live Pi throttle and
  under-voltage flags (current bits only — the latched "occurred" bits would
  pin the banner on forever), load average vs core count, weak WiFi signal
  (`wifi_low_dbm`), packet loss to a ping target (`ping_loss_warn_pct`, once
  the loss window has ≥ 10 samples), and PSI pressure (`psi_warn_pct` against
  each resource's some-avg10).
- **Predictive conditions** (`internal/ui/trend.go`): a level threshold cannot
  tell a pack discharging over eight hours from one dumping in twenty minutes,
  and by the time a 16 GB card reads 90% full there may be minutes left. The
  trend tracker keeps a two-minute window per reading and answers "how fast is
  this moving, and when does it hit the wall" — battery drain %/h with a
  time-to-empty, filesystem fill with a time-to-full, temperature climb with a
  time-to-critical. A rate needs a 45 s baseline before it is trusted, so a
  one-tick wobble cannot set a slope.
- **Storage wear**: a card at 90% of its rated write endurance looks perfect in
  every throughput and usage reading right up to the day it goes read-only,
  which makes it the one condition nothing else in ucmon could hint at.
- **Pause does not stop alerting.** `space` freezes the readings, which is what
  it is for; silencing the bell, the hook and the log at the same time would
  disarm the monitor at exactly the moment someone stopped it to look at
  something worrying.
- **Delivery**: `alertEngine.update` returns the events recorded per tick;
  the model then runs the configured side effects — a terminal bell on a new
  critical (`alert_bell`, suppressed while muted), an `alert_cmd` shell hook
  (run async with `UCMON_ALERT_STATE`/`SEV`/`MSG` in the environment, 10 s
  timeout), and an append-only on-disk log (`alert_log_file`,
  `2026-07-25 12:30:00 RAISE CRIT …` lines).
- A severity-colored **banner** shows the most severe active alert in the
  footer of *every* tab (errors still take precedence; transient notices show
  briefly above it). The Alerts tab itself lists active conditions and a
  scrollable, newest-first timestamped log. `m` mutes/unmutes the banner, `c`
  clears the log. The tab label carries a live `(N)` active-count badge.

### Snapshot export (`w`, any tab) and `--once`

`internal/ui/export.go` writes a plain-text dump of all current readings — CPU
and temps, cpufreq, PSI, memory, power/throttle (incl. battery health), disks,
network interfaces (incl. SSID), latency/loss, top-10 processes by CPU, session
peaks, and the active/recent alerts — to `~/ucmon-report-<timestamp>.txt`. A
transient footer notice reports the path written. The report is ANSI-free and
reuses the same `probe` formatting helpers as the live views.

`--once --json` (`internal/ui/metrics.go`) emits the same readings as a JSON
document instead, because parsing the text report in a script means
re-deriving its layout every time the layout changes.

`ucmon --metrics :9100` (`internal/ui/exporter.go`) serves `/metrics` in the
Prometheus text exposition format, `/snapshot` as JSON and `/healthz`, with no
TUI; `metrics_listen` runs the same server alongside the monitor. Sampling is
lazy — nothing is collected until something scrapes — and a result is reused
for 5 seconds, so a careless 1 s scrape loop cannot drive a full `/proc` walk
as fast as it can ask.

Each scrape collects through a model of its own, so the exporter never reaches
into the monitor's state and a scrape cannot perturb what is on screen. The
samplers that keep a baseline are per-instance for exactly that reason. The
consequence is that the session-scoped readings — `session_peaks` in
`/snapshot`, and the ping loss window behind `ucmon_ping_loss_percent` —
describe that one collection rather than the exporter's uptime; Prometheus is
the right place to compute a window over them.

`ucmon --once` (`internal/ui/once.go`) produces the same report on stdout
without starting the TUI: it primes the delta-based samplers (CPU%, net/disk
rates), waits ~600 ms, re-samples everything, runs one alert evaluation, and
prints. Useful over SSH and from cron.

### `ucmon doctor` — the capability report

ucmon degrades gracefully in a dozen places, and every one of them looks the
same from the outside: a row that is absent, or marked `(ro)`. `ucmon doctor`
(also `--doctor`) is the one command that says which readings this board can
produce and why the rest are missing — the question "why is the GPU tab empty"
answered without reading this document.

The knowledge lives where the sampling does. Each probe file owns a
`Check…()` returning a `probe.Capability` (`internal/probe/capability.go`):
the source consulted, a status — **present**, **absent**, **denied** or
**read-only** — what was found or why nothing was, which rows go missing
without it, and the remedy. `probe.Capabilities()` runs the set:

| Capability | Source | Typical absence |
|---|---|---|
| temperature sensors | `/sys/class/thermal`, hwmon | a VM with no sensors |
| cpufreq policies | `/sys/devices/system/cpu/cpufreq` | a VM or container |
| cpufreq governor write | `policy*/scaling_governor` | **read-only** without root or a udev rule |
| pressure stall (PSI) | `/proc/pressure` | kernel without `CONFIG_PSI`, or `psi=0` |
| per-process disk I/O | `/proc/<pid>/io` | **denied** for other users without `CAP_SYS_PTRACE` |
| battery / design capacity | `/sys/class/power_supply` | bench-supplied board; gauges with no `*_full_design` |
| GPU / VideoCore | `vcgencmd` | any board that is not a Raspberry Pi |
| backlight brightness | `/sys/class/backlight` | headless host; **read-only** without a udev rule |
| wireless link details | `iw` | package not installed, or no wireless hardware |
| WWAN modem | `mmcli` | no ModemManager, or no 4G board |
| unprivileged ICMP | `net.ipv4.ping_group_range` | **denied** — latency falls back to TCP connect timing |
| storage wear | `/sys/block/*/device/life_time` | a plain SD card, USB, SATA or NVMe |

Splitting it this way is the point: a check that reads the same files as the
sampler, in the same package, cannot drift from the behaviour it explains.
`internal/ui/doctor.go` only formats — status label, source, finding, what it
hides, how to fix it — and for the two writable sources it prints the exact
udev rule, exported as `probe.GovernorUdevRule` and `probe.BacklightUdevRule`
so the test asserts the same text the user pastes. The report is ANSI-free,
needs no privileges, writes nothing, and **always exits 0**: an absent source
is a fact about the board, not a failure.

The report closes with the config settings that suppress a reading the host
could otherwise produce (`ping_enabled`, `modem_enabled`, `alerts_enabled`,
the ignore globs) and any unrecognised keys — the half of "why is that empty"
that no capability check can see.

### Unrecognised config keys

`apply` ignored anything it did not know, so `temp_warnn = 90` did nothing and
said nothing. `LoadFrom` now collects what `apply` refused — key plus line
number, deduplicated, and only for lines that actually carry a key, since
`[section]` headers, comments and malformed lines are skipped before `apply`
ever sees them. They surface in three places: `ucmon --check-config` lists
them and exits non-zero (a provisioning script can gate on it), `ucmon doctor`
names them in its config section, and the TUI shows a one-line footer notice
at startup.

Unknown keys stay **non-fatal** everywhere else, deliberately: a config
written for a newer ucmon has to keep working on an older binary, and
`--config junk.toml --once` must still produce a report — the CI smoke job
asserts exactly that.

### Overlays, pause, session peaks

- `z` opens an **expanded graph** of one metric: a full-frame braille chart
  with a value axis, min/max/avg and `←`/`→` to step through every series the
  tab offers. `Z` resets the session peaks and the trend history together.
- `history_file` persists the sparkline ring buffers and session peaks across
  restarts (`internal/ui/history.go`), written atomically and ignored when
  stale, foreign-format or unparseable — replaying yesterday's shape next to
  this minute's reading would be a lie told convincingly.
- `?` opens a **help overlay** listing every key binding (two-column layout on
  wide-but-short screens like the uConsole); any of `?`/`esc`/`q` closes it.
- `space` **pauses** sampling: the tick loop keeps running but issues no fetch
  commands, freezing every reading (the header shows `⏸ PAUSED`). In-flight
  results still land.
- `internal/ui/peaks.go` tracks session peaks (max CPU%, hottest sensor, max
  load1, max RAM%, min battery%) fed from the same messages that update the
  histories; they render inline on the relevant tabs and in reports.
- The header also carries a **sampling-lateness badge** (`⏳ slow: gpu/soc`)
  naming the probes that could not finish inside their interval, and fades it
  once they catch up. Naming the probe is the point: on a CM4 the difference
  between "something is slow" and "the vcgencmd shell-outs are slow" is the
  whole diagnosis.
- Transient status lines (`internal/ui/notice.go`) expire on their own after a
  few seconds rather than sitting there until an unrelated keypress overwrites
  them; the footer notice, the kill confirmation and the brightness readout all
  use the same type.

## Refresh Intervals

Three configurable cadences drive all sampling (defaults shown). The base
"fast" tick schedules the others by integer multiples.

| Data                            | Setting         | Default |
|---------------------------------|-----------------|---------|
| CPU / cpufreq / PSI / memory / network I/O | `fast_interval` | 1s |
| Processes / connections / ping  | `proc_interval` | 3s      |
| Disk / battery / GPU / SoC / iw | `slow_interval` | 5s      |

The four slow rounds — the `/proc` walk, the socket walk, the vcgencmd + `iw`
shell-outs, and the ping round — carry an in-flight guard. The socket walk
needs one most of all: `gnet.Connections` readlinks every `/proc/<pid>/fd`
entry on the host, which is strictly more work than the process walk beside
it, and it used to be dispatched bare. A refresh a *keypress* asked for goes
through `queueUser`, which shares the guard but does not count a collision as
lateness — holding down the brightness key used to light the badge. A tick that finds the previous round
still running skips this one rather than queueing a second goroutine onto the
sampler's mutex, and records the skip; the header then shows which probe is
lagging (`⏳ slow: gpu/soc`). A fetch that is merely disabled is not lateness.
`Init` primes every reading once without raising the guards, since Bubble Tea's
`Init` returns a command rather than a model; the first guarded dispatch is a
whole interval away, so the priming fetches have landed by then.

## Configuration

ucmon loads `$XDG_CONFIG_HOME/ucmon/config.toml` (`~/.config/ucmon/config.toml`)
if present (`--config` overrides the path); otherwise built-in defaults apply.
The loader (`internal/config`) parses the flat `key = value` TOML subset by
hand — comments (`#`/`;`) and `[section]` headers are skipped, values may be
quoted, and durations accept a Go duration string or a bare number of seconds.
No third-party TOML dependency is added, preserving the pure-Go cross-compile.
Keys: `start_tab`, `fast_interval`, `proc_interval`, `slow_interval`,
`temp_warn`/`temp_hot`/`temp_crit`, `temp_from_trip_points`,
`backlight_step`, `backlight_device`, the alert thresholds `alerts_enabled`,
`mem_warn_pct`, `swap_warn_pct`, `disk_warn_pct`, `batt_low_pct`,
`batt_crit_pct`, `load_warn_factor`, `alert_log_cap`, the alert
delivery/damping keys `alert_clear_ticks`, `alert_bell`, `alert_cmd`,
`alert_log_file` (supports `~/`), the monitor keys `wifi_low_dbm`,
`ping_enabled`, `ping_target`, `ping_loss_warn_pct`, `psi_warn_pct`,
`modem_enabled`, the rate-based alert keys `batt_drain_warn_pct_h`,
`disk_full_warn_hours`, `temp_rise_warn_c_per_min`, the output and limit keys
`history_len`, `procs_cap`, `report_dir` (supports `~/`), `history_file`
(supports `~/`), `metrics_listen`, and the ignore lists `net_ignore`,
`disk_ignore`, `temp_ignore`. See the README for an annotated example.

Keys the parser does not recognise are collected rather than dropped (see
"Unrecognised config keys" above); they never prevent startup.

`splitValue` drops a `#` comment that is not inside quotes, then removes one
matched pair of surrounding quotes — so a value keeps quotes of its own and a
shell hook can be written as a TOML literal string. Escape sequences are not
interpreted. `Sanitize` then clamps the intervals, restores out-of-range alert
percentages to their defaults (an out-of-range threshold would otherwise fire
forever or never), and enforces `temp_warn ≤ temp_hot ≤ temp_crit` by pushing
the lower tiers down, so explicitly lowering `temp_crit` keeps the value asked
for. `internal/config/readme_test.go` parses the README's own example block and
asserts every documented key is recognised and every value loses its comment
and quoting, pinning the docs to the parser.

The ignore lists take comma-separated globs where `*` spans `/` as well, so
`/snap/*` covers a whole mount subtree — deliberately unlike `path.Match`.
Filtering is applied in `internal/ui/filter.go` where each snapshot lands, not
in the views, so sparkline history, session peaks, the alert engine and the
`--once` report all see the same filtered set; a hidden interface cannot still
raise a WiFi alert or hold a history buffer. The default lists are empty.

## Keyboard Controls

| Key              | Action                    |
|------------------|---------------------------|
| `tab` / `→`      | Next tab                  |
| `shift+tab` / `←`| Previous tab              |
| `1` - `8`        | Jump to tab               |
| `?`              | Help overlay              |
| `space`          | Pause sampling (alerts keep running) |
| `z` / `Z`        | Expand a metric / reset peaks |
| `g`              | Cycle cpufreq governor (tab 1) |
| `+` / `-`        | Adjust backlight (tab 7)  |
| `w`              | Write snapshot report     |
| `m` / `c`        | Mute banner / clear log (tab 8) |
| `/`              | Activate search (tabs 3,5)|
| `enter`          | Apply search / process details (tab 3) |
| `esc`            | Cancel search / kill / close overlay |
| `ctrl+u`         | Clear search filter       |
| `↑` `↓` PgUp/Dn `home` `end` | Move cursor (procs)/scroll|
| `s`              | Cycle process sort        |
| `t`              | Toggle process tree view  |
| `k` / `K`        | SIGTERM / SIGKILL (procs) |
| `j` / `k`        | Select interface (net)    |
| `f`              | Connection state filter (net) |
| `ctrl+c`         | Quit                      |

Process detail overlay: `+`/`-` renice, `p`/`c` SIGSTOP/SIGCONT, `k`/`K` kill,
`esc`/`q`/`enter` close.

## Build & Packaging

Binary build:
```bash
go build -o ucmon cmd/ucmon/main.go
```

Debian package — the `Makefile` is the canonical tooling (it derives the `.deb`
version from `const version` in source, so binary and package stay in lockstep):
```bash
make deb                # .deb for every platform → dist/
make deb-uconsole       # arm64
make deb-pizero2w       # armhf
make deb-licheerv       # riscv64
```

Legacy single-arch wrappers remain for parity (`./build-deb.sh [amd64|arm64|riscv64]`,
plus `build-deb-arm64.sh` / `build-deb-licheerv.sh` shortcuts); they now derive the
version from source as well. Every build is pure Go (`CGO_ENABLED=0`), so all
architectures cross-compile from one machine. Each `.deb` installs the binary to
`/usr/bin/ucmon`.

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3/4/5 (ARM64); Pi Zero 2 W (armv7 on 32-bit Pi OS)
- Sipeed LicheeRV Nano (riscv64, SG2002 — Debian-based image)
- Generic Linux x86_64

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - UI components (viewport, textinput)
- `github.com/charmbracelet/lipgloss` - Terminal styling
- `github.com/shirou/gopsutil/v4` - System metrics collection
- `golang.org/x/sys/unix` - Unprivileged ICMP datagram sockets, uid lookups
- `github.com/tklauser/go-sysconf` - USER_HZ for the `/proc/<pid>/stat` columns

Optional external tools, degraded gracefully when absent: `vcgencmd`
(VideoCore/throttle), `iw` (SSID/channel/bitrates), `mmcli` (WWAN modem).
All three are pure shell-outs on the slow tick; the HTTP exporter uses only
the standard library, so the pure-Go cross-compile stance is unchanged.
