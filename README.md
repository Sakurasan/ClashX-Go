# ClashX Go

A macOS menu bar Clash (mihomo) client built with
[MyGo](https://mygo.egoist.dev) and the
[uix](https://github.com/smallnest/uix) shadcn-style component library.

The mihomo core **ships inside the app**: it is downloaded once at build
time, bundled into the `.app`, and copied out to the user's data directory
on first launch — no runtime download. Click Start in the menu bar panel to
boot it, Stop to shut it down. The panel shows live traffic, policy groups
and a small settings form, all without opening a full window — it floats
next to the tray icon.

## Features

- Menu bar app (accessory app, no Dock icon) with a frameless always-on-top
  panel window
- Self-contained core: one universal `.app` (arm64 + x86_64) with the mihomo
  core bundled in; no download, no network needed at first run
- Live traffic data (up/down bytes via the `/traffic` stream)
- Overview tab: running state, version, mode, download/upload totals
- Proxies tab: policy groups (Selector/URLTest/Fallback/LoadBalance),
  choose a node, run a group latency test
- Settings tab: mixed port, external-controller port, API secret,
  **subscription URL** (auto-pulls nodes + updates), optional config
  profile, Save & Restart Core
- Persisted settings in `~/Library/Application Support/clashx-go/app.json`
- Core logs written to `core.log` next to the binary

## Getting it to proxy for real

The mihomo core runs out of the box, but with **no real nodes** it only
routes `MATCH,DIRECT` — every connection goes straight out locally, so
there is no proxy effect. To get proxying you must give the app real
nodes. Two ways, both in the **Settings** tab:

1. **Subscription URL** (recommended): paste your Clash subscription link.
   The generated config adds a `proxy-providers` block — mihomo pulls the
   nodes itself and refreshes them hourly. This is the only network the
   app needs at runtime (for the subscription and geo data).
2. **Config profile**: select a local `config.yaml` (e.g. from an existing
   ClashX/Clash client). It wins over the subscription when both are set.

> The app bundles a **GeoIP database** (`geoip.metadb`) so the `GEOIP,CN`
> rules work offline on first launch — no slow geo download on startup.

### A note on free test nodes

The core, subscription fetching, policy groups and the `GEOIP,CN,DIRECT`
split all work (verified end to end). But free/aggregated public
subscriptions contain mostly dead or heavily throttled nodes — Google via
a dead JP node times out or fails TLS while baidu still goes DIRECT. Use a
**paid, working subscription** for reliable proxying; the app just needs
the URL.

## Requirements

- macOS 11+ (the shipped app is universal: arm64 and x86_64)
- Go 1.27+ to build; the MyGo CLI comes from `go tool mygo`
- `curl` and `gunzip` for the one-time core pre-download

## Build

The core binaries must be fetched **before** building, for both
architectures, so the bundled app is universal and self-contained:

```sh
scripts/fetch-cores.sh                  # downloads mihomo v1.19.32 for arm64 + amd64
go tool mygo build -platform darwin/universal
```

`fetch-cores.sh` writes `resources/darwin-arm64/mihomo` and
`resources/darwin-amd64/mihomo`. MyGo's platform resource directories ship
only with the matching platform's build, and for `darwin/universal` it
**combines the two per-arch cores into a single universal `mihomo`** inside
`Contents/Resources` — so one app runs natively on both Apple Silicon and
Intel, with the right core architecture chosen at runtime.

Output:

```
build/darwin-universal/
  clashx-go.app          ~135 MB (universal, core included)
  clashx-go 0.1.0.dmg    ~39 MB
```

A single-architecture build works too (its core is flattened into
`Contents/Resources/mihomo`):

```sh
go tool mygo build -platform darwin/arm64
```

## Run in development

```sh
go build . && ./clashx-go            # needs resources/darwin-<arch>/mihomo
go tool mygo dev                     # live reload via the MyGo CLI
```

## Tests

```sh
go test ./...
go vet ./...
```

- `main_test.go` renders the panel headlessly with MyGo's `ui.NewTester`
  (no window, no core).
- `core/smoke_test.go` boots the **real** bundled core end to end: copies
  it from the resources, generates a config, waits for the external
  controller, checks the secret is enforced, and stops it. It skips when
  the bundled core is absent.

## How the core is managed

- `scripts/fetch-cores.sh` downloads `mihomo-darwin-{arm64,amd64}.gz` into
  `resources/darwin-<arch>/mihomo` before the build.
- On first launch `Manager.EnsureBinary` copies the bundled core (the
  universal `Resources/mihomo`, or the per-arch file) to
  `~/Library/Application Support/clashx-go/mihomo`, `chmod +x`. Nothing is
  fetched from the network at runtime.
- `config.yaml` is generated next to it with the mixed port, external
  controller `127.0.0.1:<port>`, optional secret and a minimal DNS block, so
  the core runs cleanly out of the box.
- When a config profile is set in Settings, that file is used instead
  (`-f <profile>`), and the controller endpoint and secret are overridden on
  the CLI with `-ext-ctl` and `-secret` (verified against the mihomo source:
  there is no `-mixed-port` flag — the mixed port always comes from the
  config file).
- The app polls `/configs` and `/proxies` every 2 s and streams `/traffic`
  continuously; state is coalesced and handed to the UI on the main thread.

## Layout

```
main.go              app lifecycle, tray, panel window, core start/stop
panel.go             uix panel view (Overview / Proxies / Settings)
settings.go          editable settings, persistence (app.json)
scripts/fetch-cores.sh  pre-download the cores for both archs
core/
  core.go            Manager: copy the bundled core, spawn/stop mihomo
  config.go          runtime config.yaml generation
  client.go          REST client for the external controller
  controller.go      state machine, polling + traffic loops
resources/
  darwin-arm64/mihomo   downloaded by fetch-cores.sh (arm64)
  darwin-amd64/mihomo   downloaded by fetch-cores.sh (x86_64)
```