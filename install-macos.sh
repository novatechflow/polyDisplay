#!/usr/bin/env bash
# Build the polyDisplay server and install it as a macOS LaunchAgent so it
# starts automatically at login and restarts if it ever crashes.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LABEL="com.novatechflow.polydisplay"
BIN="$DIR/polydisplayd"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

echo "==> Building server..."
( cd "$DIR" && go build -o "$BIN" . )

echo "==> Writing LaunchAgent to $PLIST"
mkdir -p "$HOME/Library/LaunchAgents"
sed -e "s#__BIN__#$BIN#g" -e "s#__DIR__#$DIR#g" \
    "$DIR/com.novatechflow.polydisplay.plist" > "$PLIST"

echo "==> (Re)loading service"
launchctl unload "$PLIST" 2>/dev/null || true
launchctl load "$PLIST"

IP="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo '<mac-ip>')"
PORT="$(python3 -c 'import json;print(json.load(open("'"$DIR"'/config.json")).get("port",8080))' 2>/dev/null || echo 8080)"
echo
echo "polyDisplay is running and will auto-start at login."
echo "  On the iPad, open:  http://$IP:$PORT"
echo "  Logs:               $DIR/polydisplay.log"
echo "  Stop:               launchctl unload $PLIST"
echo "  Update after edits:  re-run ./install-macos.sh"
