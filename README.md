# ucmon

A terminal-based system monitor for the ClockworkPi uConsole and Raspberry Pi devices. ucmon shows real-time CPU, processes, disk, and network stats in a compact TUI optimized for small screens.

Built in Go with the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework.

## Features

- **CPU & Temperature** — total and per-core usage with bar charts and sparkline history; color-coded thermal sensor readings; load-average summary
- **Memory** — RAM total/used/free/available/cached/buffers and swap, with bars and sparkline history
- **Processes** — searchable list with a movable cursor, sortable by CPU / MEM / PID / NAME, a parent/child tree view, a command-line detail line, and SIGTERM/SIGKILL with confirmation
- **Disk** — partition usage with color-coded bars, plus per-device read/write rates
- **Network** — interface throughput with sparklines and a searchable connection table (protocol, addresses, status, PID, process)
- **Power & System Health** — battery charge/status/time-left (uConsole/Pi via `/sys/class/power_supply`), 1/5/15-min load average, and Raspberry Pi throttle / under-voltage flags via `vcgencmd`

## Install

All builds are pure Go (no cgo), so every platform cross-compiles from one machine.

### Debian / Ubuntu (incl. uConsole, Raspberry Pi OS)

```bash
make deb                 # .deb for every platform → dist/
make deb-uconsole        # just the uConsole (arm64) package
sudo dpkg -i dist/ucmon_*_arm64.deb
```

| `make` target | Device                                   | deb arch |
|---------------|------------------------------------------|----------|
| `amd64`       | Generic Linux x86_64                      | `amd64`  |
| `uconsole`    | ClockworkPi uConsole (CM4, aarch64)       | `arm64`  |
| `pizero2w`    | Raspberry Pi Zero 2 W (32-bit Pi OS)      | `armhf`  |

> A Pi Zero 2 W running a **64-bit** OS uses the `uconsole` / `arm64` build.

### From source

```bash
make install            # build for the host, install to /usr/local/bin (sudo)
make run                # build & run natively
make help               # list all targets
```

## Keyboard Controls

| Key                | Action                       |
|--------------------|------------------------------|
| `tab` / `→`        | Next tab                     |
| `shift+tab` / `←`  | Previous tab                 |
| `1` – `6`          | Jump to tab                  |
| `/`                | Activate search (procs, net) |
| `enter`            | Apply search filter          |
| `esc`              | Cancel search / kill prompt  |
| `ctrl+u`           | Clear search filter          |
| `↑` `↓` PgUp/PgDn  | Move cursor (procs) / scroll |
| `s`                | Cycle process sort (CPU/MEM/PID/NAME) |
| `t`                | Toggle process tree / flat view |
| `k` / `K`          | SIGTERM / SIGKILL selected process (confirm with `y`) |
| `j` / `k`          | Select interface (network tab) |
| `ctrl+c`           | Quit                         |

## Refresh Intervals

| Data            | Interval |
|-----------------|----------|
| CPU / temps     | 1s       |
| Memory / swap   | 1s       |
| Network I/O     | 1s       |
| Processes       | 3s       |
| Connections     | 3s       |
| Disk usage/IO   | 5s       |
| Battery/throttle| 5s       |

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3 / 4 / 5 (ARM64)
- Generic Linux x86_64

## Design

See [DESIGN.md](DESIGN.md) for architecture, layout, and implementation notes.
