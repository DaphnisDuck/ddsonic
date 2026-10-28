package artwork

import (
	"image"
	"image/color"
)

// scale returns img shrunk so its longer side is at most side, keeping its
// aspect ratio, by averaging the source pixels each target pixel covers.
// An image already small enough is returned as is.
func scale(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side || w == 0 || h == 0 {
		return img
	}
	tw, th := side, side*h/w
	if h > w {
		tw, th = side*w/h, side
	}
	tw, th = max(tw, 1), max(th, 1)
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := range th {
		y0, y1 := b.Min.Y+y*h/th, b.Min.Y+(y+1)*h/th
		for x := range tw {
			x0, x1 := b.Min.X+x*w/tw, b.Min.X+(x+1)*w/tw
			var r, g, bl, a, n uint64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			out.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return out
}
