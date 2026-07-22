package seaweedfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestOptimizeDownsizesAndEncodesWebP(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / 2000),
				G: uint8((y * 255) / 1000),
				B: 90,
				A: 255,
			})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}

	result, err := Optimize(bytes.NewReader(input.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if result.OriginalWidth != 2000 || result.OriginalHeight != 1000 || result.Width != 1600 || result.Height != 800 {
		t.Fatalf("unexpected dimensions: %+v", result)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "webp" || config.Width != 1600 || config.Height != 800 {
		t.Fatalf("unexpected output: format=%q dimensions=%dx%d", format, config.Width, config.Height)
	}
	if len(result.Data) > MaxStoredBytes {
		t.Fatalf("optimized image exceeds storage ceiling: %d bytes", len(result.Data))
	}
}

func TestOptimizeRejectsTooManyPixelsBeforeDecode(t *testing.T) {
	input := pngHeader(8000, 5001)
	_, err := Optimize(bytes.NewReader(input))
	if err != ErrTooManyPixels {
		t.Fatalf("expected ErrTooManyPixels, got %v", err)
	}
}

func TestOptimizeRejectsMalformedImage(t *testing.T) {
	_, err := Optimize(bytes.NewReader([]byte("not an image")))
	if err == nil {
		t.Fatal("expected malformed image error")
	}
}

func TestOptimizeRejectsNilSource(t *testing.T) {
	_, err := Optimize(nil)
	if !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected ErrInvalidImage, got %v", err)
	}
}

func pngHeader(width, height uint32) []byte {
	output := []byte("\x89PNG\r\n\x1a\n")
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8] = 8
	data[9] = 2

	var encodedInteger [4]byte
	binary.BigEndian.PutUint32(encodedInteger[:], uint32(len(data)))
	output = append(output, encodedInteger[:]...)
	output = append(output, "IHDR"...)
	output = append(output, data...)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte("IHDR"))
	_, _ = checksum.Write(data)
	binary.BigEndian.PutUint32(encodedInteger[:], checksum.Sum32())
	return append(output, encodedInteger[:]...)
}
