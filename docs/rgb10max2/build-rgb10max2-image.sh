#!/bin/bash
# build-rgb10max2-image.sh — full reproducible builder for the Powkiddy
# RGB10 Max 2 andr36oid image. Takes ANY andr36oid base image (official
# release or a previous master) and applies EVERYTHING the port needs:
#
#   BOOT (p1):   RGB10MAX2 device profile (DTB, logo, u-boot/kernel dtbs),
#                profiles.txt line, boot.ini orientation + autosuspend lines
#   vendor (p8): diag.sh (current service), zz-diag.rc, rk817tool, wifitool,
#                debug.sf.nobootanimation=1 in /vendor/build.prop
#
# Nothing else is touched: /data (Wi-Fi, apps, tailscale auth) is per-install
# and migrates via migrate-console-state.sh after first boot.
#
# Usage:
#   build-rgb10max2-image.sh <base.img> [-o <output.img>]
#   build-rgb10max2-image.sh --download [-o <output.img>]   # fetch official release
#
# Idempotent: running it on an already-patched image just refreshes files.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
PROFILE_DIR="$(cd "$HERE/../device-work/boot-profile/RGB10MAX2" && pwd)"
SERVICE_DIR="$HERE/../device-work/diag-v11"
BIN_DIR="$HERE/../device-work/bin"
TOP_FILES="rk3326-r36s-android.dtb logo.bmp rg351mp-uboot.dtb rg351p-uboot.dtb rg351v-uboot.dtb rg351mp-kernel.dtb rg351p-kernel.dtb rg351v-kernel.dtb"
WORK="$(mktemp -d)"
LO=""

cleanup() {
  umount "$WORK/boot" 2>/dev/null
  umount "$WORK/vendor" 2>/dev/null
  [ -n "${VLO:-}" ] && losetup -d "$VLO" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

die() { echo "build: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "'$1' is required"; }

for f in $TOP_FILES; do [ -f "$PROFILE_DIR/$f" ] || die "profile file missing: $PROFILE_DIR/$f"
done
[ -f "$SERVICE_DIR/diag.sh" ] || die "diag.sh missing"
[ -f "$BIN_DIR/rk817tool" ] && [ -f "$BIN_DIR/wifitool" ] && [ -f "$BIN_DIR/zz-diag.rc" ] || \
  die "compiled tools missing in device-work/bin/ (rk817tool, wifitool, zz-diag.rc)"

case "${1:-}" in
  --download)
    need curl
    URL=$(curl -s https://api.github.com/repos/andr36oid/releases/releases/latest \
          | grep -oE '"browser_download_url": *"[^"]*rk3326\.img\.xz"' | head -1 | cut -d'"' -f4)
    [ -n "$URL" ] || die "no rk3326 release image found"
    echo "Downloading $URL"
    curl -L "$URL" -o "$WORK/base.img.xz"
    BASE="$WORK/base.img.xz"
    ;;
  *.img|*.img.xz) BASE="${1:?usage: $0 <base.img> [--download] [-o out.img]}"; [ -f "$BASE" ] || die "no such image: $BASE" ;;
  *) die "usage: $0 <base.img> | --download [-o out.img]" ;;
esac
OUT="${3:-}"; [ "${2:-}" = "-o" ] && OUT="${3:-}"
OUT="${OUT:-${BASE%.*}-rgb10max2-master.img}"
OUT="${OUT%.img}.img"

# --- prepare a writable copy -------------------------------------------------
need xz; need losetup; need mount; need debugfs; need mkfs.vfat; need mdir; need mcopy
if [ "${BASE##*.}" = "xz" ]; then
  echo "Decompressing base image…"
  xz -dk -c "$BASE" > "$WORK/base.img"
else
  cp --sparse=always "$BASE" "$WORK/base.img"
fi
IMG="$WORK/base.img"

# --- mount BOOT (p1) and vendor (p8) via offsets (no partition scan needed) ---
fdisk -l "$IMG" > "$WORK/fdisk.txt" || die "fdisk failed on the image"
START1=$(awk '/^'[[:space:]]*'?img1|img1/' "$WORK/fdisk.txt" | awk '{print $2}')
# robust parse: find lines with p1..p8 (sfdisk-style numbering by position)
get_start() { awk -v n="$1" '$1 ~ ("img" n "$") {print $2}' "$WORK/fdisk.txt" | head -1; }
get_size() { awk -v n="$1" '$1 ~ ("img" n "$") {print $4}' "$WORK/fdisk.txt" | head -1; }
S1=$(get_start 1); SZ8=$(get_size 8)
[ -n "$S1" ] || { grep -n "img1\b" "$WORK/fdisk.txt" | head -3; die "could not parse p1 start from fdisk output"; }
OFF1=$((S1 * 512))
mkdir -p "$WORK/boot" "$WORK/vendor"
mount -t vfat -o loop,offset=$OFF1 "$IMG" "$WORK/boot" || die "p1 vfat mount failed"
[ -f "$WORK/boot/boot.ini" ] || die "p1 has no boot.ini — not an andr36oid image?"
if [ -n "$SZ8" ]; then
  OFF8=$(get_start 8); OFF8=$((OFF8 * 512)); SZ8B=$((SZ8 * 512))
  LO8=$(losetup -f --show -o $OFF8 --sizelimit $SZ8B "$IMG")
  e2fsck -fp "$LO8" >/dev/null 2>&1 || true   # fresh images carry a dirty ext4 journal
  mount -t ext4 "$LO8" "$WORK/vendor" 2>/dev/null || { losetup -d "$LO8"; die "p8 ext4 mount failed — partition layout differs"; }
  VLO="$LO8"
else
  die "could not parse p8 from fdisk output"
fi

# --- BOOT: rebuild via mtools (kernel vfat writes proved flaky on sparse backing) ---
mkdir -p "$WORK/boot-orig"
MIDX="$IMG@@$OFF1"
# 1. pull the entire official BOOT content out (mtools = no kernel involvement)
mcopy -s -i "$MIDX" ::/ "$WORK/boot-orig"/ || die "mtools read of official BOOT failed"
[ -f "$WORK/boot-orig/boot.ini" ] || die "official BOOT extraction incomplete"

# 2. apply our changes on the local copy
mkdir -p "$WORK/boot-orig/Backup-stock"
for f in $TOP_FILES; do
  [ -f "$WORK/boot-orig/$f" ] && cp "$WORK/boot-orig/$f" "$WORK/boot-orig/Backup-stock/"
  cp "$PROFILE_DIR/$f" "$WORK/boot-orig/$f"
done
mkdir -p "$WORK/boot-orig/Devices/RGB10MAX2"
rm -rf "$WORK/boot-orig/Devices/RGB10MAX2"; mkdir -p "$WORK/boot-orig/Devices/RGB10MAX2"
for f in $TOP_FILES; do cp "$PROFILE_DIR/$f" "$WORK/boot-orig/Devices/RGB10MAX2/$f"; done
cp "$WORK/boot-orig/boot.ini" "$WORK/boot-orig/Devices/RGB10MAX2/boot.ini"
grep -q "^Devices/RGB10MAX2|" "$WORK/boot-orig/andr36oid-profiles.txt" 2>/dev/null || \
  printf 'Devices/RGB10MAX2|RGB10 Max 2|Powkiddy RGB10 Max 2|\n' >> "$WORK/boot-orig/andr36oid-profiles.txt"
grep -q "sf_orientation=ORIENTATION_270" "$WORK/boot-orig/boot.ini" || \
  sed -i 's/setenv bootargs /setenv bootargs androidboot.sf_orientation=ORIENTATION_270 usbcore.autosuspend=-1 /' "$WORK/boot-orig/boot.ini"
grep -q "ORIENTATION_270" "$WORK/boot-orig/boot.ini" || die "boot.ini orientation edit failed"

# 3. fresh vfat, everything copied back in with mtools (proven sequence)
rm -f "$WORK/boot-vfat.img"
mkfs.vfat -n BOOT -C "$WORK/boot-vfat.img" 262144   # 256 MiB in 1KB blocks
for D in Devices Experimental Image Panels Backup-stock; do mmd -i "$WORK/boot-vfat.img" "::$D"; done
mcopy -s -i "$WORK/boot-vfat.img" "$WORK/boot-orig/Devices/"* ::/Devices/
for D in Experimental Panels Backup-stock; do mcopy -s -i "$WORK/boot-vfat.img" "$WORK/boot-orig/$D/"* "::/$D/"; done
if ls "$WORK/boot-orig/Image/"* >/dev/null 2>&1; then mcopy -s -i "$WORK/boot-vfat.img" "$WORK/boot-orig/Image/"* ::/Image/; fi
mcopy -i "$WORK/boot-vfat.img" "$WORK/boot-orig/"*.txt ::/
for f in $TOP_FILES boot.ini boot.ini-android logo.bmp logo_kernel.bmp make-report.txt; do
  [ -f "$WORK/boot-orig/$f" ] && mcopy -i "$WORK/boot-vfat.img" "$WORK/boot-orig/$f" ::/
done
mdir -i "$WORK/boot-vfat.img" ::/ >/dev/null || die "fresh vfat verification failed"

# 4. splice the fresh vfat into the image at p1
dd if="$WORK/boot-vfat.img" of="$IMG" bs=512 seek=$S1 conv=notrunc status=none
grep -q "ORIENTATION_270" "$IMG" || die "boot.ini orientation did not reach the image"
grep -q "usbcore.autosuspend=-1" "$IMG" || die "boot.ini autosuspend did not reach the image"
grep -q "RGB10MAX2" "$IMG" || die "profile files did not reach the image"
mdir -i "$IMG@@$OFF1" ::/boot.ini >/dev/null 2>&1 || die "mtools post-verify failed"
echo "BOOT: rebuilt via mtools, verified."

# --- vendor: service + tools + build.prop ------------------------------------
install -m 750 "$SERVICE_DIR/diag.sh" "$WORK/vendor/etc/diag.sh"
install -m 644 "$BIN_DIR/zz-diag.rc" "$WORK/vendor/etc/init/zz-diag.rc"
install -m 755 "$BIN_DIR/rk817tool" "$WORK/vendor/etc/rk817tool"
install -m 755 "$BIN_DIR/wifitool" "$WORK/vendor/etc/wifitool"
grep -q "nobootanimation" "$WORK/vendor/build.prop" && \
  sed -i 's/^debug.sf.nobootanimation=.*/debug.sf.nobootanimation=1/' "$WORK/vendor/build.prop" || \
  echo "debug.sf.nobootanimation=1" >> "$WORK/vendor/build.prop"
grep -q "debug.sf.nobootanimation=1" "$WORK/vendor/build.prop" || die "build.prop knob failed"
echo "vendor: diag.sh + tools + build.prop installed."

sync -f "$WORK/boot" 2>/dev/null || sync
umount "$WORK/boot" || die "BOOT unmount failed (busy) — aborting instead of losing writes"
sync -f "$WORK/vendor" 2>/dev/null || sync
umount "$WORK/vendor" || die "vendor unmount failed (busy) — aborting instead of losing writes"
losetup -d "$VLO" 2>/dev/null || true

# --- verify the writes are IN THE IMAGE FILE (raw grep, no mount games) ------
grep -q "ORIENTATION_270" "$IMG" || die "boot.ini orientation did not reach the image"
grep -q "usbcore.autosuspend=-1" "$IMG" || die "boot.ini autosuspend did not reach the image"
grep -q "RGB10MAX2" "$IMG" || die "profile files did not reach the image"
grep -q "nobootanimation=1" "$IMG" || die "vendor build.prop did not reach the image"
echo "image-level verification passed."

# --- finalize -----------------------------------------------------------------
mv "$IMG" "$OUT"
sync
echo
echo "DONE: $OUT (apparent $(du -h --apparent-size "$OUT" | cut -f1))"
echo "md5:  $(md5sum "$OUT" | cut -d' ' -f1)"
echo
echo "Flash to the new card, boot once, then migrate state:"
echo "  migrate-console-state.sh --adb <console-ip>  (tailscale auth, no re-login)"
