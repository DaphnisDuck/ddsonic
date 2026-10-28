# ddsonic branding

Final assembly of the owner-approved ddsonic name trial with the original saved duck/terminal artwork. Original ddsonic artwork generated for this project, initially named ddmus. No repository code or packaging configuration has been modified.

## Files

| File | Dimensions | Intended use |
| --- | --- | --- |
| `ddsonic-logo.png` | 1400 × 1400 | Primary stacked logo, dark background |
| `ddsonic-logo-horizontal.png` | 2400 × 800 | README/site header, dark background |
| `ddsonic-logo-transparent.png` | 1400 × 1400 | Stacked logo on a custom dark surface |
| `ddsonic-logo-horizontal-transparent.png` | 2400 × 800 | Horizontal logo on a custom dark surface |
| `ddsonic-wordmark.png` | See manifest | Cream lettering with green equalizer, transparent |
| `ddsonic-icon.png` | 1024 × 1024 | High-resolution application icon |
| `icons/ddsonic-{16,24,32,48,64,128,256,512}.png` | Named size, square | Linux/application icons |
| `ddsonic.ico` | 16/24/32/48/64/128/256 frames | Optional Windows/favicon container |
| `monochrome/ddsonic-icon-gray.png` | 1024 × 1024 | Neutral grayscale icon |
| `monochrome/ddsonic-logo-gray.png` | 1400 × 1400 | Grayscale stacked logo |
| `monochrome/ddsonic-logo-horizontal-gray.png` | 2400 × 800 | Grayscale horizontal logo |
| `monochrome/ddsonic-wordmark-light.png` | See manifest | White single-color lettering, for dark surfaces |
| `monochrome/ddsonic-wordmark-dark.png` | See manifest | Charcoal single-color lettering, for light surfaces |
| `masters/` | See manifest | Canonical raster icon, isolated duck/terminal, wordmark, tagline and grayscale icon source |
| `source/ddsonic-logo.ora` | 1400 × 1400 | Four-layer editable OpenRaster source |
| `source/ddsonic-logo-horizontal.ora` | 2400 × 800 | Four-layer editable OpenRaster source |
| `previews/` | See manifest | Final overview and actual-size/enlarged icon inspection sheets |

The icon, isolated mark, tagline and grayscale-icon masters are byte-identical to the saved ddmus package masters, with new filenames. Only the name artwork changes. The new lettering was extracted from the approved horizontal ddsonic trial and is shared by both final layouts. The exact tagline remains **MUSIC FOR YOUR TERMINAL**.

## Colors and use

Dark background `#111719`; cream approximately `#FFF1DB`; orange approximately `#FF7923`; bright screen green approximately `#31F558`; tagline green `#35E65C`. Generated artwork contains shading and antialiasing, so colors vary within the image. PNGs are intended for sRGB screen use. Preserve aspect ratio. Transparent cream logos need a dark background; use the opaque versions on unknown page themes.

## Small sizes

At 16/24 px, the cream/orange duck and green screen remain recognizable, but the wings, keys and prompt merge. At 32 px fine details remain soft; at 48 px the two wing shapes become distinguishable; use 64 px or larger when both wings, keyboard, `>_` and green bars must read clearly. These are the same inspected icon exports as the original package, without a redesigned miniature duck.

## Source and provenance

The original owner-approved reference sheet is preserved as `source/original-approved-design-sheet.png`. It contains the former name and illustrative format labels; it is reference material, not an export to integrate. The approved ddsonic trial images are also retained as references. Production exports and editable layouts all use ddsonic.

The source is **raster**, including the native 1254 × 1254 icon master. No genuine vector SVG is available or claimed. OpenRaster files provide separate background, duck/terminal, wordmark and tagline layers; the duck's internal shapes are not editable vector paths. Grayscale variants contain multiple gray tones; single-color wordmarks have antialiased transparency.

Run `python3 source/build.py` with ImageMagick 7 to rebuild exports and layered layouts from the saved masters without image generation or font dependencies. The script overwrites derivatives in this directory only. Previews, provenance and the manifest are delivery records. `manifest.json` lists dimensions and SHA-256 checksums; `source/production-notes.md` records final assembly.

For later integration, copy this directory to `assets/branding`, use the named PNG sizes for application packaging and the horizontal logo for README/site headers. Preserve the artwork. This package contains no repository changes.
