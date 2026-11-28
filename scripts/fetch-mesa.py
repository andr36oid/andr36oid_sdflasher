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
import os
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


def seven_zips():
    """Every 7-Zip on this machine, in the order to try them.

    One of them failing is not the end: the MSYS2 p7zip (17.05) that comes first on the
    Windows arm64 runner cannot decode the ARM64 filter the arm64 Mesa archive is packed
    with (that needs 7-Zip 23.01 or newer), while the MinGW 7-Zip package or the 7-Zip
    installed on the runner can. SEVENZIP names one to try first.
    """
    found, seen = [], set()

    def add(path):
        if not path:
            return
        path = str(path)
        key = os.path.realpath(path)
        if key not in seen and os.path.isfile(path) and os.access(path, os.X_OK):
            seen.add(key)
            found.append(path)

    add(os.environ.get('SEVENZIP'))
    for folder in os.environ.get('PATH', '').split(os.pathsep):
        for name in ['7zz', '7z', '7za', '7zr']:
            for ext in ['', '.exe']:
                add(os.path.join(folder, name + ext))
    for var in ['ProgramW6432', 'ProgramFiles', 'ProgramFiles(x86)']:
        if os.environ.get(var):
            add(os.path.join(os.environ[var], '7-Zip', '7z.exe'))
    add('/c/Program Files/7-Zip/7z.exe')
    return found


def extract_member(archive, member, tmp):
    """Extracts one member with the first 7-Zip that can, and returns its path.

    Paths are given relative to tmp so that an MSYS tool and a native Windows one read
    them the same way. The caller checks the result against the manifest, so which tool
    did the work does not matter.
    """
    tools = seven_zips()
    if not tools:
        sys.exit('fetch-mesa: 7-Zip (7z) is required to extract the Mesa archive')
    failures = []
    for n, tool in enumerate(tools):
        dest = tmp / ('extract-%d' % n)
        dest.mkdir()
        r = subprocess.run([tool, 'e', '-y', '-o' + dest.name, archive.name, member],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, cwd=str(tmp))
        out = dest / member
        if r.returncode == 0 and out.is_file():
            return out
        lines = [l.strip() for l in r.stdout.decode('utf-8', 'replace').splitlines() if l.strip()]
        errors = [l for l in lines if 'error' in l.lower() or 'unsupported' in l.lower()]
        reason = '; '.join((errors or lines[-2:])[:3]) or 'no output'
        failures.append('  %s: exit %d: %s' % (tool, r.returncode, reason))
        print('fetch-mesa: %s cannot extract %s (%s)' % (tool, member, reason))
    sys.exit('fetch-mesa: no 7-Zip on this machine can extract %s from %s '
             '(the arm64 archive needs 7-Zip 23.01 or newer)\n%s'
             % (member, archive.name, '\n'.join(failures)))


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
        dll = extract_member(archive, entry['member'], tmp)
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
