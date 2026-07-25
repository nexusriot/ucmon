# ucmon

A terminal-based system monitor for the ClockworkPi uConsole and Raspberry Pi devices. ucmon shows real-time CPU, processes, disk, and network stats in a compact TUI optimized for small screens.

Built in Go with the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework.

## Features

- **CPU & Temperature** — total and per-core usage with bar charts and sparkline history; color-coded thermal sensor readings; load-average summary; **cpufreq clocks and governor** per policy (current/min/max MHz with sparkline, `g` cycles the governor — performance vs battery on demand); CPU pressure (PSI)
- **Memory** — RAM total/used/free/available/cached/buffers and swap, with bars, sparkline history, and memory pressure (PSI)
- **Processes** — searchable list (matches name, PID, user, **and command line**) with a movable cursor, sortable by CPU / MEM / PID / NAME, a parent/child tree view, and SIGTERM/SIGKILL with confirmation. CPU% is measured **since the previous refresh** (htop-style). `enter` opens a **detail view**: threads, open fds, memory breakdown, per-process disk I/O, sockets, start time — with **renice (`+`/`-`)** and **SIGSTOP/SIGCONT** controls
- **Disk** — partition usage with color-coded bars, per-device read/write rates, and I/O pressure (PSI)
- **Network** — interface throughput with sparklines and a searchable connection table (protocol incl. TCP6/UDP6, addresses, status, PID, process) with a **state filter** (`f`: all → LISTEN → ESTABLISHED) and per-state counts; **WiFi link quality / signal dBm** (from `/proc/net/wireless`) plus **SSID, channel, and bitrates** via `iw` when available; **connectivity monitor** pinging the default gateway (and an optional external target) with RTT sparkline and packet-loss tracking — unprivileged ICMP with a TCP-connect fallback
- **Power & System Health** — battery charge/status/time-left/**pack health** (uConsole/Pi via `/sys/class/power_supply`), power-draw sparkline, 1/5/15-min load average, and Raspberry Pi throttle / under-voltage flags via `vcgencmd`
- **GPU / VideoCore** — Raspberry Pi / uConsole SoC core temp, voltage, clock domains (arm/core/v3d/…) and CPU↔GPU memory split via `vcgencmd`, plus **display backlight readout and `+`/`-` control** (`/sys/class/backlight`)
- **Alerts** — configurable thresholds (temperature, RAM/swap, disk, battery, load, live Pi throttle flags, **weak WiFi signal, packet loss, PSI pressure**) raise edge-triggered alerts with **flap damping**. A severity-colored banner shows the top alert on every tab; the Alerts tab keeps a timestamped log. Optional **terminal bell** on criticals, an **`alert_cmd` shell hook** (gets `UCMON_ALERT_STATE/SEV/MSG`), and an **on-disk alert log**
- **Session peaks** — max CPU/temperature/load/memory and minimum battery since start, shown inline and in reports
- **Snapshot export** — press `w` on any tab to write a plain-text report of all current readings to `~/ucmon-report-<timestamp>.txt`
- **Pause** (`space`) freezes sampling to inspect readings; **help overlay** (`?`) lists every key
- **Headless mode** — `ucmon --once` prints the full snapshot report to stdout and exits (for SSH sessions and cron)

## Command line

```
ucmon [flags]
  --once            print one snapshot report to stdout and exit (no TUI)
  --config <path>   use an explicit config file
  --tab <name>      start tab: cpu|mem|proc|disk|net|power|gpu|alerts
  --version         print the version and exit
```

## Configuration

ucmon runs with sensible defaults and needs no config. To customise, create
`~/.config/ucmon/config.toml` (respects `$XDG_CONFIG_HOME`):

```toml
start_tab        = "cpu"   # cpu | mem | proc | disk | net | power | gpu | alerts
fast_interval    = "1s"    # CPU / memory / network throughput tick
proc_interval    = "3s"    # process & connection list (also the ping cadence)
slow_interval    = "5s"    # disk, power, GPU/SoC, iw
temp_warn        = 50      # °C — yellow threshold
temp_hot         = 65      # °C — orange threshold (also alert: warning)
temp_crit        = 80      # °C — red threshold (also alert: critical)
backlight_step   = 10      # percent change per +/- keypress
# backlight_device = "intel_backlight"  # override auto-detect

# Alerts
alerts_enabled   = true    # master switch for the Alerts tab + banner
mem_warn_pct     = 90      # RAM used % that raises a warning
swap_warn_pct    = 50      # swap used % that raises a warning
disk_warn_pct    = 90      # filesystem used % that raises a warning (squashfs skipped)
batt_low_pct     = 15      # discharging battery % — warning
batt_crit_pct    = 5       # discharging battery % — critical
load_warn_factor = 2.0     # load1 ≥ factor × cores raises a warning
alert_log_cap    = 200     # max alert-log entries kept in memory
alert_clear_ticks = 3      # consecutive OK ticks before an alert clears (flap damping)
alert_bell       = true    # terminal bell on a new critical alert
# alert_cmd      = "notify-send ucmon \"$UCMON_ALERT_MSG\""  # run on each raise/clear
# alert_log_file = "~/ucmon-alerts.log"                      # append raise/clear lines

# WiFi & connectivity
# wifi_low_dbm   = -75     # warn when a wireless signal drops to/below this (0 = off)
ping_enabled     = true    # ping the default gateway on the proc interval
# ping_target    = "1.1.1.1"  # extra external target (empty = gateway only)
# ping_loss_warn_pct = 20  # packet-loss % that raises a warning (0 = off)

# Pressure stall information
# psi_warn_pct   = 40      # warn when some-avg10 pressure ≥ this % (0 = off)
```

Intervals accept a Go duration (`"500ms"`, `"2s"`) or a bare number of seconds.
Unknown keys, comments (`#`), and `[section]` headers are ignored.

> Changing backlight brightness or the cpufreq governor writes to sysfs, which
> usually requires root or a udev rule granting your user write access. ucmon
> shows the values read-only otherwise.
>
> The latency monitor uses unprivileged ICMP datagram sockets (enabled by
> default on Raspberry Pi OS via `net.ipv4.ping_group_range`); where those are
> unavailable it falls back to timing TCP connects to ports 80/443.

## Install

All builds are pure Go (no cgo), so every platform cross-compiles from one machine.

### Debian / Ubuntu (incl. uConsole, Raspberry Pi OS)

```bash
make deb                 # .deb for every platform → dist/
make deb-uconsole        # just the uConsole (arm64) package
sudo dpkg -i dist/ucmon_*_arm64.deb
```

| `make` target | Device                                   | deb arch  |
|---------------|------------------------------------------|-----------|
| `amd64`       | Generic Linux x86_64                      | `amd64`   |
| `uconsole`    | ClockworkPi uConsole (CM4, aarch64)       | `arm64`   |
| `pizero2w`    | Raspberry Pi Zero 2 W (32-bit Pi OS)      | `armhf`   |
| `licheerv`    | Sipeed LicheeRV Nano (SG2002, riscv64)    | `riscv64` |

> A Pi Zero 2 W running a **64-bit** OS uses the `uconsole` / `arm64` build.
>
> The LicheeRV Nano image is Debian-based: `make deb-licheerv` then
> `sudo dpkg -i dist/ucmon_*_riscv64.deb` on the board.

### From source

```bash
make install            # build for the host, install to /usr/local/bin (sudo)
make run                # build & run natively
make test               # unit tests
make help               # list all targets
```

## Keyboard Controls

| Key                | Action                       |
|--------------------|------------------------------|
| `tab` / `→`        | Next tab                     |
| `shift+tab` / `←`  | Previous tab                 |
| `1` – `8`          | Jump to tab                  |
| `?`                | Help overlay (all bindings)  |
| `space`            | Pause / resume sampling      |
| `w`                | Write snapshot report to `~/ucmon-report-*.txt` |
| `g`                | Cycle cpufreq governor (CPU tab) |
| `+` / `-`          | Adjust backlight (GPU tab)    |
| `m`                | Mute / unmute alert banner (Alerts tab) |
| `c`                | Clear alert log (Alerts tab) |
| `/`                | Activate search (procs, net) |
| `enter`            | Apply search / open process details |
| `esc`              | Cancel search / kill / close overlay |
| `ctrl+u`           | Clear search filter          |
| `↑` `↓` PgUp/PgDn  | Move cursor (procs) / scroll |
| `s`                | Cycle process sort (CPU/MEM/PID/NAME) |
| `t`                | Toggle process tree / flat view |
| `k` / `K`          | SIGTERM / SIGKILL selected process (confirm with `y`) |
| `j` / `k`          | Select interface (network tab) |
| `f`                | Cycle connection state filter (network tab) |
| `ctrl+c`           | Quit                         |

In the process detail view: `+`/`-` renice, `p` SIGSTOP, `c` SIGCONT, `k`/`K` kill, `esc` close.

## Refresh Intervals

| Data            | Interval |
|-----------------|----------|
| CPU / temps / cpufreq / PSI | 1s |
| Memory / swap   | 1s       |
| Network I/O     | 1s       |
| Processes       | 3s       |
| Connections     | 3s       |
| Ping targets    | 3s       |
| Disk usage/IO   | 5s       |
| Battery/throttle| 5s       |
| WiFi `iw` details | 5s     |

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3 / 4 / 5 (ARM64)
- Generic Linux x86_64

## Design

See [DESIGN.md](DESIGN.md) for architecture, layout, and implementation notes,
and [ROADMAP.md](ROADMAP.md) for planned work.
