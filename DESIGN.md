# ucmon - uConsole/Raspberry Pi TUI System Monitor

## Overview

ucmon is a terminal-based system monitoring tool designed for the ClockworkPi uConsole and Raspberry Pi devices. It provides real-time monitoring of CPU, processes, disk, and network in a compact TUI interface optimized for small screens.

Built with Go using the Charmbracelet Bubble Tea TUI framework (Elm architecture).

## Architecture

```
ucmon/
├── cmd/ucmon/main.go           # Entry point
├── internal/
│   ├── probe/                  # Data collection layer
│   │   ├── cpu.go              # CPU load & temperature sensors
│   │   ├── mem.go              # RAM & swap
│   │   ├── procs.go            # Process listing
│   │   ├── disk.go             # Disk usage & I/O counters
│   │   ├── net.go              # Network interfaces & connections
│   │   ├── wifi.go             # WiFi link quality/signal (/proc/net/wireless)
│   │   ├── power.go            # Battery, RPi throttle, load average
│   │   ├── gpu.go              # VideoCore clocks/temp/volts/mem (vcgencmd)
│   │   ├── backlight.go        # Display backlight read/set (/sys/class/backlight)
│   │   └── util.go             # Formatting helpers
│   ├── config/                 # Optional TOML-subset config loader
│   │   └── config.go
│   └── ui/                     # Presentation layer
│       ├── model.go            # Bubble Tea model (state, Update, View)
│       ├── alerts.go           # Threshold alert engine + condition evaluation
│       ├── export.go           # Plain-text snapshot report writer
│       ├── styles.go           # Lipgloss style definitions
│       ├── spark.go            # Unicode sparkline chart renderer
│       ├── bar.go              # Horizontal bar chart renderer
│       └── table.go            # Text table & ANSI helpers
├── DEBIAN/control              # Debian package metadata
├── build-deb.sh                # Debian package builder (amd64/arm64)
├── build-deb-arm64.sh          # ARM64 shortcut
└── DESIGN.md                   # This file
```

## Design Decisions

### TUI Framework: Bubble Tea

Chosen for consistency with the existing project family (ducknetview). The Elm architecture (Model → Update → View) provides:

- Clean separation of state management and rendering
- Non-blocking async data collection via Cmd/Msg pattern
- Predictable UI updates on terminal resize, key events, and data ticks

### Data Collection: gopsutil

`shirou/gopsutil/v4` provides cross-platform access to system metrics. For temperature sensors on Raspberry Pi / uConsole, we also read `/sys/class/thermal/thermal_zone*` directly as a fallback, since gopsutil may not detect all ARM thermal zones.

### Rendering

- **Sparkline charts** (from ducknetview's `spark.go`): Unicode block characters `▁▂▃▄▅▆▇█` for time-series visualization of CPU load, temperatures, and network throughput
- **Bar charts** (inspired by s3duck-tui's `SummaryGraph`): `█` filled / `░` empty blocks for disk usage percentages with color coding (green → yellow → orange → red)
- **Viewport scrolling** (from Bubble Tea's `bubbles/viewport`): for process and connection lists that exceed terminal height
- **Search/filter** (from ducknetview pattern): `/` activates inline search, `ctrl+u` clears, `enter` applies

## Tab Layout

### Tab 1: CPU / Temperature (High Priority)

- Total CPU usage with bar chart and sparkline history
- Per-core CPU usage with individual bars and sparklines
- Temperature sensor readings with color-coded values, bars, and sparkline history
- Temperature thresholds: green (<50°C), yellow (50-65°C), orange (65-80°C), red (>80°C)

CPU tab also shows a one-line load-average summary (1/5/15-min, color-coded against CPU count).

### Tab 2: Memory

- RAM: total / used / free / available / cached / buffers, with a color-coded usage bar and sparkline history
- Swap: total / used / free with bar and sparkline (or "not configured")
- Refreshed every 1 second

### Tab 3: Processes

- Columns: PID, USER, NAME, CPU%, MEM%, RSS, STATUS (up to 200 rows, scrollable)
- Movable cursor (`↑↓` / PgUp/PgDn / Home/End) with the selected row highlighted
- Sort cycle via `s`: CPU → MEM → PID → NAME (active sort shown in the header)
- Tree view toggle via `t`: renders a parent/child process tree (built from
  PPID) with `├─`/`└─` branch glyphs; siblings ordered by the active sort.
  Falls back to the flat list while a search filter is active.
- Detail line shows the selected process's full command line
- `k` (SIGTERM) / `K` (SIGKILL) open a `y/N` confirmation; the signal is sent
  via `probe.KillProcess` (gopsutil `Terminate`/`Kill`) and a status line reports
  the result
- Search/filter support; refreshed every 3 seconds

### Tab 4: Disk Usage

- Partition list with device, mount point, filesystem type, total/used/free sizes
- Color-coded usage bar per partition (green → red based on usage percentage)
- Disk I/O rates (read/write bytes per second) per block device

### Tab 5: Network

- Active interface summary with MAC, address, RX/TX rates and sparkline history
- WiFi interfaces show a color-coded link-quality % and signal dBm inline; the
  selected interface expands to a quality bar (parsed from `/proc/net/wireless`,
  no external tooling)
- Connection table: protocol, local/remote address, status, PID, process name
- LISTEN connections sorted first
- Search/filter support for connection table

### Tab 6: Power & System Health

- Battery: charge %, charging status, voltage, instantaneous watts, estimated time to empty/full, technology — read directly from `/sys/class/power_supply` (works with the uConsole axp20x driver and standard laptop batteries); AC/USB mains presence
- Load average: 1/5/15-minute, color-coded against CPU count, with sparkline history
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
- Monitored conditions: CPU/SoC temperature (reusing `temp_hot`/`temp_crit`),
  RAM and swap usage, per-filesystem usage (squashfs skipped to avoid loop-mount
  noise), discharging battery level (low/critical), live Pi throttle and
  under-voltage flags (current bits only — the latched "occurred" bits would
  pin the banner on forever), and load average vs core count.
- A severity-colored **banner** shows the most severe active alert in the
  footer of *every* tab (errors still take precedence; transient notices show
  briefly above it). The Alerts tab itself lists active conditions and a
  scrollable, newest-first timestamped log. `m` mutes/unmutes the banner, `c`
  clears the log. The tab label carries a live `(N)` active-count badge.

### Snapshot export (`w`, any tab)

`internal/ui/export.go` writes a plain-text dump of all current readings — CPU
and temps, memory, power/throttle, disks, network interfaces, top-10 processes
by CPU, and the active/recent alerts — to `~/ucmon-report-<timestamp>.txt`. A
transient footer notice reports the path written. The report is ANSI-free and
reuses the same `probe` formatting helpers as the live views.

## Refresh Intervals

Three configurable cadences drive all sampling (defaults shown). The base
"fast" tick schedules the others by integer multiples.

| Data                         | Setting         | Default |
|------------------------------|-----------------|---------|
| CPU / memory / network I/O   | `fast_interval` | 1s      |
| Processes / connections      | `proc_interval` | 3s      |
| Disk / battery / GPU / SoC    | `slow_interval` | 5s      |

## Configuration

ucmon loads `$XDG_CONFIG_HOME/ucmon/config.toml` (`~/.config/ucmon/config.toml`)
if present; otherwise built-in defaults apply. The loader (`internal/config`)
parses the flat `key = value` TOML subset by hand — comments (`#`/`;`) and
`[section]` headers are skipped, values may be quoted, and durations accept a Go
duration string or a bare number of seconds. No third-party TOML dependency is
added, preserving the pure-Go cross-compile. Keys: `start_tab`, `fast_interval`,
`proc_interval`, `slow_interval`, `temp_warn`/`temp_hot`/`temp_crit`,
`backlight_step`, `backlight_device`, and the alert thresholds
`alerts_enabled`, `mem_warn_pct`, `swap_warn_pct`, `disk_warn_pct`,
`batt_low_pct`, `batt_crit_pct`, `load_warn_factor`, `alert_log_cap`. See the
README for an annotated example.

## Keyboard Controls

| Key              | Action                    |
|------------------|---------------------------|
| `tab` / `→`      | Next tab                  |
| `shift+tab` / `←`| Previous tab              |
| `1` - `8`        | Jump to tab               |
| `+` / `-`        | Adjust backlight (tab 7)  |
| `w`              | Write snapshot report     |
| `m` / `c`        | Mute banner / clear log (tab 8) |
| `/`              | Activate search (tabs 3,5)|
| `enter`          | Apply search filter       |
| `esc`            | Cancel search / kill      |
| `ctrl+u`         | Clear search filter       |
| `↑` `↓` PgUp/Dn | Move cursor (procs)/scroll|
| `s`              | Cycle process sort        |
| `t`              | Toggle process tree view  |
| `k` / `K`        | SIGTERM / SIGKILL (procs) |
| `j` / `k`        | Select interface (net)    |
| `ctrl+c`         | Quit                      |

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
- `github.com/charmbracelet/bubbles` - UI components (viewport, textinput, list)
- `github.com/charmbracelet/lipgloss` - Terminal styling
- `github.com/shirou/gopsutil/v4` - System metrics collection
