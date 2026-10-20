# ucmon

A terminal-based system monitor for the ClockworkPi uConsole and Raspberry Pi devices. ucmon shows real-time CPU, processes, disk, and network stats in a compact TUI optimized for small screens.

Built in Go with the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework.

## Features

- **CPU & Temperature** — total and per-core usage with bar charts and sparkline history; a **time breakdown** (user/sys/nice/iowait/irq/steal) that separates "saturating the cores" from "blocked on the SD card"; color-coded thermal sensor readings with thresholds **calibrated from the kernel's own thermal trip points**; load-average summary; **cpufreq clocks and governor** per policy (current/min/max MHz with sparkline, `g` cycles the governor — performance vs battery on demand); CPU pressure (PSI)
- **Memory** — RAM total/used/free/available/cached/buffers and swap, with bars, sparkline history, and memory pressure (PSI)
- **Processes** — searchable list (matches name, PID, user, **and command line**) with a movable cursor, sortable by CPU / MEM / **DISK I/O** / PID / NAME, a parent/child tree view, and SIGTERM/SIGKILL with confirmation. CPU% and the **per-process disk I/O rate** are measured **since the previous refresh** (htop-style); the walk reads `/proc` directly, which is ~7× faster than the gopsutil accessor path it replaced. `enter` opens a **detail view**: threads, open fds, memory breakdown, per-process disk I/O, sockets, start time — with **renice (`+`/`-`)** and **SIGSTOP/SIGCONT** controls
- **Disk** — partition usage with color-coded bars, per-device read/write rates, I/O pressure (PSI), and **SD/eMMC wear**: life-time bands, pre-EOL reserve state and read-only lockout from `/sys/block/*/device`. Card death is the characteristic failure of these boards and the one reading nothing else hints at
- **Network** — interface throughput with sparklines and a searchable connection table (protocol incl. TCP6/UDP6, addresses, status, PID, process — inet only, so Unix sockets no longer pad it out) with per-interface **error and drop counters**, a **state filter** (`f`: all → LISTEN → ESTABLISHED) and per-state counts; **WiFi link quality / signal dBm** (from `/proc/net/wireless`) plus **SSID, channel, and bitrates** via `iw` when available; **connectivity monitor** pinging the default gateway (and an optional external target) with RTT sparkline and packet-loss tracking — unprivileged ICMP with a TCP-connect fallback; **4G/LTE modem status** (operator, registration, access technology, RSSI) via `mmcli` when `modem_enabled` is set
- **Power & System Health** — battery charge/status/time-left/**pack health** (uConsole/Pi via `/sys/class/power_supply`), power-draw sparkline, 1/5/15-min load average, and Raspberry Pi throttle / under-voltage flags via `vcgencmd`
- **GPU / VideoCore** — Raspberry Pi / uConsole SoC core temp, voltage, clock domains (arm/core/v3d/…) and CPU↔GPU memory split via `vcgencmd`, plus **display backlight readout and `+`/`-` control** (`/sys/class/backlight`)
- **Alerts** — configurable thresholds (temperature, RAM/swap, disk, battery, load, live Pi throttle flags, **weak WiFi signal, packet loss, PSI pressure, storage wear, modem registration**) raise edge-triggered alerts with **flap damping**, plus **predictive alerts on the rate of change** — battery drain %/h with a time-to-empty, filesystem fill with a time-to-full, and temperature climb with a time-to-critical. Alerting keeps running while sampling is paused. A severity-colored banner shows the top alert on every tab; the Alerts tab keeps a timestamped log. Optional **terminal bell** on criticals, an **`alert_cmd` shell hook** (gets `UCMON_ALERT_STATE/SEV/MSG`), and an **on-disk alert log**
- **Session peaks** — max CPU/temperature/load/memory and minimum battery since start, shown inline and in reports; `Z` resets them
- **Expanded graphs** — `z` blows one metric up to a full-screen braille chart with a real value axis and min/max/avg, stepping between every series the tab offers. Sparklines of bounded readings (CPU, memory, battery) are drawn on a fixed 0–100 axis, so a core pinned at 100% no longer looks like one sitting idle
- **Persistent history** — set `history_file` and the sparklines and session peaks survive a restart
- **Snapshot export** — press `w` on any tab to write a plain-text report of all current readings to `~/ucmon-report-<timestamp>.txt`
- **Pause** (`space`) freezes sampling to inspect readings; **help overlay** (`?`) lists every key
- **Capability report** — `ucmon doctor` lists every source ucmon reads (PSI, `vcgencmd`, `iw`, `mmcli`, unprivileged ICMP, `/proc/<pid>/io`, eMMC wear counters, the battery's design capacity, the writable governor and backlight files) as present / absent / denied / read-only, says which rows go missing without each one, and prints the exact udev rule for the two writable ones. `ucmon --check-config` does the same for config keys ucmon does not recognise
- **Headless mode** — `ucmon --once` prints the full snapshot report to stdout and exits (for SSH sessions and cron); `--once --json` emits the same readings as JSON for scripts
- **Prometheus exporter** — `ucmon --metrics :9100` serves `/metrics` and `/snapshot` without a TUI, or `metrics_listen` runs one alongside the monitor. Sampling is lazy and rate-limited, so a tight scrape loop cannot peg a CM4
- **Ignore lists** — `net_ignore` / `disk_ignore` / `temp_ignore` hide noisy names (container veths, snap loop mounts, per-core `coretemp` sensors) from their tab, the `--once` report and the alert engine alike; comma-separated globs where `*` spans `/`, so `/snap/*` covers a whole subtree
- **Sampling-lateness badge** — when a probe cannot finish inside its interval ucmon skips the next round rather than piling up work, and the header names what is lagging (`⏳ slow: gpu/soc`) so a stall on a slow board is diagnosable instead of invisible

## Development

```bash
make test-race    # go test -race with a coverage profile
make fmt-check    # fail if anything needs gofmt
make vet
make              # cross-compile every target into dist/
```

CI runs the same checks on every push, plus pinned `staticcheck` /
`golangci-lint`, a statically-linked assertion on all four cross-compiled
binaries, and a smoke job that drives the real CLI. See
[DESIGN.md](DESIGN.md#architecture) for the job breakdown.

## Command line

```
ucmon [flags] [doctor]
  --once            print one snapshot report to stdout and exit (no TUI)
  --json            with --once, print the snapshot as JSON instead of text
  --metrics <addr>  serve Prometheus metrics on this address (e.g. :9100), no TUI
  --doctor          report what this host can measure and why the rest is
                    missing, then exit (no TUI); `ucmon doctor` does the same
  --check-config    list the config keys ucmon does not recognise and exit
  --config <path>   use an explicit config file
  --tab <name>      start tab: cpu|mem|proc|disk|net|power|gpu|alerts
  --version         print the version and exit
```

`ucmon doctor` answers "why is the GPU tab empty" without a trip through the
source: it checks every source ucmon reads — thermal sensors, cpufreq, PSI,
`/proc/<pid>/io`, the battery and its design-capacity counters, `vcgencmd`,
the backlight, `iw`, `mmcli`, unprivileged ICMP and eMMC wear counters —
and prints each one as present, absent, denied or read-only, with what it
found, which rows go missing without it, and how to get it. For the two
writable sources (cpufreq `scaling_governor` and backlight `brightness`) that
remedy is the exact udev rule, ready to paste. It needs no privileges, writes
nothing, and always exits 0: an absent source is a fact about the board, not
an error. The report ends with the config settings that suppress a reading the
host could otherwise produce (`ping_enabled`, `modem_enabled`, the ignore
globs).

`ucmon --check-config` names the keys in your config file that ucmon does not
recognise, with their line numbers, and exits non-zero when it finds any, so a
packaging or provisioning script can gate on it. The monitor itself never
fails on one: a config written for a newer ucmon has to keep working, so
unknown keys are ignored at startup — with a one-line notice in the footer
naming them.

The exporter serves `/metrics` (Prometheus text format), `/snapshot` (the JSON
report) and `/healthz`. It re-samples at most once every 5 seconds however
often it is scraped, and collects through a model of its own, so running it
alongside the TUI (`metrics_listen`) never perturbs what is on screen.

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

# Hide noisy names from their tab, the --once report and the alert engine.
# Comma-separated globs; "*" spans "/" so "/snap/*" covers a whole subtree.
# Empty (the default) shows everything.
# net_ignore   = "veth*, br-*, docker*"      # container plumbing
# disk_ignore  = "/snap/*, /var/lib/docker/*"
# temp_ignore  = "coretemp_core_*"           # keep the package sensor only

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
# alert_cmd      = 'notify-send ucmon "$UCMON_ALERT_MSG"'   # run on each raise/clear
# alert_log_file = "~/ucmon-alerts.log"                      # append raise/clear lines

# WiFi & connectivity
# wifi_low_dbm   = -75     # warn when a wireless signal drops to/below this (0 = off)
ping_enabled     = true    # ping the default gateway on the proc interval
# ping_target    = "1.1.1.1"  # extra external target (empty = gateway only)
# ping_loss_warn_pct = 20  # packet-loss % that raises a warning (0 = off)

# Pressure stall information
# psi_warn_pct   = 40      # warn when some-avg10 pressure ≥ this % (0 = off)

# Rate-based (predictive) alerts. A level threshold cannot tell a pack
# discharging over eight hours from one dumping in twenty minutes; these fire
# on the slope, while there is still time to act. 0 disables each.
batt_drain_warn_pct_h    = 25   # discharging faster than this %/hour warns
disk_full_warn_hours     = 6    # warn when a filesystem is this many hours from full
temp_rise_warn_c_per_min = 3    # warn when a sensor climbs this fast

# Temperature thresholds are seeded from the kernel's own thermal trip points
# unless temp_warn/temp_hot/temp_crit are set above. The built-in 50/65/80
# suits a Raspberry Pi (which throttles at 80 °C) and is wrong by tens of
# degrees elsewhere.
temp_from_trip_points = true

# WWAN modem (uConsole 4G board) via ModemManager. Off by default: it shells
# out to mmcli on the slow tick.
# modem_enabled  = true

# History, limits and output paths
history_len    = 200     # sparkline ring-buffer length
procs_cap      = 200     # process rows rendered
# report_dir   = "~/reports"             # where `w` writes snapshots (default: ~)
# history_file = "~/.cache/ucmon/history.json"  # keep sparklines across restarts
# metrics_listen = ":9100"               # also serve Prometheus metrics from the TUI
```

Intervals accept a Go duration (`"500ms"`, `"2s"`) or a bare number of seconds.
Comments (`#`), and `[section]` headers are ignored. Unknown keys are ignored
too — never fatal, so a file written for a newer release still starts — but
ucmon names them in a startup notice, and `ucmon --check-config` lists them
with their line numbers.

> Changing backlight brightness or the cpufreq governor writes to sysfs, which
> usually requires root or a udev rule granting your user write access. ucmon
> shows the values read-only otherwise, and `ucmon doctor` prints the udev
> rule for whichever of the two this host refuses.
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
| `space`            | Pause / resume sampling (alerts keep running) |
| `z`                | Expand a metric to a full-screen graph |
| `Z`                | Reset session peaks          |
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
| `home` / `end`     | First / last process row     |
| `s`                | Cycle process sort (CPU/MEM/I-O/PID/NAME) |
| `t`                | Toggle process tree / flat view |
| `k` / `K`          | SIGTERM / SIGKILL selected process (confirm with `y`) |
| `j` / `k`          | Select interface (network tab) |
| `f`                | Cycle connection state filter (network tab) |
| `ctrl+c`           | Quit                         |

In the process detail view: `+`/`-` renice, `p` SIGSTOP, `c` SIGCONT, `k`/`K` kill, `esc` close.

In the expanded graph (`z`): `←`/`→` (or `j`/`k`) step between metrics, `z`/`esc` closes.

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
| Storage wear / modem | 5s  |

## Target Platforms

- ClockworkPi uConsole (ARM64, Raspberry Pi CM4)
- Raspberry Pi 3 / 4 / 5 (ARM64)
- Generic Linux x86_64

## Design

See [DESIGN.md](DESIGN.md) for architecture, layout, and implementation notes,
and [ROADMAP.md](ROADMAP.md) for planned work.
