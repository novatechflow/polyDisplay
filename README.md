# polyDisplay

Copyright 2026ff novatechflow (Alexander Alten). Licensed under PolyForm Shield 1.0.0. See `LICENSE`.

A LAN **dashboard for agentic binary trading** on Polymarket: the live book
(positions, PnL) plus charts for a mix of trading and monitoring assets. The
watchlist is not hardcoded — `server.go` reads `POLYDISPLAY_ASSETS` from the
process env or `.env` (see `.env.example`). The display is a split-screen
**web app** for any browser (phones, tablets, desktops). A small Go server on
the internal network serves the page and does all outbound API work. The
browser never talks to Polymarket, Binance, or CoinGecko itself.

- **Left** — live Polymarket positions for a wallet (PnL, size, prices).
- **Right** — watchlist prices and candlestick charts (trading + monitoring).

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
| `config.json` | Wallet, candle range, port; optional watchlist override after ⚙ save. |
| `.env.example` | Template for `POLYDISPLAY_ASSETS` (copy to `.env`). |
| `install.sh` | Checks/downloads Go, builds, asks for the Polymarket address, installs a login service (macOS, Linux). |
| `install-macos.sh` | Wrapper that runs `install.sh`. |
| `com.novatechflow.polydisplay.plist` | LaunchAgent template (filled in by the installer on macOS). |
| `LICENSE` | PolyForm Shield 1.0.0. |

## Run it

**Quick dev run:**
```bash
go run .
```

**Install as an auto-starting background service (recommended):**
```bash
./install.sh
```
Works on macOS and Linux. If `go` is missing (or older than `go.mod`), the
script downloads an official toolchain into `.go/` in this directory. It then
asks for your Polymarket **public** address (`0x…`), writes it to
`config.json`, builds, and installs a login service:

- **macOS** — LaunchAgent (`RunAtLoad` + `KeepAlive`), ad-hoc codesign, firewall exception if the firewall is on.
- **Linux** — systemd user unit (`~/.config/systemd/user/polydisplay.service`).

Non-interactive: `POLYMARKET_WALLET=0x… ./install.sh`.

`./install-macos.sh` still works; it calls `install.sh`.

**On reboot (macOS):** a LaunchAgent starts at *login*, so for an unattended
reboot enable **automatic login** (System Settings → Users & Groups). The
ad-hoc codesign + firewall exception mean no "accept incoming connections?"
dialog on startup. (For pre-login start you'd need a LaunchDaemon, which
requires `sudo`.)

Watchlist: `.env.example` is the four Polymarket-traded names (BTC, ETH, SOL,
XRP). `install.sh` copies that to `.env`, then asks for extra tickers. Each
extra is looked up on **CoinGecko** (id + name). If Binance has `SYMUSDT`, the
live server uses Binance for price/candles; if not (FLR, some memecoins, …),
it already falls back to CoinGecko — you do not need a Binance pair. Format
`SYM:Name:coingecko-id`. Restart after editing `.env`. If ⚙ has saved a
watchlist into `config.json`, that list wins until you remove `coins` from
the file.

Non-interactive extras: `POLYDISPLAY_EXTRA_ASSETS=FLR,HBAR,POND ./install.sh`.

Optional: for higher CoinGecko limits, set `CG_DEMO_KEY` in `.env` or the plist.

## Refreshing

The page is always fetched fresh from the server, so it can't get stuck on an
old build. To force a reload: **pull down** at the top of either column
(touch), or open **⚙ → Reload · clear cache**, or use the browser's reload.

## Open it on the LAN

1. Find the host's LAN IP, e.g. `ipconfig getifaddr en0` (macOS) → `192.168.4.201`.
2. On any pad or browser, open `http://192.168.4.201:8080`.

## Add it to the pad as a web app

The page is built to run fullscreen from a home-screen / dock icon (no browser
chrome). Open it in the pad's browser first, then pin it:

**iPad (Safari)**
1. Tap **Share** (square with an arrow).
2. Tap **Add to Home Screen**, or **Add to Dock** if the sheet offers it.
3. Name it `polyDisplay` → **Add**.
4. Open it from that icon — it launches fullscreen, not as a Safari tab.
5. If it landed on the home screen, hold the icon and drag it onto the **Dock**.

**Android pad (Chrome)**
1. Tap **⋮** (top right).
2. Tap **Add to Home screen** or **Install app**.
3. Confirm. Open from the new icon.

On a plain `http://` LAN address, Android usually pins a shortcut (still the
same page). iPad Safari still opens it as a standalone web app.

Leave the pad on a charger and turn **Auto-Lock** off if it should stay up.
On iPad, Guided Access (Settings → Accessibility) can lock the device to that
icon.

## Configure

The installer stores the Polymarket public address and seeds `.env` with BTC,
ETH, SOL, and XRP. It then asks for extra assets to chart and resolves them
through CoinGecko. Open **⚙** later to change the wallet, candle range
(1/7/14/30d), or add/remove tokens via the same search. Saving writes
`config.json` on the server and reloads.

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
