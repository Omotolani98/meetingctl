#!/usr/bin/env bash
# meetingctl per-user installer (macOS / Linux)
set -euo pipefail

REPO="${MEETINGCTL_REPO:-Omotolani98/meetingctl}"
VERSION="${MEETINGCTL_VERSION:-latest}"
PREFIX="${MEETINGCTL_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
DATA_DIR="${MEETINGCTL_DATA_DIR:-$HOME/.meetingctl}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

echo "==> installing meetingctl into $BIN_DIR"
mkdir -p "$BIN_DIR" "$DATA_DIR"
chmod 700 "$DATA_DIR"

if command -v go >/dev/null 2>&1; then
  ROOT="$(cd "$(dirname "$0")/.." && pwd)"
  if [[ -f "$ROOT/go.mod" && "${MEETINGCTL_INSTALL_FROM_SOURCE:-1}" == "1" ]]; then
    echo "==> building from source"
    (cd "$ROOT" && go build -o "$BIN_DIR/meetingctl" ./cmd/meetingctl)
    (cd "$ROOT" && go build -o "$BIN_DIR/meetingd" ./cmd/meetingd)
    (cd "$ROOT" && go build -o "$BIN_DIR/meeting-mcp" ./cmd/meeting-mcp)
  else
    echo "==> installing from module source"
    GOBIN="$BIN_DIR" go install "github.com/${REPO}/cmd/meetingctl@${VERSION}"
    GOBIN="$BIN_DIR" go install "github.com/${REPO}/cmd/meetingd@${VERSION}"
    GOBIN="$BIN_DIR" go install "github.com/${REPO}/cmd/meeting-mcp@${VERSION}"
  fi
else
  echo "go is required to install meetingctl" >&2
  exit 1
fi

# encryption key
if [[ -z "${MEETINGCTL_ENCRYPTION_KEY:-}" ]]; then
  if [[ ! -f "$DATA_DIR/encryption.key" ]]; then
    KEY="$("$BIN_DIR/meetingctl" keygen)"
    printf '%s\n' "$KEY" >"$DATA_DIR/encryption.key"
    chmod 600 "$DATA_DIR/encryption.key"
    echo "==> generated encryption key at $DATA_DIR/encryption.key"
  fi
  export MEETINGCTL_ENCRYPTION_KEY="$(tr -d '\n' <"$DATA_DIR/encryption.key")"
fi

# generate control token by booting config path
export MEETINGCTL_DATA_DIR="$DATA_DIR"
export MEETINGCTL_ENCRYPTION_KEY="${MEETINGCTL_ENCRYPTION_KEY:-$(tr -d '\n' <"$DATA_DIR/encryption.key")}"

install_launchagent_macos() {
  local plist="$HOME/Library/LaunchAgents/com.meetingctl.meetingd.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.meetingctl.meetingd</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/meetingd</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MEETINGCTL_DATA_DIR</key><string>$DATA_DIR</string>
    <key>MEETINGCTL_ENCRYPTION_KEY</key><string>$MEETINGCTL_ENCRYPTION_KEY</string>
  </dict>
  <key>StandardOutPath</key><string>$DATA_DIR/meetingd.log</string>
  <key>StandardErrorPath</key><string>$DATA_DIR/meetingd.err.log</string>
</dict>
</plist>
EOF
  launchctl bootout "gui/$(id -u)/com.meetingctl.meetingd" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null || launchctl load -w "$plist"
  echo "==> LaunchAgent installed: $plist"
}

install_systemd_user() {
  local unit_dir="$HOME/.config/systemd/user"
  mkdir -p "$unit_dir"
  cat >"$unit_dir/meetingd.service" <<EOF
[Unit]
Description=meetingctl meeting daemon
After=default.target

[Service]
Type=simple
ExecStart=$BIN_DIR/meetingd
Restart=on-failure
Environment=MEETINGCTL_DATA_DIR=$DATA_DIR
Environment=MEETINGCTL_ENCRYPTION_KEY=$MEETINGCTL_ENCRYPTION_KEY

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now meetingd.service
  echo "==> systemd user unit enabled: meetingd.service"
}

case "$OS" in
  darwin) install_launchagent_macos ;;
  linux)
    if command -v systemctl >/dev/null 2>&1; then
      install_systemd_user
    else
      echo "warn: systemctl not found; start manually: $BIN_DIR/meetingd" >&2
      nohup "$BIN_DIR/meetingd" >"$DATA_DIR/meetingd.log" 2>&1 &
    fi
    ;;
  *) echo "unsupported OS: $OS" >&2; exit 1 ;;
esac

echo "==> waiting for meetingd..."
for i in $(seq 1 20); do
  if MEETINGCTL_DATA_DIR="$DATA_DIR" MEETINGCTL_ENCRYPTION_KEY="$MEETINGCTL_ENCRYPTION_KEY" "$BIN_DIR/meetingctl" doctor >/dev/null 2>&1; then
    echo "==> meetingd is healthy"
    echo "Next:"
    echo "  export MEETINGCTL_DATA_DIR=$DATA_DIR"
    echo "  export MEETINGCTL_ENCRYPTION_KEY=\$(cat $DATA_DIR/encryption.key)"
    echo "  meetingctl doctor"
    echo "  meetingctl start --title 'My Meeting' --source fixture --input /path/to/fixture"
    exit 0
  fi
  sleep 0.5
done
echo "warn: meetingd not healthy yet; check $DATA_DIR/meetingd.err.log" >&2
exit 0
