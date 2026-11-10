package screen

import (
	"bufio"
	"image"
	"image/png"
	"os"
)

// IsBlack reports whether a frame is (nearly) all black, which is what a
// screen capture of an exclusive-fullscreen game often looks like.
func IsBlack(img *image.RGBA) bool {
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	var sum, n, peak int
	for i := 1; i < 20; i++ {
		for j := 1; j < 20; j++ {
			x := b.Min.X + b.Dx()*i/20
			y := b.Min.Y + b.Dy()*j/20
			o := img.PixOffset(x, y)
			l := (int(img.Pix[o])*3 + int(img.Pix[o+1])*6 + int(img.Pix[o+2])) / 10
			sum += l
			n++
			peak = max(peak, l)
		}
	}
	return sum/n < 6 && peak < 30
}

// OfferBand crops a game-window frame to the middle band where augment cards
// appear (same as the GDI path) and returns the crop with its offset.
func OfferBand(img *image.RGBA) (*image.RGBA, int, int) {
	b := img.Bounds()
	cx, cy := b.Dx()*8/100, b.Dy()*12/100
	r := image.Rect(b.Min.X+cx, b.Min.Y+cy, b.Min.X+cx+b.Dx()*84/100, b.Min.Y+cy+b.Dy()*78/100)
	return img.SubImage(r).(*image.RGBA), cx, cy
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(w, img); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
