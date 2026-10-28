#!/usr/bin/env python3
"""Rebuild deterministic exports from saved raster masters. Requires ImageMagick 7."""
from pathlib import Path
import subprocess, tempfile, zipfile, xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
BG = '#111719'
def mag(*args):
    subprocess.run(['magick', *map(str, args)], check=True)
def info(path):
    return tuple(map(int, subprocess.check_output(['magick','identify','-format','%w %h',str(path)]).split()))
def ora(path, size, layers, merged):
    image = ET.Element('image', w=str(size[0]), h=str(size[1]), name=path.stem, version='0.0.3')
    stack = ET.SubElement(image, 'stack')
    with zipfile.ZipFile(path, 'w') as z:
        z.writestr('mimetype', 'image/openraster', compress_type=zipfile.ZIP_STORED)
        for i,(name,file,x,y) in enumerate(reversed(layers)):
            src=f'data/layer{i}.png'
            ET.SubElement(stack,'layer',name=name,src=src,x=str(x),y=str(y),opacity='1.0',visibility='visible', **{'composite-op':'svg:src-over'})
            z.write(file,src,compress_type=zipfile.ZIP_DEFLATED)
        z.writestr('stack.xml',ET.tostring(image,encoding='utf-8',xml_declaration=True))
        z.write(merged,'mergedimage.png')

def main():
    for name in ['icons','monochrome','previews']:
        (ROOT/name).mkdir(exist_ok=True)
    masters=ROOT/'masters'
    for n in [16,24,32,48,64,128,256,512]:
        mag(masters/'ddsonic-icon-master.png','-filter','Lanczos','-resize',f'{n}x{n}','-strip',ROOT/f'icons/ddsonic-{n}.png')
    mag(masters/'ddsonic-icon-master.png','-filter','Lanczos','-resize','1024x1024','-strip',ROOT/'ddsonic-icon.png')
    mag(*[ROOT/f'icons/ddsonic-{n}.png' for n in [16,24,32,48,64,128,256]],ROOT/'ddsonic.ico')
    mag(masters/'ddsonic-icon-monochrome-master.png','-colorspace','Gray','-resize','1024x1024','-strip',ROOT/'monochrome/ddsonic-icon-gray.png')
    mag(masters/'ddsonic-wordmark-master.png','-strip',ROOT/'ddsonic-wordmark.png')
    for name,color in [('light','#FFFFFF'),('dark','#111719')]:
        mag(masters/'ddsonic-wordmark-master.png','-fill',color,'-colorize','100','-strip',ROOT/f'monochrome/ddsonic-wordmark-{name}.png')
    with tempfile.TemporaryDirectory() as temp:
        tmp=Path(temp)
        for filename,canvas,mark_box,mark_pos,word_w,word_pos,tag_w,tag_pos in [
            ('ddsonic-logo',(1400,1400),'1080x960',(160,65),1120,(140,1070),1050,(175,1320)),
            ('ddsonic-logo-horizontal',(2400,800),'680x660',(60,70),1500,(800,250),1450,(825,575)),
        ]:
            mark,word,tag,bg=[tmp/f'{filename}-{k}.png' for k in ['mark','word','tag','bg']]
            mag(masters/'ddsonic-mark-master.png','-trim','+repage','-resize',mark_box,mark)
            mag(masters/'ddsonic-wordmark-master.png','-resize',f'{word_w}x',word)
            mag(masters/'ddsonic-tagline-master.png','-resize',f'{tag_w}x',tag)
            mag('-size',f'{canvas[0]}x{canvas[1]}',f'xc:{BG}',bg)
            layers=[('Dark background',bg,0,0),('Duck and terminal',mark,*mark_pos),('ddsonic wordmark',word,*word_pos),('Tagline',tag,*tag_pos)]
            cmd=[bg]
            for _,file,x,y in layers[1:]:
                cmd += [file,'-geometry',f'+{x}+{y}','-compose','over','-composite']
            dest=ROOT/f'{filename}.png'
            mag(*cmd,'-strip',dest)
            ora(ROOT/f'source/{filename}.ora',canvas,layers,dest)
            cmd=['-size',f'{canvas[0]}x{canvas[1]}','xc:none']
            for _,file,x,y in layers[1:]:
                cmd += [file,'-geometry',f'+{x}+{y}','-compose','over','-composite']
            mag(*cmd,'-strip',ROOT/f'{filename}-transparent.png')
            mag(dest,'-colorspace','Gray','-strip',ROOT/f'monochrome/{filename}-gray.png')

if __name__ == '__main__':
    main()
