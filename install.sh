#!/usr/bin/env bash
# Copyright 2026ff novatechflow (Alexander Alten)
# SPDX-License-Identifier: PolyForm-Shield-1.0.0
# Check/install Go, build polyDisplay, write the Polymarket wallet, and
# install a login service on macOS or Linux.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LABEL="com.novatechflow.polydisplay"
GO_MIN="$(awk '/^go /{print $2; exit}' "$DIR/go.mod")"
GO_MIN="${GO_MIN:-1.26}"

die() { echo "error: $*" >&2; exit 1; }

py() {
  if command -v python3 >/dev/null 2>&1; then python3 "$@"
  elif command -v python >/dev/null 2>&1; then python "$@"
  else die "python3 is required to write config.json"
  fi
}

os_arch() {
  case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux) OS=linux ;;
    *) die "unsupported OS: $(uname -s) (macOS and Linux only)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
  BIN="$DIR/polydisplayd"
}

ver_ge() {
  local a="$1" b="$2"
  a="${a#go}"; b="${b#go}"
  local a1 a2 b1 b2
  IFS=. read -r a1 a2 _ <<<"${a}.0.0"
  IFS=. read -r b1 b2 _ <<<"${b}.0.0"
  a1=${a1:-0}; a2=${a2:-0}; b1=${b1:-0}; b2=${b2:-0}
  [ "$a1" -gt "$b1" ] || { [ "$a1" -eq "$b1" ] && [ "$a2" -ge "$b2" ]; }
}

have_go() {
  command -v go >/dev/null 2>&1 || return 1
  local v
  v="$(go env GOVERSION 2>/dev/null || go version | awk '{print $3}')"
  ver_ge "$v" "$GO_MIN"
}

fetch_go() {
  local ver url tmp dest
  dest="$DIR/.go"
  echo "==> Go $GO_MIN+ not on PATH; downloading a toolchain into $dest"
  ver="$(curl -fsSL "https://go.dev/VERSION?m=text" 2>/dev/null | head -1 || true)"
  [ -n "$ver" ] || ver="go${GO_MIN}.0"
  url="https://go.dev/dl/${ver}.${OS}-${ARCH}.tar.gz"
  tmp="$(mktemp /tmp/go-XXXX.tgz)"
  curl -fL --progress-bar -o "$tmp" "$url"
  rm -rf "$dest"
  mkdir -p "$dest"
  tar -C "$dest" -xzf "$tmp"
  rm -f "$tmp"
  export GOROOT="$dest/go"
  export PATH="$GOROOT/bin:$PATH"
  have_go || die "downloaded Go still below $GO_MIN"
  echo "   using $(go version)"
}

ensure_go() {
  if have_go; then
    echo "==> Using $(go version)"
    return
  fi
  if [ -x "$DIR/.go/go/bin/go" ]; then
    export GOROOT="$DIR/.go/go"
    export PATH="$GOROOT/bin:$PATH"
    if have_go; then
      echo "==> Using bundled $(go version)"
      return
    fi
  fi
  command -v curl >/dev/null 2>&1 || die "curl is required to download Go"
  fetch_go
}

is_wallet() { [[ "$1" =~ ^0x[a-fA-F0-9]{40}$ ]]; }

existing_wallet() {
  py - "$DIR/config.json" <<'PY' 2>/dev/null || true
import json, sys
try:
    print(json.load(open(sys.argv[1])).get("wallet") or "")
except Exception:
    pass
PY
}

ask_wallet() {
  local current w
  current="$(existing_wallet)"
  if [ -n "${POLYMARKET_WALLET:-}" ]; then
    w="$POLYMARKET_WALLET"
  elif [ -t 0 ]; then
    if [ -n "$current" ]; then
      echo "Current Polymarket wallet: $current"
      read -r -p "Polymarket public address (0x..., empty to keep): " w
      [ -n "$w" ] || w="$current"
    else
      read -r -p "Polymarket public address (0x...): " w
    fi
  else
    w="$current"
  fi
  is_wallet "$w" || die "need a Polymarket public address 0x + 40 hex (or set POLYMARKET_WALLET)"
  WALLET="$w"
}

ensure_env() {
  if [ ! -f "$DIR/.env" ]; then
    [ -f "$DIR/.env.example" ] || die "missing .env.example"
    echo "==> Creating .env from .env.example (BTC ETH SOL XRP)"
    cp "$DIR/.env.example" "$DIR/.env"
  fi
}

# CoinGecko search for id+name; Binance ticker tells us if SYM+USDT exists.
# Prints "SYM:Name:id<TAB>binance|coingecko" or exits non-zero.
resolve_asset() {
  py - "$1" <<'PY'
import json, sys, urllib.error, urllib.parse, urllib.request

q = sys.argv[1].strip()
if not q:
    sys.exit(2)
ua = {"User-Agent": "polyDisplay-install"}

def get(url):
    req = urllib.request.Request(url, headers=ua)
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)

try:
    raw = get("https://api.coingecko.com/api/v3/search?query=" + urllib.parse.quote(q))
except Exception as e:
    print("lookup failed: %s" % e, file=sys.stderr)
    sys.exit(3)
coins = raw.get("coins") or []
if not coins:
    sys.exit(4)
ql = q.lower()
pick = None
for c in coins:
    if (c.get("symbol") or "").lower() == ql or (c.get("id") or "").lower() == ql:
        pick = c
        break
if pick is None:
    pick = coins[0]
sym = (pick.get("symbol") or q).upper()
name = pick.get("name") or sym
cid = pick.get("id") or ""
if not cid:
    sys.exit(4)
src = "coingecko"
try:
    urllib.request.urlopen(
        urllib.request.Request(
            "https://api.binance.com/api/v3/ticker/24hr?symbol=" + urllib.parse.quote(sym + "USDT"),
            headers=ua,
        ),
        timeout=10,
    )
    src = "binance"
except urllib.error.HTTPError:
    pass
except Exception:
    pass
print("%s:%s:%s\t%s" % (sym, name, cid, src))
PY
}

ask_assets() {
  local extra raw spec src line
  extra="${POLYDISPLAY_EXTRA_ASSETS:-}"
  if [ -z "$extra" ] && [ -t 0 ]; then
    echo "Watchlist starts with BTC, ETH, SOL, XRP (Polymarket)."
    echo "Extra names are looked up on CoinGecko; Binance is used when a USDT pair exists."
    read -r -p "Additional assets to chart (tickers or names, comma-separated, empty to skip): " extra
  fi
  [ -n "$extra" ] || return 0
  echo "==> Resolving extra assets..."
  local -a specs=()
  IFS=',' read -r -a raw <<<"$extra"
  for line in "${raw[@]}"; do
    line="$(echo "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [ -n "$line" ] || continue
    if spec="$(resolve_asset "$line")"; then
      src="${spec##*$'\t'}"
      spec="${spec%%$'\t'*}"
      echo "   $spec  ($src)"
      specs+=("$spec")
    else
      echo "   skip $line (not found on CoinGecko)"
    fi
  done
  if [ ${#specs[@]} -gt 0 ]; then
    py - "$DIR/.env" "${specs[@]}" <<'PY'
import re, sys
path, extras = sys.argv[1], sys.argv[2:]
try:
    text = open(path).read()
except FileNotFoundError:
    text = ""
m = re.search(r'(?m)^POLYDISPLAY_ASSETS=(.*)$', text)
cur = ""
if m:
    cur = m.group(1).strip().strip('"').strip("'")
have = set()
parts = []
for item in cur.split(","):
    item = item.strip()
    if not item:
        continue
    fields = item.split(":")
    key = (fields[2] if len(fields) >= 3 else fields[0]).strip().lower()
    if key in have:
        continue
    have.add(key)
    parts.append(item)
for spec in extras:
    fields = spec.split(":")
    key = (fields[2] if len(fields) >= 3 else fields[0]).strip().lower()
    if key in have:
        continue
    have.add(key)
    parts.append(spec)
line = 'POLYDISPLAY_ASSETS="%s"' % ",".join(parts)
if m:
    text = text[:m.start()] + line + text[m.end():]
elif text and not text.endswith("\n"):
    text += "\n" + line + "\n"
else:
    text += line + "\n"
open(path, "w").write(text)
PY
  fi
}

write_config() {
  echo "==> Writing wallet into config.json"
  py - "$DIR/config.json" "$WALLET" <<'PY'
import json, os, sys
path, wallet = sys.argv[1], sys.argv[2]
if os.path.exists(path):
    try:
        cfg = json.load(open(path))
    except Exception:
        cfg = {}
else:
    cfg = {}
cfg["wallet"] = wallet
cfg.setdefault("candleDays", 1)
cfg.setdefault("sort", "trades")
if not cfg.get("port"):
    cfg.pop("port", None)
# coins stay out of config unless already set — POLYDISPLAY_ASSETS in .env is the list
if not cfg.get("coins"):
    cfg.pop("coins", None)
with open(path, "w") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
PY
}

build() {
  echo "==> Building server..."
  ( cd "$DIR" && go build -o "$BIN" . )
  if [ "$OS" = darwin ]; then
    echo "==> Ad-hoc codesigning $BIN"
    codesign --force --sign - "$BIN" 2>/dev/null || echo "   (codesign skipped)"
  fi
}

install_macos() {
  local plist="$HOME/Library/LaunchAgents/$LABEL.plist"
  echo "==> Writing LaunchAgent to $plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  sed -e "s#__BIN__#$BIN#g" -e "s#__DIR__#$DIR#g" \
    "$DIR/com.novatechflow.polydisplay.plist" > "$plist"
  local fw=/usr/libexec/ApplicationFirewall/socketfilterfw
  if [ -x "$fw" ] && "$fw" --getglobalstate 2>/dev/null | grep -qi "enabled"; then
    echo "==> Firewall is enabled; adding an exception for polydisplayd (needs sudo)"
    sudo "$fw" --add "$BIN" >/dev/null 2>&1 || true
    sudo "$fw" --unblockapp "$BIN" >/dev/null 2>&1 || true
  fi
  echo "==> (Re)loading service"
  launchctl unload "$plist" 2>/dev/null || true
  launchctl load "$plist"
  STOP="launchctl unload $plist"
}

install_linux() {
  local unit_dir="$HOME/.config/systemd/user"
  local unit="$unit_dir/polydisplay.service"
  echo "==> Writing systemd user unit to $unit"
  mkdir -p "$unit_dir"
  cat > "$unit" <<EOF
[Unit]
Description=polyDisplay
After=network.target

[Service]
WorkingDirectory=$DIR
ExecStart=$BIN
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
EOF
  if command -v systemctl >/dev/null 2>&1 && systemctl --user list-units >/dev/null 2>&1; then
    systemctl --user daemon-reload
    systemctl --user enable --now polydisplay.service
    STOP="systemctl --user disable --now polydisplay.service"
  else
    echo "   (no systemd --user; start with: $BIN)"
    nohup "$BIN" >/dev/null 2>&1 &
    STOP="kill the polydisplayd process"
  fi
}

lan_ip() {
  case "$OS" in
    darwin) ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "<host-ip>" ;;
    linux) hostname -I 2>/dev/null | awk '{print $1}' || echo "<host-ip>" ;;
  esac
}

port() {
  if [ -n "${POLYDISPLAY_PORT:-}" ]; then echo "$POLYDISPLAY_PORT"; return; fi
  py - "$DIR/.env" "$DIR/config.json" <<'PY' 2>/dev/null || echo 8080
import os, json, sys
env_path, cfg_path = sys.argv[1], sys.argv[2]
if os.path.exists(env_path):
    for line in open(env_path):
        line = line.strip()
        if line.startswith("POLYDISPLAY_PORT="):
            v = line.split("=", 1)[1].strip().strip('"').strip("'")
            if v.isdigit() and int(v) > 0:
                print(v)
                raise SystemExit
try:
    p = json.load(open(cfg_path)).get("port") or 0
    print(int(p) if p else 8080)
except Exception:
    print(8080)
PY
}

os_arch
ensure_go
ensure_env
ask_wallet
ask_assets
write_config
build
case "$OS" in
  darwin) install_macos ;;
  linux) install_linux ;;
esac

IP="$(lan_ip)"
PORT="$(port)"
echo
echo "polyDisplay is running and will auto-start at login."
echo "  On a pad or browser: http://$IP:$PORT"
echo "  Logs:               $DIR/polydisplay.log"
echo "  Stop:               $STOP"
echo "  Update after edits:  re-run $0"
