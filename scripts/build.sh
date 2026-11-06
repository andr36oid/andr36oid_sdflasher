#!/usr/bin/env bash
set -euo pipefail
version=${VERSION:-development}
mkdir -p build dist
ext=
if [ "$(go env GOOS)" = windows ]; then
  ext=.exe
  arch=$(go env GOARCH)
  app_icon_resource="cmd/andr36oid-sdflasher/icon_windows_${arch}.syso"
  helper_icon_resource="cmd/andr36oid-sdflasher-helper/icon_windows_${arch}.syso"
  trap 'rm -f "$app_icon_resource" "$helper_icon_resource"' EXIT
  GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) go run github.com/tc-hib/go-winres@v0.3.3 simply --arch "$arch" --manifest none \
    --icon internal/ui/icon.png --out build/icon
  cp "build/icon_windows_${arch}.syso" "$app_icon_resource"
  cp "build/icon_windows_${arch}.syso" "$helper_icon_resource"
fi
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "build/andr36oid-sdflasher-helper$ext" ./cmd/andr36oid-sdflasher-helper
flags="-s -w -X github.com/andr36oid/andr36oid-sdflasher/internal/ui.Version=$version"
if [ "$(go env GOOS)" = windows ]; then flags="$flags -H=windowsgui -extldflags=-static"; fi
CGO_ENABLED=1 go build -trimpath -ldflags "$flags" -o "build/andr36oid-sdflasher$ext" ./cmd/andr36oid-sdflasher
if [ "$ext" = .exe ]; then
  python3 scripts/check-icons.py windows build/andr36oid-sdflasher.exe build/andr36oid-sdflasher-helper.exe
  # Software OpenGL (Mesa llvmpipe) beside the exe so the GUI starts on machines
  # without an OpenGL driver (fresh Windows 11, VMs, RDP). See third_party/mesa.
  python3 -I scripts/fetch-mesa.py --arch "$arch" --out build
fi
python3 scripts/licenses.py
