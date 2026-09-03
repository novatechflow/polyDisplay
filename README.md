# polyDisplay

A fullscreen split-screen kiosk dashboard for an **old iPad (iOS 10.3.3)** — no
native app, no App Store, no jailbreak.

- **Left** — live Polymarket positions for a wallet (PnL, size, prices).
- **Right** — crypto watchlist with live prices + **7-day candlestick charts**.

The iPad runs a plain fullscreen web page and talks **only to a tiny local Go
server** on your Mac. That server does all the outbound work — aggregating
Polymarket + crypto (Binance-first, CoinGecko fallback), caching it, and handing
the iPad one cheap JSON endpoint. So the iPad never hits an external API, cert,
rate limit, or geoblock. (Confirmed: iOS 10.3.3 reaches the LAN server fine.)

```
 ┌────────────┐   LAN http    ┌─────────────────────────────┐   https
 │  iPad 4    │ ────────────► │  polydisplayd (Go, on Mac)  │ ──► Polymarket
 │  Safari    │  /api/state   │   • polls + caches          │ ──► Binance
 │  fullscreen│ ◄──────────── │   • serves the web page     │ ──► CoinGecko
 └────────────┘   one JSON    │   • /api/config, /api/search│
                              └─────────────────────────────┘
```

## Why a server (and the tradeoff)

Polling 10+ assets directly from the iPad hits CoinGecko's free rate limit and
Binance's geoblock. The server fetches once, caches, and spaces requests, so the
iPad is always fast and never rate-limited. **Tradeoff:** the display needs this
Mac awake on the LAN. The binary cross-compiles for a Raspberry Pi unchanged
(`GOOS=linux GOARCH=arm64 go build`) if you later want a dedicated always-on box.

## Files

| File | Purpose |
|------|---------|
| `server.go` | The aggregator + static web server + config/search API. |
| `index.html` | The web app (consumes `/api/state`). Always loaded fresh from the server — no browser/app cache to get stuck on. |
| `config.json` | Wallet, watchlist, candle range, port. Editable in-app or by hand. |
| `install-macos.sh` | Builds + ad-hoc codesigns the binary, adds a firewall exception if needed, installs the launchd auto-start service. |
| `com.novatechflow.polydisplay.plist` | LaunchAgent template (filled in by the installer). |

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
old build. To force a reload on the iPad: **pull down** at the top of either
column, or open **⚙ → Reload · clear cache**.

## Set up the iPad as a kiosk

1. Find the Mac's LAN IP: `ipconfig getifaddr en0` (e.g. `192.168.4.201`).
2. On the iPad, open Safari → `http://192.168.4.201:8080` and let it load.
3. **Share → Add to Home Screen.** Launch from the icon — it opens fullscreen.
4. **Settings → Display & Brightness → Auto-Lock → Never**; keep it on the charger.
5. **Settings → General → Accessibility → Guided Access → On.** Open the app,
   **triple-click Home**, Start. Triple-click + passcode to exit later.

## Configure assets (from the iPad)

A fresh install has no wallet. Tap **⚙**, paste a Polymarket address
(`0x…`), pick the candle range (1/7/14/30d), and add/remove tokens via live
search. Saving writes `config.json` on the server and reloads. (You can also
edit `config.json` directly and restart the service.)

## Data sources & notes

- Candles: **Binance klines** when reachable, else **CoinGecko OHLC** — each
  chart is tagged with the source it's actually using.
- Prices: CoinGecko `simple/price` (one call, no geoblock).
- Positions: Polymarket `data-api`. (No price lines on the Polymarket side.)
- Endpoints: `GET /api/state`, `GET|POST /api/config`, `GET /api/search?q=`.
