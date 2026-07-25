# ucmon roadmap

Planned work and known limitations, roughly in priority order. See
[DESIGN.md](DESIGN.md) for how the current features are built.

## Shipped in 0.6.0 (for context)

CLI flags + `--once` headless report; cpufreq clocks/governor (read + `g`
cycle); PSI on the CPU/Mem/Disk tabs; help overlay (`?`); pause (`space`);
cmdline search; session peaks; SSID/channel/bitrates via `iw`; gateway/target
latency monitor (unprivileged ICMP + TCP fallback) with loss tracking;
process detail overlay with renice/SIGSTOP/SIGCONT; alert flap damping, bell,
`alert_cmd` hook, on-disk alert log; connection state filter, counts, and
TCP6/UDP6 labels; battery health % and power-draw sparkline; interval-accurate
process CPU% and disk I/O rates; first test suite (57 tests).

## Next up

- **Metrics logging** — `--log <file.csv>` appending one row per fast tick
  (timestamp, cpu%, temp, mem%, rx/tx, battery%, load1) for later graphing;
  the TUI-less sibling of `--once`. Consider a JSONL variant.
- **Services tab** — failed/active systemd units via `systemctl` shell-out
  (same graceful-absence pattern as vcgencmd), with start/restart/stop behind
  the existing y/N confirm flow.
- **Kernel log tail tab** — `dmesg --follow` style view with severity
  coloring; under-voltage and OOM events land exactly there, pairing with the
  Power tab and alert engine (e.g. alert on new `Under-voltage detected!`).
- **Peaks reset key** — zero the session-peak tracker without restarting
  (candidate: `Z` on any tab), plus "since" timestamp.
- **Staggered sampling** — proc/slow fetches all fire on the same tick when
  their multiples align (every 15 s by default), a small thundering herd on a
  CM4. Offset the schedules to smooth CPU.
- **Config for history/UI constants** — sparkline history length (fixed 200),
  process display cap (fixed 200), snapshot export directory (fixed `~`).

## Later / bigger bets

- **4G/LTE modem status** (uConsole extension board) — operator, registration,
  RSSI via `mmcli`; graceful absence on hosts without ModemManager.
- **Braille multi-row graphs** — btop-style higher-resolution charts when the
  terminal is tall enough; the uConsole's 1280x480 panel is wide-but-short, so
  keep 1-row sparklines the default and gate on available height (the Power
  tab's `roomy` pattern).
- **Record & replay** — persist the metric ring buffers so sparklines survive
  restarts; optionally `--replay <file>` to scrub a recorded session.
- **Per-process network rates** — needs packet accounting (eBPF or pcap) or
  `/proc/net` diffing per socket; heavier than the current pure-procfs stance,
  so design carefully before committing.
- **Theming** — configurable color palette / mono mode beyond termenv's
  automatic `NO_COLOR` handling.
- **Mouse support** — click tab headers to switch; wheel already scrolls the
  viewports.

## Known limitations

- **WiFi quality scale** assumes the conventional cfg80211 maximum of 70 in
  `/proc/net/wireless`; drivers with other scales skew the percentage (the
  dBm reading is always trustworthy). `iw` reports could calibrate it.
- **Latency monitor**: ICMP needs `net.ipv4.ping_group_range` to include the
  user (default-open on Raspberry Pi OS / systemd ≥ 247). The TCP fallback
  reads a gateway that silently DROPs ports 80/443 as loss. IPv6 targets are
  not supported yet.
- **PID reuse / exec renames**: the persistent process sampler can serve one
  stale sample or name for a recycled PID or a process that exec'd; htop-style
  createTime revalidation would fix it at the cost of an extra read per PID.
- **PSI** requires `CONFIG_PSI=y` (present on Raspberry Pi OS ≥ 5.10 kernels;
  some distros need `psi=1` on the kernel cmdline). The tabs simply omit the
  rows when absent.
- **Battery health** needs `*_full_design` counters; the uConsole axp20x
  driver exposes them on recent kernels only.
- **Governor / backlight writes** need root or a udev rule; ucmon shows both
  read-only otherwise (`(ro)` marker / hint line).
- **Alert cmd** runs `/bin/sh -c` with a 10 s timeout and no output capture;
  failures are silent by design (the alert itself is already visible).
