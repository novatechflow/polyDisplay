# polyDisplay

Copyright 2026ff novatechflow (Alexander Alten). Licensed under PolyForm Shield 1.0.0. See `LICENSE`.

A split-screen **web app** for Polymarket positions and a crypto watchlist. It
runs in any browser — phones, tablets, desktops. Intended for **internal
network** use: a small Go server on the LAN serves the page and does all
outbound API work. The browser never talks to Polymarket, Binance, or
CoinGecko itself.

- **Left** — live Polymarket positions for a wallet (PnL, size, prices).
- **Right** — crypto watchlist with live prices and candlestick charts.

No native app, no App Store, no AppCache. The page is a normal document with
`Cache-Control: no-cache`, so a reload always gets the current build. Colors
follow the device: dark stays on the current palette, light inverts it.

```
 ┌────────────┐   LAN http    ┌─────────────────────────────┐   https
 │  any pad / │ ────────────► │  polydisplayd (Go)          │ ──► Polymarket
 │  browser   │  /api/state   │   • polls + caches          │ ──► Binance
 │            │ ◄──────────── │   • serves the web page     │ ──► CoinGecko
 └────────────┘   one JSON    │   • /api/config, /api/search│
                              └─────────────────────────────┘
```

## Why a server (and the tradeoff)

Hitting those APIs from every display hits CoinGecko's free rate limit and
Binance's geoblock. The server fetches once, caches, and spaces requests, so
every client stays fast. **Tradeoff:** the display needs this host awake on the
LAN. The binary cross-compiles for a Raspberry Pi unchanged
(`GOOS=linux GOARCH=arm64 go build`) if you want a dedicated always-on box.

## Files

| File | Purpose |
|------|---------|
| `server.go` | The aggregator + static web server + config/search API. |
| `index.html` | The web app (consumes `/api/state`). Always loaded fresh from the server. |
| `config.json` | Wallet, watchlist, candle range, port. Editable in-app or by hand. |
| `install-macos.sh` | Builds + ad-hoc codesigns the binary, adds a firewall exception if needed, installs the launchd auto-start service. |
| `com.novatechflow.polydisplay.plist` | LaunchAgent template (filled in by the installer). |
| `LICENSE` | PolyForm Shield 1.0.0. |

## Run it

**Quick dev run:**
```bash
go run .
```

**Install as an auto-starting background service (recommended):**
```bash
./install-macos.sh
```
This builds + ad-hoc codesigns `polydisplayd`, adds a macOS firewall exception
if the firewall is on, installs a LaunchAgent (`RunAtLoad` + `KeepAlive`), and
prints the URL. It restarts if it crashes.

**On reboot:** a LaunchAgent starts at *login*, so for an unattended reboot
enable **automatic login** (System Settings → Users & Groups). The ad-hoc
codesign + firewall exception mean no "accept incoming connections?" dialog on
startup. (For pre-login start you'd need a LaunchDaemon, which requires `sudo`.)

Optional: for higher CoinGecko limits, paste a free demo key into the
`CG_DEMO_KEY` value in the plist (or `export CG_DEMO_KEY=...` for `go run`).

## Refreshing

The page is always fetched fresh from the server, so it can't get stuck on an
old build. To force a reload: **pull down** at the top of either column
(touch), or open **⚙ → Reload · clear cache**, or use the browser's reload.

## Open it on the LAN

1. Find the host's LAN IP, e.g. `ipconfig getifaddr en0` (macOS) → `192.168.4.201`.
2. On any pad or browser, open `http://192.168.4.201:8080`.

For a tablet kiosk: add the page to the home screen if the browser supports it,
keep Auto-Lock off, and leave it on a charger. On iOS, Guided Access (Settings →
Accessibility) can lock the device to that screen.

## Configure assets

A fresh install has no wallet. Open **⚙**, paste a Polymarket address
(`0x…`), pick the candle range (1/7/14/30d), and add/remove tokens via live
search. Saving writes `config.json` on the server and reloads. (You can also
edit `config.json` directly and restart the service.)

## Data sources & notes

- Candles: **Binance klines** when reachable, else **CoinGecko OHLC** — each
  chart is tagged with the source it's actually using.
- Prices: Binance ticker when the pair exists, else a CoinGecko price cached
  on the slow loop.
- Positions: Polymarket `data-api`. (No price lines on the Polymarket side.)
- Endpoints: `GET /api/state`, `GET|POST /api/config`, `GET /api/search?q=`.

## License

Copyright 2026ff novatechflow (Alexander Alten).

polyDisplay is licensed under the [PolyForm Shield License 1.0.0](https://polyformproject.org/licenses/shield/1.0.0). You may run, change, and share it, including for internal use. You may not provide a product that competes with it — including a paid or hosted service that is a practical substitute. Full terms are in `LICENSE`.
