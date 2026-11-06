#!/usr/bin/env python3
"""Check a Windows portable zip before it is published.

Fails unless the zip holds the flasher, the helper, the pinned Mesa llvmpipe
opengl32.dll (hash and size from third_party/mesa/manifest.json) and the Mesa
and LLVM licence notices. Prints the member listing so the build log shows
what shipped.

Usage: python3 -I scripts/check-portable-zip.py dist/andr36oid-sdflasher-<ver>-windows-<arch>-portable.zip <amd64|arm64>
"""
import hashlib
import json
import pathlib
import sys
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent.parent


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    zip_path, arch = sys.argv[1], sys.argv[2]
    manifest = json.loads((ROOT / 'third_party' / 'mesa' / 'manifest.json').read_text(encoding='utf-8'))
    entry = manifest['arch'][arch]
    problems = []
    with zipfile.ZipFile(zip_path) as z:
        names = z.namelist()
        print('%s:' % zip_path)
        for info in z.infolist():
            print('  %10d  %s' % (info.file_size, info.filename))
        for required in ['andr36oid-sdflasher.exe', 'andr36oid-sdflasher-helper.exe', entry['member']]:
            if required not in names:
                problems.append('missing ' + required)
        for lic in manifest['license_files']:
            if 'licenses/' + lic not in names:
                problems.append('missing licenses/' + lic)
        if entry['member'] in names:
            h = hashlib.sha256()
            size = 0
            with z.open(entry['member']) as f:
                for chunk in iter(lambda: f.read(1 << 20), b''):
                    h.update(chunk)
                    size += len(chunk)
            if h.hexdigest() != entry['sha256'] or size != entry['size']:
                problems.append('%s does not match manifest (sha256 %s, %d bytes)' % (entry['member'], h.hexdigest(), size))
            else:
                print('  %s: Mesa %s llvmpipe for %s, sha256 %s OK' % (entry['member'], manifest['version'], arch, entry['sha256']))
    if problems:
        sys.exit('check-portable-zip: ' + '; '.join(problems))
    print('check-portable-zip: OK')


if __name__ == '__main__':
    main()
