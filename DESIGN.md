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
`backlight_step`, `backlight_device`. See the README for an annotated example.

## Keyboard Controls

| Key              | Action                    |
|------------------|---------------------------|
| `tab` / `→`      | Next tab                  |
| `shift+tab` / `←`| Previous tab              |
| `1` - `7`        | Jump to tab               |
| `+` / `-`        | Adjust backlight (tab 7)  |
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

Debian package (follows ducknetview pattern):
```bash
./build-deb.sh          # amd64
./build-deb-arm64.sh    # arm64 (cross-compile)
```

The build script produces a `.deb` file with the binary installed to `/usr/bin/ucmon`.

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3/4/5 (ARM64)
- Generic Linux x86_64

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - UI components (viewport, textinput, list)
- `github.com/charmbracelet/lipgloss` - Terminal styling
- `github.com/shirou/gopsutil/v4` - System metrics collection
