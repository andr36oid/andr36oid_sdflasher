#!/usr/bin/env python3
import json
import pathlib
import shutil
import subprocess

out = pathlib.Path('build/licenses')
out.mkdir(parents=True, exist_ok=True)
shutil.copyfile('LICENSE', out / 'andr36oid-GPL-3.0.txt')
raw = subprocess.check_output(['go', 'list', '-m', '-json', 'all'], text=True)
decoder = json.JSONDecoder()
while raw.strip():
    module, end = decoder.raw_decode(raw.lstrip())
    raw = raw.lstrip()[end:]
    if 'Dir' not in module or module.get('Main'):
        continue
    root = pathlib.Path(module['Dir'])
    for p in root.iterdir():
        if p.is_file() and p.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE')):
            name = module['Path'].replace('/', '_') + '-' + p.name
            shutil.copyfile(p, out / name)

for directory in ['internal/ui/flags', 'internal/ui/fonts']:
    for p in pathlib.Path(directory).iterdir():
        if p.name in ['LICENSE', 'NOTICE'] or p.name.endswith('-OFL.txt'):
            shutil.copyfile(p, out / (p.parent.name + '-' + p.name))

# Windows builds ship Mesa's llvmpipe opengl32.dll (third-party, MIT + LLVM
# Apache-2.0 with LLVM Exceptions) beside the exe; keep its notices with it.
if subprocess.check_output(['go', 'env', 'GOOS'], text=True).strip() == 'windows':
    mesa = pathlib.Path('third_party/mesa')
    for name in json.loads((mesa / 'manifest.json').read_text(encoding='utf-8'))['license_files']:
        shutil.copyfile(mesa / name, out / name)
