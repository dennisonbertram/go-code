# Icon explorations

Minimalist monochrome app-icon marks on a dark rounded-square tile. Made while
designing the **Sonda** app icon, kept here to reuse across apps (pi-google,
life-rag, …). Each has a full-res `png/` (1024×1024) and its `svg/` source.

See `contact-sheet.png` for all of them at a glance.

## The marks

| Name | Description |
|------|-------------|
| **aperture** | A ring focused on a center point. Calm, minimal. |
| **spark** | 4-point star, flat. |
| **spark-refined** | The spark with a soft luminance glow + gradients. |
| **spark-rotated-cutout-refined** | The rotated-cutout with the glow/gradient treatment — the icon currently shipping on Sonda. |
| **sounding-line** | A weighted line dropping from an anchor (a depth probe). |
| **sonar** | Echo arcs radiating from a source. |
| **orb** | A soft monochrome sphere with depth. |
| **crescent** | A crescent of light (reads as a moon). |
| **spark-ring** | A spark with a spark-shaped hole (outline). |
| **spark-rotated-cutout** | A smaller spark rotated 45° and carved out of the center. |
| **spark-8point** | Spark + a rotated spark → 8-point burst. |
| **spark-two-tone** | Big light spark + smaller grey spark inside (facet/depth). |
| **spark-off-center** | Spark shifted off-center with a small companion. |
| **spark-faceted** | Spark with a ring + rotated cutout. |

## Regenerating / resizing

The SVGs render correctly with any **librsvg**-based rasterizer (ImageMagick's
built-in SVG renderer drops stroked circles/lines). To re-render a PNG at size N:

```bash
# via sharp (node)
node -e "require('sharp')(require('fs').readFileSync('svg/aperture.svg'),{density:384}).resize(N,N).png().toFile('out.png')"
# or, if installed:
rsvg-convert -w N -h N svg/aperture.svg > out.png
```

To turn one into a macOS `.icns`: render sizes 16/32/64/128/256/512/1024 into an
`Icon.iconset/` (with `@2x` variants) and run `iconutil -c icns Icon.iconset`.
