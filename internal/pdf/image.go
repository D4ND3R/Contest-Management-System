package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decoders for AddImage
	_ "image/png"
)

// Image is a picture embedded once in a document and drawn on any page.
type Image struct {
	Width, Height int
	filter, space string
	data          []byte
	id            int
}

// MaxImagePixels bounds the images a document accepts.
const MaxImagePixels = 12 << 20

// ErrImageTooLarge is returned for images over MaxImagePixels.
var ErrImageTooLarge = errors.New("the image is too large")

// AddImage embeds a JPEG as it is, or any other supported image (PNG) as
// compressed RGB with transparency composed over white.
func (d *Doc) AddImage(data []byte) (*Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxImagePixels {
		return nil, ErrImageTooLarge
	}
	im := &Image{Width: cfg.Width, Height: cfg.Height}
	switch {
	case format == "jpeg" && cfg.ColorModel == color.YCbCrModel:
		im.filter, im.space, im.data = "DCTDecode", "DeviceRGB", data
	case format == "jpeg" && cfg.ColorModel == color.GrayModel:
		im.filter, im.space, im.data = "DCTDecode", "DeviceGray", data
	default:
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		raw := make([]byte, 0, b.Dx()*b.Dy()*3)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := img.At(x, y).RGBA() // alpha-premultiplied
				raw = append(raw, byte((r+0xffff-a)>>8), byte((g+0xffff-a)>>8), byte((bl+0xffff-a)>>8))
			}
		}
		var z bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
		zw.Write(raw)
		zw.Close()
		im.filter, im.space, im.data = "FlateDecode", "DeviceRGB", z.Bytes()
	}
	d.images = append(d.images, im)
	im.id = len(d.images)
	return im, nil
}

// Image draws im in the rectangle (x, y, w, h).
func (p *Page) Image(im *Image, x, y, w, h float64) {
	fmt.Fprintf(&p.buf, "q %s 0 0 %s %s %s cm /Im%d Do Q\n", num(w), num(h), num(x), num(y), im.id)
}

func (im *Image) object() string {
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /%s /BitsPerComponent 8 /Filter /%s /Length %d >>\nstream\n%s\nendstream",
		im.Width, im.Height, im.space, im.filter, len(im.data), im.data)
}
