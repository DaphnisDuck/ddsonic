# Production record

Built-in image generation/editing was used, with the owner-supplied `approved-design-sheet.png` as the reference. No fallback CLI/API generation was used. The final saved PNG masters are the reproducible source of truth. Generation itself is nondeterministic; these prompts document provenance, not a promise that another run would reproduce identical pixels.

## Canonical icon prompt

Use case: precise-object-edit. Production asset extraction, NOT redesign. Use only the LARGE UPPER LEFT rounded square app icon from the supplied owner-approved branding sheet as edit target. Output ONE standalone 2048x2048 square high resolution app icon, no presentation sheet, no text labels, no size labels, no wordmark. Faithfully reconstruct/extract that exact approved icon with clean crisp edges: identical cream duck face, black oval eyes with white highlights, orange smiling beak and orange chest accents, exactly TWO cream wings both resting on the little black keyboard across the bottom, same black retro monitor tilted in perspective behind duck, cream monitor rim, cream >_ prompt, bright green segmented equalizer bars on screen. Keep original shapes, pose, proportions, composition, palette, friendly expression. The left wing is large in foreground and right wing smaller to its right atop keyboard. Keep complete keyboard including cream bottom edge. Dark charcoal rounded square tile with subtle gray rim, entire tile centered filling square, transparent outside rounded corners, no external shadows or glow. The artwork within the tile fills it like reference. Preserve identity exactly; do not introduce glasses, headphones, extra limbs, text, ornaments. No SVG claims. This is the canonical raster master for a software app.

Actual returned dimensions were **1254 × 1254**. The returned native resolution is preserved; a larger file was not fabricated to match the requested dimensions.

## Isolated mark prompt

Use case: background-extraction. Remove ONLY the dark rounded-square tile and its gray border from behind this exact duck-and-terminal app icon. Preserve the entire duck, both cream wings on keyboard, all black outlines, keyboard base, terminal monitor and cream rim, cream >_ prompt, green equalizer. Keep identical artwork and colors, proportions and pose. Transparent background outside the actual duck+monitor+keyboard silhouette; also transparent where the original rounded tile showed through between silhouette parts. Do not remove the dark terminal screen or black contours. No redesign, no lettering, no shadows, no checkerboard. Single standalone isolated brand mark at high resolution, entire artwork visible with modest transparent padding.

## Monochrome icon prompt

Use case: precise-object-edit. Make a monochrome grayscale version of this exact app icon. Change ONLY colors: cream to near-white, orange to medium-light gray, green equalizer to near-white. Keep black contours, black screen, dark rounded tile and subtle gray rim. Preserve EXACT geometry, every shape, duck face, both wings on keyboard, cream >_ prompt, all equalizer segments, framing and dimensions. Do not redraw or redesign. Real transparency outside rounded tile, no checkerboard. Single grayscale app icon, no other text. High resolution.

The delivered gray export is also converted to a neutral grayscale color space to remove any residual tint in the generated source.

## Final wordmark prompt

Extract and clean the ddmus lettering and adjacent equalizer from the lower-left horizontal logo in this reference. Single standalone wordmark asset, 3:1 wide. Background MUST be fully OPAQUE perfectly uniform solid dark charcoal #111719. No transparency anywhere. Text exactly 'ddmus' in the identical wide cream rounded geometric lowercase lettering from reference. Same bright green three-column segmented equalizer to right of text. NO duck, NO monitor, NO tagline, NO labels. Flat solid cream letter fills, flat green equalizer, crisp smooth contours. Remove all texture and noise. Fill holes in letters and all empty spaces with the same solid #111719 background. Preserve reference letter shapes, no font substitution, no redesign. Close framing with generous enough padding so nothing is clipped. This is a finished production wordmark, not a presentation sheet.

Two earlier transparent wordmark outputs contained stray white artifacts and were rejected. The final opaque output is retained as `wordmark-generated.png`; its dark background was color-keyed using ImageMagick (`-alpha on -fuzz 14% -transparent '#111719' -trim +repage`) and the resulting master inspected on dark background. Do not use the rejected generations as source artwork.

## Deterministic production

- PNG icons: Lanczos reduction from the canonical master, no independent generation per size.
- Tagline master: DejaVu Sans, 96-pixel type with 28-pixel tracking; exact text `MUSIC FOR YOUR TERMINAL`; color #35E65C. Font files are not redistributed.
- Logo layouts: composited from saved isolated-mark, wordmark and tagline PNG masters; layout coordinates are in `build.py` and editable layers in the two `.ora` files.
- Monochrome logos: grayscale conversions of the final composited logos. Single-color wordmarks preserve the wordmark's alpha mask.
- ICO: actual multi-resolution container built from exported PNG icons, not a renamed PNG.
- Visual checks: icon size sheet, dark/light isolated-mark compositing, final stacked/horizontal layouts, monochrome exports and wordmark edges.

The generated reconstruction preserves the approved design identity but is not a lossless extraction or a newly owner-approved vector drawing. Keep the original sheet for visual comparison during review.
