package imageopt

import (
	"bytes"
	"encoding/binary"
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

func pngHeader(width, height uint32) []byte {
	var output bytes.Buffer
	output.Write([]byte("\x89PNG\r\n\x1a\n"))
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8] = 8
	data[9] = 2

	binary.Write(&output, binary.BigEndian, uint32(len(data)))
	output.WriteString("IHDR")
	output.Write(data)
	checksum := crc32.NewIEEE()
	checksum.Write([]byte("IHDR"))
	checksum.Write(data)
	binary.Write(&output, binary.BigEndian, checksum.Sum32())
	return output.Bytes()
}
