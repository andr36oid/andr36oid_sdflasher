# Bundled software OpenGL for Windows (Mesa llvmpipe)

The graphical flasher is a Fyne app. Fyne draws with OpenGL through GLFW, which
on Windows means WGL from `opengl32.dll`. A fresh Windows 11 install with only
the Microsoft Basic Display Adapter, a VM without GPU passthrough, or an RDP
session has no OpenGL ICD at all, so GLFW fails with "WGL: The driver does not
appear to support OpenGL" and the app never shows a window
(andr36oid/issue_tracker#11).

The fix is the usual one for OpenGL apps on Windows: ship Mesa's software
renderer (llvmpipe) as an `opengl32.dll` next to the .exe. Windows resolves a
DLL imported by name from the application directory before `System32`
(standard search order; `opengl32.dll` is not in the KnownDLLs list), and the
flasher imports `OPENGL32.dll` statically (go-gl links `-lopengl32`) and GLFW
also calls `LoadLibraryA("opengl32.dll")`, so the bundled copy is what gets
loaded. No registry change, no installer step, no code change.

## What exactly is bundled

`manifest.json` pins one archive per Windows architecture we build
(`windows/amd64`, `windows/arm64`), its SHA-256, and the SHA-256 and size of
the single `opengl32.dll` member we extract from it. Nothing else from the
archive is used.

- Source: https://github.com/mmozeiko/build-mesa - automated, reproducible
  builds of upstream Mesa for Windows, statically linked (CRT and LLVM
  included, no vcruntime or Vulkan runtime needed). The widely used
  mesa-dist-win project points ARM64 users to these builds.
- Flavour: `llvmpipe` only. mesa-dist-win's own x64 package is a loader
  (`opengl32.dll`) plus a megadriver (`libgallium_wgl.dll`) that tries GLon12
  and zink before llvmpipe and needs `dxil.dll`; driver choice would then
  depend on the host's D3D12/Vulkan state (and 26.2.6 shipped with a GLon12
  regression that broke OpenGL on MSVC builds). A llvmpipe-only DLL behaves the
  same on every machine, which is what a flasher wants.
- Version: Mesa 26.2.5 (LLVM 23.1.3). Chosen as the newest release without a
  known issue at the time of pinning.

## How it gets into the release

- `scripts/fetch-mesa.py --arch <amd64|arm64> --out build` downloads the
  pinned archive into a fresh temporary directory, checks the archive hash,
  extracts only `opengl32.dll` with 7-Zip, checks the DLL hash and size, and
  copies it to `build/opengl32.dll`. Any mismatch is fatal.
- `scripts/build.sh` runs that on Windows builds, so `build/` holds the exe,
  the helper and `opengl32.dll` together.
- `.github/workflows/build.yml` zips `opengl32.dll` into the Windows portable
  zip and `scripts/check-portable-zip.py` fails the build if the zip is
  missing the DLL, if its hash does not match the manifest, or if the licence
  notices are absent. The portable zip is the only Windows artifact (there is
  no installer).
- `scripts/licenses.py` copies `LICENSE-Mesa.txt` and `LICENSE-LLVM.txt` into
  `build/licenses/` on Windows builds.

Size: the DLL is about 56-59 MB uncompressed and 20-22 MB inside the zip.

## Forcing or inspecting the renderer

With the llvmpipe-only build there is nothing to choose, but the usual Mesa
environment variables still work from the shell that launches the flasher:
`GALLIUM_DRIVER=llvmpipe`, `LIBGL_ALWAYS_SOFTWARE=1`, `MESA_DEBUG=1`,
`LP_NUM_THREADS=<n>`. To use the GPU vendor's OpenGL instead, delete
`opengl32.dll` from the flasher folder. If the GUI still cannot start, the
launcher falls back to text mode; `andr36oid-sdflasher.exe -cli` selects text
mode directly.

## Licensing

`opengl32.dll` is third-party software, not part of andr36oid-sdflasher:

- Mesa 3D: MIT licence (core library; individual components carry their own
  permissive licences, see `LICENSE-Mesa.txt`).
- LLVM (statically linked into llvmpipe): Apache License 2.0 with LLVM
  Exceptions, see `LICENSE-LLVM.txt`.

Both are redistributable in binary form with the notices kept. The notices are
shipped in `licenses/` inside the Windows zip.

## Bumping

1. Pick a release on https://github.com/mmozeiko/build-mesa/releases and read
   the matching mesa-dist-win release notes for known issues.
2. Download the x64 and arm64 `mesa-llvmpipe-*.7z` into an empty directory,
   `sha256sum` them, extract `opengl32.dll`, `sha256sum` and `stat -c %s` it.
3. Update `manifest.json` (version, llvm, url, archive_sha256, sha256, size).
4. Run `python3 -I scripts/fetch-mesa.py --arch amd64 --out /tmp/x` and the
   same for arm64; both must succeed.
