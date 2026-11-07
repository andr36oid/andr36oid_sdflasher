#!/bin/bash
# migrate-console-state.sh — move per-install state from the test console to a
# freshly built+flashed card. Run AFTER the new card has booted once with
# adb-on-TCP reachable (the boot service opens adb automatically).
#
# What it migrates:
#   - tailscale authentication (ts/state/tailscaled.state) — no re-login
#   - (optional) full ts state incl. caches
#
# Usage:
#   migrate-console-state.sh --adb <ip>            # from dist/essentials.tgz
#   migrate-console-state.sh --adb <ip> --full     # full ts state (bigger)
#   migrate-console-state.sh --file <kit.tgz> --adb <ip>
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
DIST="$HERE/../dist"

ADB_IP=""; KIT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --adb) ADB_IP="$2"; shift 2 ;;
    --full) KIT="$DIST/state-kit.tgz"; shift ;;
    --file) KIT="$2"; shift 2 ;;
    *) echo "unknown arg $1"; exit 1 ;;
  esac
done
[ -n "$ADB_IP" ] || { echo "usage: $0 --adb <ip> [--full] [--file kit.tgz]"; exit 1; }
[ -n "$KIT" ] || KIT="$DIST/essentials.tgz"
[ -f "$KIT" ] || { echo "kit missing: $KIT (run the console-side backup first)"; exit 1; }

adb connect "$ADB_IP:5555" >/dev/null
ST=$(adb devices | grep "$ADB_IP" | awk '{print $2}')
[ "$ST" = "device" ] || { echo "device not online ($ST)"; exit 1; }

echo "pushing $KIT"
adb push "$KIT" /data/local/tmp/migrate.tgz >/dev/null
adb shell 'cat > /data/local/tmp/cmd.in << "EOF"
{
mkdir -p /data/local/tmp/ts/state
tar xzf /data/local/tmp/migrate.tgz -C /data/local/tmp
rm -f /data/local/tmp/migrate.tgz
pkill -f tailscaled 2>/dev/null; sleep 1
nohup /data/local/tmp/ts/tailscaled --statedir=/data/local/tmp/ts/state --socket=/data/local/tmp/ts/tailscaled.sock --tun=tun0 > /data/local/tmp/ts/tsd.log 2>&1 &
sleep 4
/data/local/tmp/ts/tailscale --socket=/data/local/tmp/ts/tailscaled.sock ip -4
} > /data/local/tmp/cmd.out 2>&1
chmod 666 /data/local/tmp/cmd.out
EOF
echo pushed'
sleep 10
adb shell 'cat /data/local/tmp/cmd.out'
echo
echo "If a 100.x.y.z IP printed above, tailscale migrated — no re-login needed."
