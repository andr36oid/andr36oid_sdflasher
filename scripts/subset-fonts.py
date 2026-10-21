#!/usr/bin/env python3
# Regenerate bundled fonts from the Noto source files listed in fonts/NOTICE.
# Usage: python3 scripts/subset-fonts.py DIRECTORY (requires fontTools).
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from fontTools.subset import Options,Subsetter
from fontTools.merge import Merger
from pathlib import Path
import json,shutil,hashlib,sys
root=Path(sys.argv[1])
alltext=''.join(Path('internal/i18n/locales/'+code+'.json').read_text() for code in ['en','de','ru','uk','es','pt','pt-BR','hi','ja','ko','zh-Hans'])+'EnglishDeutschРусскийУкраїнськаEspañolPortuguêsPortuguês brasileiroहिन्दी日本語한국어简体中文'
remaining=set(map(ord,alltext))|set(range(32,256))
# General Punctuation (’ … — and so on) comes from Noto Sans too; the CJK font draws it full width.
source={'Latin':{c for c in remaining if c<0x900 or 0x2000<=c<0x2070},'Devanagari':{c for c in remaining if 0x900<=c<0xa00},'Korean':{c for c in remaining if 0xac00<=c<=0xd7af}}
source['CJK']=remaining-set.union(*source.values())
for weight,label in [(400,'Regular'),(700,'Bold')]:
    parts=[]
    for name,chars in source.items():
        f=TTFont(root/(name+'.ttf'))
        limits={'wght':weight}
        if 'wdth' in [a.axisTag for a in f['fvar'].axes]: limits['wdth']=100
        f=instantiateVariableFont(f,limits,inplace=True)
        opts=Options();opts.layout_features=['*'];opts.name_IDs=['*'];opts.name_legacy=True;opts.name_languages=['*']
        opts.drop_tables += ['BASE','vhea','vmtx']
        subset=Subsetter(options=opts);subset.populate(unicodes=chars);subset.subset(f)
        # All source families use 1000 units per em.
        assert f['head'].unitsPerEm==1000
        file=root/(name+'-'+label+'.ttf');f.save(file);parts.append(str(file))
    merged=Merger().merge(parts)
    for record in merged['name'].names:
        value={1:'SD Flasher UI',2:label,3:'SDFlasherUI-'+label,4:'SD Flasher UI '+label,6:'SDFlasherUI-'+label,16:'SD Flasher UI',17:label}.get(record.nameID)
        if value:record.string=value.encode(record.getEncoding())
    merged.save('internal/ui/fonts/SDFlasherUI-'+label+'.ttf')
for name in source:shutil.copyfile(root/(name+'-OFL.txt'),'internal/ui/fonts/'+name+'-OFL.txt')
Path('internal/ui/fonts/NOTICE').write_text('SD Flasher UI is a subset and merge of Noto Sans, Noto Sans Devanagari, Noto Sans KR and Noto Sans SC (which also draws the Japanese kana), distributed under the SIL Open Font License 1.1. Original source: https://github.com/google/fonts (ofl/notosans, ofl/notosansdevanagari, ofl/notosanskr, ofl/notosanssc). The modified font family has been renamed.\n\nSource SHA256:\n'+''.join(hashlib.sha256((root/(n+'.ttf')).read_bytes()).hexdigest()+'  '+n+'.ttf\n' for n in source))
print('Created bundled UI fonts')
