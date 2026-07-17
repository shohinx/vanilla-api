package seaweedfs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/deepteams/webp"
	"github.com/disintegration/imaging"
)

const (
	MaxSourcePixels = 40_000_000
	MaxWidth        = 1600
	MaxHeight       = 1600
	MaxStoredBytes  = 2 << 20
	WebPQuality     = 80
)

var (
	ErrInvalidImage   = errors.New("invalid image")
	ErrTooManyPixels  = errors.New("image has too many pixels")
	ErrCannotCompress = errors.New("image cannot be compressed below the storage limit")
)

type Result struct {
	Data           []byte
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
}

// Optimize validates, auto-orients, downsizes, and converts one still image
// to WebP. Re-encoding without metadata strips EXIF, ICC, and XMP payloads.
func Optimize(source io.ReadSeeker) (Result, error) {
	config, format, err := image.DecodeConfig(source)
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode configuration: %v", ErrInvalidImage, err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return Result{}, fmt.Errorf("%w: unsupported format %q", ErrInvalidImage, format)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Result{}, fmt.Errorf("%w: invalid dimensions", ErrInvalidImage)
	}
	if int64(config.Width)*int64(config.Height) > MaxSourcePixels {
		return Result{}, ErrTooManyPixels
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return Result{}, fmt.Errorf("%w: rewind source: %v", ErrInvalidImage, err)
	}

	decoded, err := imaging.Decode(source, imaging.AutoOrientation(true))
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode pixels: %v", ErrInvalidImage, err)
	}
	options := webp.DefaultOptions()
	options.Quality = WebPQuality
	options.Preset = webp.PresetPhoto
	options.UseSharpYUV = true

	var optimized image.Image
	var output bytes.Buffer
	for _, maxDimension := range []int{MaxWidth, 1200, 800} {
		optimized = imaging.Fit(decoded, maxDimension, maxDimension, imaging.Lanczos)
		output.Reset()
		if err := webp.Encode(&output, optimized, options); err != nil {
			return Result{}, fmt.Errorf("encode optimized WebP: %w", err)
		}
		if output.Len() <= MaxStoredBytes {
			break
		}
	}
	if output.Len() > MaxStoredBytes {
		return Result{}, ErrCannotCompress
	}

	originalBounds := decoded.Bounds()
	optimizedBounds := optimized.Bounds()
	return Result{
		Data:           output.Bytes(),
		OriginalWidth:  originalBounds.Dx(),
		OriginalHeight: originalBounds.Dy(),
		Width:          optimizedBounds.Dx(),
		Height:         optimizedBounds.Dy(),
	}, nil
}
