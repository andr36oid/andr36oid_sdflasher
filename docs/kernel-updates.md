# Kernel releases

The flasher checks `https://github.com/andr36oid/kernel-releases/raw/main/kernels.json`.
This is release metadata, like the hotfix index; assets are downloaded from GitHub
releases in **andr36oid/kernel-releases**. No GitHub API credentials are required.
The index and its release assets must be published separately by the kernel maintainer.

## Index (format 1)

```json
{
  "format": 1,
  "base_url": "https://github.com/andr36oid/kernel-releases/releases/download/kernel-updates/",
  "kernels": [
    {
      "version": "v1.1.0",
      "title": "Btrfs support",
      "description": "Adds btrfs support while retaining the existing vendor module ABI.",
      "file": "kernel-v1.1.0.zip",
      "size": 12345678,
      "sha256": "<64 hex characters: SHA-256 of the complete ZIP>",
      "compatible_builds": [
        "v2026-10-22-release|eng.kenny.20261022.1200"
      ]
    }
  ]
}
```

`base_url` must be HTTPS on github.com, have the shown repository and
`/releases/download/<tag>/` path, and end in `/`. Queries, fragments, credentials,
and nested paths are rejected. Each filename must match
`[a-zA-Z0-9][a-zA-Z0-9._-]*.zip` and be at most 255 bytes. ZIP sizes are exact,
positive bytes, at most 128 MiB. Digests are 64 hex characters without a prefix.
Version and filename duplicates are rejected. Unknown JSON fields are ignored;
unknown format versions are refused. The index response is capped at 2 MiB.
All entries in an index share the base release tag; that tag can hold several assets.

Versions are Go semver values, with an optional leading `v` (for example `1.1.0`
or `4.4.194-btrfs.1`). They order **kernel release builds**, independently of the
Linux version/vermagic. Increment a patch or prerelease number for a changed
kernel. Changing only `+build` metadata does not make a version newer.

Compatibility is an exact allowlist of BOOT build identities:
`ro.andr36oid.version|ro.product.build.version.incremental` from
`andr36oid-build.txt`. There are no globs, ranges, or implicit compatibility across
builds. Both properties must be present, with `format=1`. The maintainer must
ensure that a compatible kernel keeps the card's vendor module ABI and supports
the DTBs shipped for those builds; the flasher cannot prove that from vermagic.

The flasher selects the highest compatible version newer than the card's version.
It reads the card version from `kernel-applied.txt` first, or an optional
`kernel_version=v1.0.0` line in `andr36oid-build.txt`. Image release versions and
Linux version strings are not used as kernel release versions. On older cards
without a reliable version, the highest explicitly compatible entry is offered.

## ZIP contents

All entries are regular files at the archive root, with exactly these names:

```text
kernel-v1.1.0.zip
├── manifest.json                   required
├── Image                           required, ARM64 kernel
├── rg351mp-kernel.dtb               optional example
├── rg351mp-uboot.dtb                optional example
└── boot.ini                        optional
```

Optional DTB filenames must match `[a-zA-Z0-9][a-zA-Z0-9_-]*-(kernel|uboot).dtb`
(at most 255 bytes). No directories, absolute paths, symlinks, duplicate names
(case insensitive), or extra files are allowed. In particular, do not include
`boot.ini-android`, the selected Android DTB, profile folders, or a marker file.

`manifest.json` lists every payload file exactly once:

```json
{
  "format": 1,
  "version": "v1.1.0",
  "compatible_builds": ["v2026-10-22-release|eng.kenny.20261022.1200"],
  "files": [
    {"name": "Image", "size": 23456789, "sha256": "<SHA-256 of Image>"},
    {"name": "rg351mp-kernel.dtb", "size": 12345, "sha256": "<SHA-256 of this DTB>"},
    {"name": "rg351mp-uboot.dtb", "size": 12345, "sha256": "<SHA-256 of this DTB>"},
    {"name": "boot.ini", "size": 1234, "sha256": "<SHA-256 of boot.ini>"}
  ]
}
```

The version must match the offered release, and the installed build must appear
in both compatibility lists. Size and SHA-256 cover each uncompressed file.
Limits: 64 payload files, 64 KiB manifest, 96 MiB Image, 4 MiB per DTB, 128 KiB
boot.ini, and 128 MiB total uncompressed payload. Payloads cannot be empty.
Omit optional files from both the ZIP and the manifest when they are unchanged.

Prefer an Image-only release. If supplying boot.ini, it must retain the same
ordered `load mmc ... <selected>.dtb` targets as the card's script. If the flasher
cannot establish that the DTB selection is unchanged, it refuses the update before
writing. Other boot.ini changes are installed as supplied, so only include it when
needed and compatible with every listed build.

## Applying and retrying

The UI offers a standalone **Check for kernel update** action and checks after a
successful flash, before the existing hotfix offer. Apply appears only for a
compatible available update. Skipping or continuing advances to hotfixes after a
flash; a failed check can be retried or skipped.

The privileged helper verifies the download and manifest, revalidates the card,
then checks its build and kernel version again. It stages a copy of BOOT, writes
only listed files with truncation, and reads each back. Only changed aligned BOOT
sectors are committed. The partition table and other partitions are untouched.
All payload files are read back from the raw device after syncing.

Only after that verification does it write and verify BOOT/kernel-applied.txt:

```json
{"format":1,"build":"v2026-10-22-release|eng.kenny.20261022.1200","version":"v1.1.0"}
```

The marker belongs to that exact installed build and prevents repeat offers. It
is not carried across a full image update. A damaged marker is reported as an
error; a marker for another build is ignored. Reapplying the same bundle is safe;
downgrades and stale offers are refused. Other BOOT files, including
boot.ini-android, selected Android DTB, user files and .noroms, are preserved.

Keep the card attached until verification finishes. Like existing BOOT writes,
raw FAT-sector commits are not atomic against power loss or physical removal.
A failed write can be retried; staged files are removed when the helper finishes.
