# ucmon

A terminal-based system monitor for the ClockworkPi uConsole and Raspberry Pi devices. ucmon shows real-time CPU, processes, disk, and network stats in a compact TUI optimized for small screens.

Built in Go with the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework.

## Features

- **CPU & Temperature** — total and per-core usage with bar charts and sparkline history; color-coded thermal sensor readings; load-average summary
- **Memory** — RAM total/used/free/available/cached/buffers and swap, with bars and sparkline history
- **Processes** — searchable list with a movable cursor, sortable by CPU / MEM / PID / NAME, a parent/child tree view, a command-line detail line, and SIGTERM/SIGKILL with confirmation
- **Disk** — partition usage with color-coded bars, plus per-device read/write rates
- **Network** — interface throughput with sparklines and a searchable connection table (protocol, addresses, status, PID, process); **WiFi link quality / signal dBm** for wireless interfaces (from `/proc/net/wireless`)
- **Power & System Health** — battery charge/status/time-left (uConsole/Pi via `/sys/class/power_supply`), 1/5/15-min load average, and Raspberry Pi throttle / under-voltage flags via `vcgencmd`
- **GPU / VideoCore** — Raspberry Pi / uConsole SoC core temp, voltage, clock domains (arm/core/v3d/…) and CPU↔GPU memory split via `vcgencmd`, plus **display backlight readout and `+`/`-` control** (`/sys/class/backlight`)

## Configuration

ucmon runs with sensible defaults and needs no config. To customise, create
`~/.config/ucmon/config.toml` (respects `$XDG_CONFIG_HOME`):

```toml
start_tab        = "cpu"   # cpu | mem | proc | disk | net | power | gpu
fast_interval    = "1s"    # CPU / memory / network throughput tick
proc_interval    = "3s"    # process & connection list
slow_interval    = "5s"    # disk, power, GPU/SoC
temp_warn        = 50      # °C — yellow threshold
temp_hot         = 65      # °C — orange threshold
temp_crit        = 80      # °C — red threshold
backlight_step   = 10      # percent change per +/- keypress
# backlight_device = "intel_backlight"  # override auto-detect
```

Intervals accept a Go duration (`"500ms"`, `"2s"`) or a bare number of seconds.
Unknown keys, comments (`#`), and `[section]` headers are ignored.

> Changing backlight brightness writes to `/sys/class/backlight/<dev>/brightness`,
> which usually requires root or a udev rule granting your user write access. ucmon
> shows the brightness read-only otherwise.

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
| `1` – `7`          | Jump to tab                  |
| `+` / `-`          | Adjust backlight (GPU tab)    |
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
