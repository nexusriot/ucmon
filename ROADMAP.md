# ucmon roadmap

Planned work and known limitations, roughly in priority order. See
[DESIGN.md](DESIGN.md) for how the current features are built.

## Shipped in 0.8.0 (for context)

Straight from 0.6.0 — 0.7.0 was never tagged, so its correctness work is folded
in here. No new tabs, but a lot that was quietly broken now works.

**Correctness.** The config file actually applies: inline `#` comments were
being read as part of the value, so every key in the README's own example was
a no-op (masked because each example value equalled its default), and quote
stripping mangled any `alert_cmd` with nested quotes. Out-of-range alert
thresholds fall back to defaults instead of firing forever or never, and
`temp_warn ≤ temp_hot ≤ temp_crit` is enforced. Filtering the process list no
longer crashes the TUI on rows holding a multi-byte character (the `…` from a
truncated name was enough). Search boxes keep the digits, arrows, tab and `/`
that the global key map used to steal. Counter resets report 0 instead of
3.2 EiB/s. Frames never outgrow the terminal, and a too-small terminal says so
instead of blanking the alt screen. Interface history is evicted on churn,
selection follows the interface by name, DNS in the ping path is bounded by
the sample deadline, and the slow fetches will not queue twice.

**Features.** **Ignore lists** (`net_ignore`, `disk_ignore`,
`temp_ignore`) hide noisy names — container veths, snap loop mounts, per-core
coretemp sensors — from their tab, the `--once` report and the alert engine
together, because filtering happens where each snapshot lands rather than in
the views. `*` spans `/`, so `/snap/*` covers a subtree. Default is empty:
ucmon shows the whole machine until told otherwise. **A sampling-lateness
badge** in the header names the probe that could not keep up (`⏳ slow:
gpu/soc`), turning an invisible stall on a CM4 into a diagnosis — the
in-flight guards already knew, they just had nowhere to say it.

**Tooling.** Test suite grew 110 → 251, and **CI landed** (`.github/workflows/ci.yml`):
race-detector tests with coverage, pinned staticcheck + golangci-lint, a
cross-compile job asserting all four targets stay statically linked, and a
smoke job that runs the real CLI because `main` is the one function unit tests
cannot reach.

Filling the coverage gaps turned up one more bug of its own: gopsutil's
`Process.Nice()` forwards the raw `getpriority(2)` return, which is `20-nice`
(its source even carries a `// FIXME: is this true?` on that line). ucmon showed
a normal process as nice 20, and renicing by ±1 from there computed
`ClampNice(20±1)` = 19 both ways — one keypress dropped any process to the
lowest priority. `probe.getNice` now converts it.

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
  CM4. In-flight guards (0.8.0) stop the herd from *compounding* and the
  header badge makes it visible, but the schedules still need offsetting to
  smooth the spike itself.
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
  failures are silent by design (the alert itself is already visible). There is
  no cap on concurrent hooks — a condition flapping with `alert_clear_ticks = 1`
  can hold several shells open at once.
- **Config values are literal**: `splitValue` strips one matched pair of
  surrounding quotes and an unquoted trailing `#` comment, but does not
  interpret TOML escape sequences. Write shell hooks as literal strings
  (`alert_cmd = '…'`) when they contain double quotes.
- **Minimum terminal size** is 30×6; below that ucmon draws a one-line notice
  rather than a frame, because the chrome alone (header, footer, box border)
  costs 4 rows and an overflowing frame blanks the alt screen.
