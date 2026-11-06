#!/usr/bin/env python3
"""Fetch the pinned Mesa llvmpipe opengl32.dll for a Windows build.

Reads third_party/mesa/manifest.json, downloads the archive for --arch into a
fresh temporary directory, verifies its SHA-256, extracts only the pinned DLL
member with 7-Zip, verifies the DLL's SHA-256 and size, and copies it to --out.
Anything that does not match the manifest is fatal. The archive is treated as
untrusted data: nothing is executed from it, and it is deleted afterwards.

Usage: python3 -I scripts/fetch-mesa.py --arch amd64 --out build
       python3 -I scripts/fetch-mesa.py --arch amd64 --archive <local .7z> --out build
"""
import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
MANIFEST = ROOT / 'third_party' / 'mesa' / 'manifest.json'


def sha256(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def seven_zip():
    for name in ['7z', '7za', '7zr', '7zz']:
        p = shutil.which(name)
        if p:
            return p
    sys.exit('fetch-mesa: 7-Zip (7z) is required to extract the Mesa archive')


def download(url, dest):
    req = urllib.request.Request(url, headers={'User-Agent': 'andr36oid-sdflasher-build'})
    with urllib.request.urlopen(req, timeout=120) as r, dest.open('wb') as f:
        shutil.copyfileobj(r, f, 1 << 20)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--arch', required=True, choices=['amd64', 'arm64'])
    ap.add_argument('--out', required=True, help='directory that receives opengl32.dll')
    ap.add_argument('--archive', help='already downloaded archive (still hash-checked) instead of downloading')
    ap.add_argument('--manifest', default=str(MANIFEST))
    a = ap.parse_args()

    manifest = json.loads(pathlib.Path(a.manifest).read_text(encoding='utf-8'))
    entry = manifest['arch'][a.arch]
    out = pathlib.Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    target = out / entry['member']
    if target.is_file() and sha256(target) == entry['sha256'] and target.stat().st_size == entry['size']:
        print('fetch-mesa: %s already present and matches manifest (Mesa %s, %s)' % (target, manifest['version'], a.arch))
        return

    tmp = pathlib.Path(tempfile.mkdtemp(prefix='mesa-llvmpipe-', dir=str(out)))
    try:
        archive = tmp / pathlib.Path(entry['url']).name
        if a.archive:
            shutil.copyfile(a.archive, archive)
        else:
            print('fetch-mesa: downloading', entry['url'])
            download(entry['url'], archive)
        got = sha256(archive)
        if got != entry['archive_sha256']:
            sys.exit('fetch-mesa: archive sha256 mismatch\n  expected %s\n  got      %s' % (entry['archive_sha256'], got))
        extract = tmp / 'extract'
        extract.mkdir()
        subprocess.run([seven_zip(), 'e', '-y', '-o' + str(extract), str(archive), entry['member']],
                       check=True, stdout=subprocess.DEVNULL, cwd=str(ROOT))
        dll = extract / entry['member']
        if not dll.is_file():
            sys.exit('fetch-mesa: %s not found in archive' % entry['member'])
        got = sha256(dll)
        size = dll.stat().st_size
        if got != entry['sha256'] or size != entry['size']:
            sys.exit('fetch-mesa: %s mismatch\n  expected %s (%d bytes)\n  got      %s (%d bytes)'
                     % (entry['member'], entry['sha256'], entry['size'], got, size))
        shutil.copyfile(dll, target)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    print('fetch-mesa: wrote %s (Mesa %s llvmpipe, %s, sha256 %s)' % (target, manifest['version'], a.arch, entry['sha256']))


if __name__ == '__main__':
    main()
