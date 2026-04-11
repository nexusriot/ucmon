# ucmon

A terminal-based system monitor for the ClockworkPi uConsole and Raspberry Pi devices. ucmon shows real-time CPU, processes, disk, and network stats in a compact TUI optimized for small screens.

Built in Go with the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework.

## Features

- **CPU & Temperature** — total and per-core usage with bar charts and sparkline history; color-coded thermal sensor readings
- **Processes** — top 100 processes by CPU, scrollable and searchable
- **Disk** — partition usage with color-coded bars, plus per-device read/write rates
- **Network** — interface throughput with sparklines and a searchable connection table (protocol, addresses, status, PID, process)

## Install

### Debian / Ubuntu (incl. uConsole, Raspberry Pi OS)

```bash
./build-deb.sh          # amd64
./build-deb-arm64.sh    # arm64 (cross-compile)
sudo dpkg -i ucmon_*.deb
```

### From source

```bash
go build -o ucmon cmd/ucmon/main.go
./ucmon
```

## Keyboard Controls

| Key                | Action                       |
|--------------------|------------------------------|
| `tab` / `→`        | Next tab                     |
| `shift+tab` / `←`  | Previous tab                 |
| `1` – `4`          | Jump to tab                  |
| `/`                | Activate search (procs, net) |
| `enter`            | Apply search filter          |
| `esc`              | Cancel search                |
| `ctrl+u`           | Clear search filter          |
| `↑` `↓` PgUp/PgDn  | Scroll viewport              |
| `ctrl+c`           | Quit                         |

## Refresh Intervals

| Data          | Interval |
|---------------|----------|
| CPU / temps   | 1s       |
| Network I/O   | 1s       |
| Processes     | 3s       |
| Connections   | 3s       |
| Disk usage/IO | 5s       |

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3 / 4 / 5 (ARM64)
- Generic Linux x86_64

## Design

See [DESIGN.md](DESIGN.md) for architecture, layout, and implementation notes.
