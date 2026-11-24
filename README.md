# moonlight

A cross-platform Go daemon and CLI for automatic **hardware monitor brightness (DDC/CI)** and **smooth night light (color temperature)** scheduling on **Windows** and **macOS**.

Designed for multi-monitor and KVM setups (e.g., switching between a Windows PC and a MacBook):

- **Automatic Monitor Adapter & Selection (`moonlight select`)** — discovers connected monitors on first run, detects vendor profiles (`Gigabyte / AORUS`, `Dell / Alienware`, `LG`, `ASUS`, `Samsung`, `BenQ`, or `Generic DDC/CI`), lets you pick one, multiple, or all monitors, remembers your choice in `config.json`, and lets you switch monitors anytime.
- **Hardware Brightness via DDC/CI** — controls your monitor's real backlight directly over DisplayPort, HDMI, or USB-C without washing out contrast.
- **Smooth Night Light via OS Gamma** (`night_light_mode: "gamma"`) — works in any monitor picture mode (`Standard`, `sRGB`, `Custom`) regardless of monitor firmware quirks. Optional `"ddc"` (hardware RGB gains) and `"both"` modes are also supported.
- **Zero External Go Dependencies** (`CGO_ENABLED=0`) — compiles to a ~3 MB standalone binary for Windows (`amd64`) and macOS (`arm64` / `amd64`).
- **KVM & Sleep Resilient** — re-opens display handles on every tick and automatically re-applies the active schedule when a monitor reconnects after a KVM switch or wake from sleep.
- **Live Config Hot-Reload** — edit `config.json` without restarting the daemon; invalid edits are safely rejected while keeping the last valid config active.
- **Multi-Layer Blackout Protection** — configurable safety floors, automatic initial hardware backup (`backup.json`), `-dry-run`, auto-reverting `-confirm` test mode, and one-command emergency recovery (`moonlight reset`).

---

## How It Works

| Feature | Windows (`amd64`) | macOS (`arm64` / `amd64`) |
|---|---|---|
| **Brightness (DDC/CI)** | Native WinAPI (`user32.dll` + `dxva2.dll`, VCP `0x10`) | [`m1ddc`](https://github.com/waydabber/m1ddc) (Apple Silicon) or `ddcctl` (Intel) |
| **Night Light (`gamma`)** | Native WinAPI (`gdi32.dll` → `SetDeviceGammaRamp`) | Built-in `CoreGraphics` (`CGSetDisplayTransferByFormula`) |
| **Night Light (`ddc`)** | WinAPI `dxva2.dll` (VCP `0x16`/`0x18`/`0x1A` + `0x14`) | `m1ddc` (`red`/`green`/`blue`) |
| **Background Autostart** | Windows Task Scheduler (hidden via `wscript.exe`) | macOS `launchd` (`LaunchAgents`) |
| **Config & State Dir** | `%APPDATA%\moonlight\` | `~/Library/Application Support/moonlight/` |

---

## Default 24-Hour Curve

Values are smoothly linearly interpolated every minute between keyframes (including wrap-around across midnight):

| Time | Brightness | Color Temp | RGB Multipliers |
|---|---:|---:|---|
| `20:00 – 09:30` | `0%` | `3400K` | `[100, 75, 54]` *(steady warm night plateau)* |
| `10:00` | `70%` | `6500K` | `[100, 100, 100]` *(start of workday)* |
| `12:00` | `60%` | `6200K` | `[100, 98, 97]` |
| `14:00` | `50%` | `5800K` | `[100, 96, 92]` |
| `16:00` | `40%` | `5300K` | `[100, 92, 86]` |
| `17:00` | `30%` | `4800K` | `[100, 88, 80]` *(−10%/hr)* |
| `18:00` | `20%` | `4300K` | `[100, 84, 72]` *(−10%/hr)* |
| `19:00` | `10%` | `3800K` | `[100, 79, 63]` *(−10%/hr)* |
| `20:00` | `0%` | `3400K` | `[100, 75, 54]` *(minimum brightness + max warmth)* |

> Inspect your active curve anytime with `moonlight preview`.

---

## Prerequisites

1. Enable **DDC/CI** in your monitor's OSD menu (typically **Settings / System → DDC/CI → ON**).
2. Disable your OS's built-in **Night Light** (Windows) / **Night Shift** (macOS) so it does not double-tint on top of `moonlight`'s gamma curve.
3. On **Windows**, keep **HDR** off if your monitor locks DDC/CI brightness in HDR mode.
4. On **macOS (Apple Silicon M1–M4)**, connect via direct **USB-C (DP Alt Mode)** or **USB-C → DisplayPort** and install `m1ddc`:
   ```bash
   brew install m1ddc
   ```
   *(On Intel Macs, install `brew install ddcctl`.)*

---

## Build & Installation

### Build Binaries

**Windows binary only (PowerShell):**
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1
```

**All platforms — Windows + macOS (macOS / Linux / Windows Git Bash):**
```bash
./scripts/build-all.sh
# or via Makefile:
make test && make build-all
```

Built artifacts in `dist/`:
- `dist/moonlight-windows-amd64.exe`
- `dist/moonlight-darwin-arm64` *(Apple Silicon)*
- `dist/moonlight-darwin-amd64` *(Intel Mac)*

---

### Quick Start — Windows

```powershell
# 1. Initialize config and interactively pick your monitor
.\dist\moonlight-windows-amd64.exe init

# 2. Preview the 24-hour curve and run a safe dry-run
.\dist\moonlight-windows-amd64.exe preview
.\dist\moonlight-windows-amd64.exe apply -dry-run

# 3. Install hidden background autostart (Task Scheduler)
powershell -ExecutionPolicy Bypass -File .\scripts\install-windows-task.ps1
```

---

### Quick Start — macOS

```bash
# 1. Install m1ddc (Apple Silicon)
brew install m1ddc

# 2. Install binary into PATH
sudo cp dist/moonlight-darwin-arm64 /usr/local/bin/moonlight
sudo chmod +x /usr/local/bin/moonlight

# 3. Pick your monitor and create config.json
moonlight init

# 4. Install background LaunchAgent
chmod +x scripts/install-macos-agent.sh
./scripts/install-macos-agent.sh /usr/local/bin/moonlight
```

macOS daemon logs are written to `~/Library/Logs/moonlight/stdout.log`.

---

## Switching Monitors Anytime

On first setup (`moonlight init`), `moonlight` scans connected displays, matches a vendor adapter profile, and saves your selection:

```text
Detected monitors:
  [1] Generic PnP Monitor | ID: GBT273E | Brightness 50/100 | Adapter: Gigabyte / AORUS
  [2] DELL U2723QE        | ID: DEL423E | Brightness 70/100 | Adapter: Dell / Alienware
  [0] All DDC/CI monitors (no filter)

Select monitor number (comma-separated for multiple, Enter = [1]):
```

To switch to a different monitor later (without losing your custom schedule), run:
```bash
moonlight select
```

---

## CLI Commands

| Command | Description |
|---|---|
| `moonlight init [-force] [-no-select]` | Create `config.json` and run interactive monitor selection |
| `moonlight select [-no-apply]` | Scan monitors, pick one/multiple/all, and save to `config.json` |
| `moonlight probe` | List connected DDC/CI monitors and read live VCP codes (`0x10`, `0x14`, `0x16/0x18/0x1A`) |
| `moonlight preview` | Print the interpolated 24-hour brightness and color temperature table |
| `moonlight status` | Show backend, active adapter, pause state, current target, and matched monitors |
| `moonlight apply [-dry-run]` | Apply the current schedule immediately (`-dry-run` logs without writing) |
| `moonlight set -brightness 30 -temp 4000 [-pause 2h] [-confirm]` | Manual override (`-confirm` auto-reverts after 12s unless you press Enter) |
| `moonlight pause [2h]` | Pause automatic updates for a duration (or indefinitely if omitted) |
| `moonlight resume` | Resume automatic updates and immediately apply the schedule |
| `moonlight reset` | **Emergency rescue**: pause daemon and restore safe brightness & neutral gamma/RGB on **all** monitors |
| `moonlight restore` | Pause daemon and restore the initial hardware snapshot saved in `backup.json` |
| `moonlight daemon [-dry-run]` | Run the background scheduling loop |

Use `-config /path/to/config.json` before any subcommand to target a custom config file.

---

## Configuration (`config.json`)

See [`config.example.json`](config.example.json) for the full default template:

- `adapter` — auto-detected monitor profile name (set by `moonlight init` / `moonlight select`).
- `monitor_match` — comma-separated case-insensitive substrings to filter monitors (`""` targets all DDC monitors).
- `night_light_mode` — `"gamma"` (recommended), `"ddc"`, or `"both"`.
- `min_brightness` / `allow_zero_brightness` — safety floor for backlight brightness (`0` is blocked unless `allow_zero_brightness` is `true`).
- `min_rgb` — hard minimum per RGB channel (`10..100`, default `25`) to prevent blacking out the screen.
- `safe_brightness` — fallback brightness used by `moonlight reset`.
- `schedule[].temp_k` — target color temperature in Kelvin (`1500..10000`), or specify explicit `"rgb": [100, 82, 66]`.

---

## Blackout Protection & Emergency Recovery

1. **Defense-in-Depth Safety Floors:** Both `config.Validate()` (on load and hot-reload) and `daemon.ClampTarget()` (right before any write) enforce `min_brightness` and `min_rgb`.
2. **Automatic Initial Backup (`backup.json`):** Before its first write to a monitor, `moonlight` snapshots the current hardware brightness and RGB. Run `moonlight restore` to revert to it.
3. **Auto-Reverting Test Mode (`-confirm`):**
   ```bash
   moonlight set -brightness 0 -temp 3400 -confirm
   ```
   Applies the settings and waits **12 seconds** for `Enter`; if you don't confirm, it automatically reverts.
4. **One-Command / One-Click Rescue (`moonlight reset`):**
   Works even if `config.json` is corrupted, ignores `monitor_match`, pauses the daemon, and resets brightness + gamma on all displays.
   - Windows: [`scripts/rescue-windows.bat`](scripts/rescue-windows.bat) *(tip: pin a shortcut to Desktop and bind a hotkey like `Ctrl+Alt+R`)*
   - macOS: [`scripts/rescue-macos.command`](scripts/rescue-macos.command)

---

## Repository Layout

```text
moonlight/
├── cmd/moonlight/main.go            # CLI and daemon entrypoint
├── internal/
│   ├── adapter/                     # Monitor auto-discovery, vendor profiles, interactive picker + tests
│   ├── config/                      # Config loader, validation, state.json, backup.json + tests
│   ├── schedule/                    # 24-hour curve interpolation & Kelvin -> RGB math + tests
│   ├── ddc/                         # DDC/CI backends (Windows dxva2 / macOS m1ddc, ddcctl)
│   ├── gamma/                       # Native OS gamma night light (Windows gdi32 / macOS CoreGraphics)
│   └── daemon/                      # Background loop, hot-reload, KVM reconnect handling, reset/restore
├── scripts/
│   ├── build-windows.ps1            # Build Windows binary (PowerShell)
│   ├── build-all.sh                 # Universal cross-compile script for Windows + macOS (macOS / Linux / Git Bash)
│   ├── install-windows-task.ps1     # Windows Task Scheduler autostart installer
│   ├── run-daemon-hidden.vbs        # Hidden launcher for Windows (no console window)
│   ├── install-macos-agent.sh       # macOS LaunchAgent installer
│   ├── rescue-windows.bat           # Emergency rescue script (Windows)
│   └── rescue-macos.command         # Emergency rescue script (macOS)
├── config.example.json              # Default 24-hour curve & safety config template
├── Makefile                         # Build & test targets (macOS / Linux / Git Bash)
└── go.mod
```
