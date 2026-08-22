package clean

// Image handling: every raster image is flattened onto white, converted to
// grayscale, downscaled to the width cap, and re-encoded as baseline JPEG.
// SVG/WebP/TIFF are dropped and replaced by a blank placeholder.

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"path/filepath"
	"strings"

	// decoders register themselves with image.Decode
	_ "image/gif"
	_ "image/png"

	"github.com/disintegration/imaging"
)

const jpegQuality = 82

var blankJPEG = func() []byte {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: jpegQuality})
	return b.Bytes()
}()

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }

func isRasterImage(name string) bool {
	switch ext(name) {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	}
	return false
}

func isDroppedImage(name string) bool {
	switch ext(name) {
	case ".webp", ".tiff", ".tif", ".svg":
		return true
	}
	return false
}

func isFontFile(name string) bool {
	switch ext(name) {
	case ".ttf", ".otf", ".woff", ".woff2":
		return true
	}
	return false
}

// convertImage decodes any supported raster, flattens alpha over white,
// grayscales, caps width, and returns baseline JPEG bytes.
func convertImage(data []byte, maxWidth int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	b := src.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(white()), image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), src, b.Min, draw.Over)

	if maxWidth > 0 && b.Dx() > maxWidth {
		resized := imaging.Resize(canvas, maxWidth, 0, imaging.Lanczos)
		canvas = image.NewRGBA(resized.Bounds())
		draw.Draw(canvas, canvas.Bounds(), resized, resized.Bounds().Min, draw.Src)
	}
	gray := imaging.Grayscale(canvas)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, gray, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
