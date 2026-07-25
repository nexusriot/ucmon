# ucmon - uConsole/Raspberry Pi TUI System Monitor

## Overview

ucmon is a terminal-based system monitoring tool designed for the ClockworkPi uConsole and Raspberry Pi devices. It provides real-time monitoring of CPU, processes, disk, and network in a compact TUI interface optimized for small screens.

Built with Go using the Charmbracelet Bubble Tea TUI framework (Elm architecture).

## Architecture

```
ucmon/
├── cmd/ucmon/main.go           # Entry point + CLI flags (--once/--config/--tab/--version)
├── internal/
│   ├── probe/                  # Data collection layer
│   │   ├── cpu.go              # CPU load & temperature sensors
│   │   ├── cpufreq.go          # cpufreq policies: clocks + governor read/set (sysfs)
│   │   ├── mem.go              # RAM & swap
│   │   ├── procs.go            # ProcSampler (interval CPU%), detail, kill/renice/stop
│   │   ├── disk.go             # Disk usage & I/O counters
│   │   ├── net.go              # Network interfaces & connections (TCP6/UDP6 aware)
│   │   ├── wifi.go             # WiFi quality (/proc/net/wireless) + SSID/bitrate (iw)
│   │   ├── latency.go          # Gateway/target RTT: unprivileged ICMP + TCP fallback
│   │   ├── psi.go              # Pressure stall information (/proc/pressure)
│   │   ├── power.go            # Battery (incl. health), RPi throttle, load average
│   │   ├── gpu.go              # VideoCore clocks/temp/volts/mem (vcgencmd)
│   │   ├── backlight.go        # Display backlight read/set (/sys/class/backlight)
│   │   └── util.go             # Formatting helpers
│   ├── config/                 # Optional TOML-subset config loader
│   │   └── config.go
│   └── ui/                     # Presentation layer
│       ├── model.go            # Bubble Tea model (state, Update, View)
│       ├── alerts.go           # Alert engine (flap damping) + delivery (bell/cmd/file)
│       ├── detail.go           # Process detail overlay (renice, STOP/CONT, kill)
│       ├── help.go             # Key-binding help overlay (?)
│       ├── peaks.go            # Session peak tracker
│       ├── once.go             # RunOnce: headless one-shot snapshot for --once
│       ├── export.go           # Plain-text snapshot report writer
│       ├── styles.go           # Lipgloss style definitions
│       ├── spark.go            # Unicode sparkline chart renderer
│       ├── bar.go              # Horizontal bar chart renderer
│       └── table.go            # Text table & ANSI helpers
├── DEBIAN/control              # Debian package metadata
├── build-deb.sh                # Debian package builder (amd64/arm64)
├── build-deb-arm64.sh          # ARM64 shortcut
├── ROADMAP.md                  # Planned features & known limitations
└── DESIGN.md                   # This file
```

Unit tests live beside each package (`*_test.go`); probes that read sysfs or
procfs take their base path from a package-level var so tests can point them
at fake trees under `t.TempDir()`.

## Design Decisions

### TUI Framework: Bubble Tea

Chosen for consistency with the existing project family (ducknetview). The Elm architecture (Model → Update → View) provides:

- Clean separation of state management and rendering
- Non-blocking async data collection via Cmd/Msg pattern
- Predictable UI updates on terminal resize, key events, and data ticks

### Data Collection: gopsutil

`shirou/gopsutil/v4` provides cross-platform access to system metrics. For temperature sensors on Raspberry Pi / uConsole, we also read `/sys/class/thermal/thermal_zone*` directly as a fallback, since gopsutil may not detect all ARM thermal zones.

**Process CPU% semantics**: `ProcSampler` keeps one gopsutil `Process` handle
per PID across refreshes. gopsutil computes `CPUPercent()` as the delta since
the previous call on the same handle, so reuse yields usage *since the last
refresh* (htop-style). Recreating handles every refresh — the old behaviour —
silently degrades to an average over the whole process lifetime. The first
sample for a new PID still reports the lifetime average. Known trade-offs:
a recycled PID or an exec() that changes the process name can serve one stale
sample/name from the cached handle.

**Delta-based rates** (network, disk I/O) divide counter deltas by the real
elapsed time tracked inside each sampler — never by an assumed interval,
since the disk sampler runs on the slow cadence.

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

Loss is tracked per target over a rolling 50-sample window; the alert only
evaluates once ≥ 10 samples exist.

### Rendering

- **Sparkline charts** (from ducknetview's `spark.go`): Unicode block characters `▁▂▃▄▅▆▇█` for time-series visualization of CPU load, temperatures, and network throughput
- **Bar charts** (inspired by s3duck-tui's `SummaryGraph`): `█` filled / `░` empty blocks for disk usage percentages with color coding (green → yellow → orange → red)
- **Viewport scrolling** (from Bubble Tea's `bubbles/viewport`): for process and connection lists that exceed terminal height
- **Search/filter** (from ducknetview pattern): `/` activates inline search, `ctrl+u` clears, `enter` applies

## Tab Layout

### Tab 1: CPU / Temperature (High Priority)

- Total CPU usage with bar chart, sparkline history, and session peak
- Per-core CPU usage with individual bars and sparklines
- **cpufreq**: one line per policy — current MHz (with sparkline), min–max
  range, governor, affected cpus — from
  `/sys/devices/system/cpu/cpufreq/policy*`. `g` cycles every policy to the
  next available governor (sysfs write; read-only without root/udev, marked
  `(ro)`)
- **CPU pressure** (PSI `some` avg10/avg60) when the kernel exposes it
- Temperature sensor readings with color-coded values, bars, sparkline
  history, and the session-peak sensor
- Temperature thresholds: green (<50°C), yellow (50-65°C), orange (65-80°C), red (>80°C)

CPU tab also shows a one-line load-average summary (1/5/15-min, color-coded against CPU count).

### Tab 2: Memory

- RAM: total / used / free / available / cached / buffers, with a color-coded usage bar, sparkline history, and session peak
- Memory pressure (PSI some + full)
- Swap: total / used / free with bar and sparkline (or "not configured")
- Refreshed every 1 second

### Tab 3: Processes

- Columns: PID, USER, NAME, CPU%, MEM%, RSS, STATUS (up to 200 rows, scrollable)
- CPU% is the usage since the previous refresh (persistent `ProcSampler`)
- Movable cursor (`↑↓` / PgUp/PgDn / Home/End) with the selected row highlighted
- Sort cycle via `s`: CPU → MEM → PID → NAME (active sort shown in the header)
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

### Tab 5: Network

- Active interface summary with address, RX/TX rates and sparkline history
- WiFi interfaces show a color-coded link-quality % and signal dBm inline; the
  selected interface expands to a quality bar (parsed from `/proc/net/wireless`,
  no external tooling) plus SSID / channel / tx-rx bitrates when `iw` is
  installed (sampled on the slow tick, graceful absence)
- **Ping block**: one line per target (default gateway + optional
  `ping_target`) with color-coded RTT, sparkline, rolling loss %, and the
  measurement method (icmp/tcp)
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

`ucmon --once` (`internal/ui/once.go`) produces the same report on stdout
without starting the TUI: it primes the delta-based samplers (CPU%, net/disk
rates), waits ~600 ms, re-samples everything, runs one alert evaluation, and
prints. Useful over SSH and from cron.

### Overlays, pause, session peaks

- `?` opens a **help overlay** listing every key binding (two-column layout on
  wide-but-short screens like the uConsole); any of `?`/`esc`/`q` closes it.
- `space` **pauses** sampling: the tick loop keeps running but issues no fetch
  commands, freezing every reading (the header shows `⏸ PAUSED`). In-flight
  results still land.
- `internal/ui/peaks.go` tracks session peaks (max CPU%, hottest sensor, max
  load1, max RAM%, min battery%) fed from the same messages that update the
  histories; they render inline on the relevant tabs and in reports.

## Refresh Intervals

Three configurable cadences drive all sampling (defaults shown). The base
"fast" tick schedules the others by integer multiples.

| Data                            | Setting         | Default |
|---------------------------------|-----------------|---------|
| CPU / cpufreq / PSI / memory / network I/O | `fast_interval` | 1s |
| Processes / connections / ping  | `proc_interval` | 3s      |
| Disk / battery / GPU / SoC / iw | `slow_interval` | 5s      |

## Configuration

ucmon loads `$XDG_CONFIG_HOME/ucmon/config.toml` (`~/.config/ucmon/config.toml`)
if present (`--config` overrides the path); otherwise built-in defaults apply.
The loader (`internal/config`) parses the flat `key = value` TOML subset by
hand — comments (`#`/`;`) and `[section]` headers are skipped, values may be
quoted, and durations accept a Go duration string or a bare number of seconds.
No third-party TOML dependency is added, preserving the pure-Go cross-compile.
Keys: `start_tab`, `fast_interval`, `proc_interval`, `slow_interval`,
`temp_warn`/`temp_hot`/`temp_crit`, `backlight_step`, `backlight_device`, the
alert thresholds `alerts_enabled`, `mem_warn_pct`, `swap_warn_pct`,
`disk_warn_pct`, `batt_low_pct`, `batt_crit_pct`, `load_warn_factor`,
`alert_log_cap`, the alert delivery/damping keys `alert_clear_ticks`,
`alert_bell`, `alert_cmd`, `alert_log_file` (supports `~/`), and the monitor
keys `wifi_low_dbm`, `ping_enabled`, `ping_target`, `ping_loss_warn_pct`,
`psi_warn_pct`. See the README for an annotated example.

## Keyboard Controls

| Key              | Action                    |
|------------------|---------------------------|
| `tab` / `→`      | Next tab                  |
| `shift+tab` / `←`| Previous tab              |
| `1` - `8`        | Jump to tab               |
| `?`              | Help overlay              |
| `space`          | Pause / resume sampling   |
| `g`              | Cycle cpufreq governor (tab 1) |
| `+` / `-`        | Adjust backlight (tab 7)  |
| `w`              | Write snapshot report     |
| `m` / `c`        | Mute banner / clear log (tab 8) |
| `/`              | Activate search (tabs 3,5)|
| `enter`          | Apply search / process details (tab 3) |
| `esc`            | Cancel search / kill / close overlay |
| `ctrl+u`         | Clear search filter       |
| `↑` `↓` PgUp/Dn | Move cursor (procs)/scroll|
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
- `golang.org/x/sys/unix` - Unprivileged ICMP datagram sockets (latency monitor)

Optional external tools, degraded gracefully when absent: `vcgencmd`
(VideoCore/throttle), `iw` (SSID/channel/bitrates).
