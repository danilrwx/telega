package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/sixel"
)

func TestSixelPreviewAndInlinePlacement(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "")
	t.Setenv("TUI_KITTY_GRAPHICS", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "foot")
	t.Setenv("TMUX", "")

	if KittyGraphics() || !ImageGraphics() {
		t.Fatal("Foot should select Sixel automatically")
	}

	src := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.NRGBA{R: 240, A: 255}), image.Point{}, draw.Src)

	var data bytes.Buffer
	if err := png.Encode(&data, src); err != nil {
		t.Fatal(err)
	}

	image, err := DecodeTerminalImage(bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	preview := image.Preview(nil, "photo.png", 80, 24, true)
	if !strings.Contains(preview, "\x1bP0;1q") || !strings.Contains(preview, "photo.png") ||
		!strings.Contains(preview, "o open externally") || strings.Contains(preview, "\x1b_G") || image.Delete() != "" {
		t.Fatalf("incorrect Sixel preview: %q", preview)
	}

	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	out := NewInlineImages(file)

	icon, err := DecodeInlineImage(bytes.NewReader(data.Bytes()), 1, 1)
	if err != nil || !out.Start("emoji") {
		t.Fatalf("could not start inline Sixel: %v", err)
	}

	out.Store("emoji", icon)
	frame := "before " + icon.Rows[0] + " 2 after"

	n, err := out.WriteString(frame)
	if err != nil || n != len(frame) {
		t.Fatalf("invalid transformed write result: %d, %v", n, err)
	}

	wire, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(wire, []byte("before \x1b[1X\x1b7\x1bP0;1q")) ||
		!bytes.HasSuffix(wire, []byte("\x1b8\x1b[1C 2 after")) ||
		bytes.Contains(wire, []byte(inlinePlaceholder)) {
		t.Fatalf("image placement overlaps the surrounding cells: %q", wire)
	}

	thumb, err := DecodeInlineImage(bytes.NewReader(data.Bytes()), 4, 2)
	if err != nil || !out.Start("thumb") {
		t.Fatalf("could not make a multiple-row placement: %v", err)
	}

	out.Store("thumb", thumb)

	if _, err := out.WriteString(thumb.Rows[1]); err != nil {
		t.Fatal(err)
	}

	wire, err = os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	// A renderer may update just the second row. It must still redraw the whole thumbnail.
	if !bytes.Contains(wire, []byte("\x1b7\x1b[1A\x1bP0;1q\"1;1;32;32")) {
		t.Fatalf("a single changed row did not repaint the complete thumbnail: %q", wire)
	}

	t.Setenv("TUI_GRAPHICS", "off")

	if ImageGraphics() || NewInlineImages(file) != nil {
		t.Fatal("graphics should stay disabled in off mode")
	}
}

func TestClosingPreviewDropsTheRaster(t *testing.T) {
	im := TerminalImage{
		source:       image.NewNRGBA(image.Rect(0, 0, 8, 8)),
		sixelColumns: []sixelColumn{{data: "tile"}},
	}
	im.Release()
	im.Release()

	if im.source != nil || im.sixelColumns != nil {
		t.Fatal("closing the preview should drop all raster data")
	}
}

func TestSixelColumnsFitTheTmuxBuffer(t *testing.T) {
	const cellWidth, limit = 8, 16 << 10

	src := image.NewNRGBA(image.Rect(0, 0, 191, 96))

	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] =
			byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32()), 255
	}

	columns, err := sixelColumns(src, cellWidth, limit)
	if err != nil || len(columns) < 2 {
		t.Fatalf("expected a split image, got %d blocks, error %v", len(columns), err)
	}

	width := 0
	for _, col := range columns {
		if len(col.data) > limit || col.offset*cellWidth != width {
			t.Fatalf("misaligned or oversized block, col %d size %d", col.offset, len(col.data))
		}

		payload := strings.TrimSuffix(strings.TrimPrefix(col.data, "\x1bP0;1q"), "\x1b\\")

		decoded, err := new(sixel.Decoder).Decode(strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}

		width += decoded.Bounds().Dx()
		if decoded.Bounds().Dy() != src.Bounds().Dy() {
			t.Fatalf("unexpected tile height %v", decoded.Bounds())
		}
	}

	if width != src.Bounds().Dx() {
		t.Fatalf("combined tiles span %d px, expected %d", width, src.Bounds().Dx())
	}
}
