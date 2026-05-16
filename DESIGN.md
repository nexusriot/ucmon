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
│   │   ├── power.go            # Battery, RPi throttle, load average
│   │   └── util.go             # Formatting helpers
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

- Top 100 processes sorted by CPU usage
- Columns: PID, USER, NAME, CPU%, MEM%, RSS, STATUS
- Scrollable viewport with search/filter support
- Refreshed every 3 seconds

### Tab 4: Disk Usage

- Partition list with device, mount point, filesystem type, total/used/free sizes
- Color-coded usage bar per partition (green → red based on usage percentage)
- Disk I/O rates (read/write bytes per second) per block device

### Tab 5: Network

- Active interface summary with MAC, address, RX/TX rates and sparkline history
- Connection table: protocol, local/remote address, status, PID, process name
- LISTEN connections sorted first
- Search/filter support for connection table

### Tab 6: Power & System Health

- Battery: charge %, charging status, voltage, instantaneous watts, estimated time to empty/full, technology — read directly from `/sys/class/power_supply` (works with the uConsole axp20x driver and standard laptop batteries); AC/USB mains presence
- Load average: 1/5/15-minute, color-coded against CPU count, with sparkline history
- Raspberry Pi throttle / under-voltage: parses `vcgencmd get_throttled`, showing both current and since-boot flags for under-voltage, ARM frequency capping, throttling, and soft temperature limit. Gracefully reports unavailable when `vcgencmd` is absent (non-Pi hosts)
- Refreshed every 5 seconds

## Refresh Intervals

| Data             | Interval |
|------------------|----------|
| CPU / temps      | 1s       |
| Memory / swap    | 1s       |
| Network I/O      | 1s       |
| Processes        | 3s       |
| Connections      | 3s       |
| Disk usage/IO    | 5s       |
| Battery/throttle | 5s       |

## Keyboard Controls

| Key              | Action                    |
|------------------|---------------------------|
| `tab` / `→`      | Next tab                  |
| `shift+tab` / `←`| Previous tab              |
| `1` - `6`        | Jump to tab               |
| `/`              | Activate search (tabs 3,5)|
| `enter`          | Apply search filter       |
| `esc`            | Cancel search             |
| `ctrl+u`         | Clear search filter       |
| `↑` `↓` PgUp/Dn | Scroll viewport           |
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
