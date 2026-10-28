# Final ddsonic assembly

The owner approved the ddsonic stacked/horizontal name trials and authorized final assembly. No further image generation was used in this assembly step.

The original icon, isolated mark, tagline and monochrome-icon masters were copied byte-for-byte from the previous branding package. The approved horizontal ddsonic trial supplied the new name artwork: a 1400 × 300 region at x=700, y=200 was extracted, its dark background color-keyed with ImageMagick (`-alpha on -fuzz 14% -transparent '#111719' -trim +repage`), and the lettering and adjacent equalizer retained as the new raster wordmark master.

Both final logos were assembled from the original isolated-mark and tagline masters plus this single shared wordmark. This avoids using the image-edited trial's duck pixels as final artwork. The reproducible layout coordinates, scaling and colors are recorded in `build.py`. Grayscale logos and single-color wordmarks are deterministic conversions; icon exports use Lanczos reduction. The ICO contains seven actual image frames.

Final checks cover visual inspection of both logo layouts and monochrome exports, icon dimensions and identity with the previous exports, OpenRaster layer references, ZIP integrity and per-file SHA-256 checksums. Historical reference images intentionally retain the former name where applicable.

Original raster generation provenance and the approved trial editing prompt are retained in `original-production-notes.md` and `name-trial-notes.md`. No genuine vector reconstruction was performed.
