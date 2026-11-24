package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/sys/unix"
)

const (
	maxKittyInput  = 16 << 20
	maxPreviewSide = 4096
)

// KittyGraphics reports whether previews can use the Kitty graphics protocol.
func KittyGraphics() bool {
	switch os.Getenv("TUI_GRAPHICS") {
	case "kitty":
		return true
	case "sixel", "off":
		return false
	}

	switch os.Getenv("TUI_KITTY_GRAPHICS") {
	case "1":
		return true
	case "0":
		return false
	}

	if multiplexedTerminal() && os.Getenv("TMUX") != "" {
		return tmuxUsesKitty()
	}

	return os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("TERM") == "xterm-kitty"
}

var tmuxUsesKitty = sync.OnceValue(func() bool {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane,
		"#{client_termname} #{allow-passthrough}").Output()
	if err != nil {
		return false
	}

	fields := strings.Fields(string(result))

	return len(fields) == 2 && fields[0] == "xterm-kitty" && (fields[1] == "on" || fields[1] == "all")
})

// TerminalImage holds a bounded source image and its encoded terminal raster.
type TerminalImage struct {
	id                        uint32
	width, height             int
	source                    image.Image
	sixel                     bool
	sixelColumns              []sixelColumn
	sixelCellWidth            int
	rasterWidth, rasterHeight int
}

// KittyImage is the image type used by the original Kitty-only API.
type KittyImage = TerminalImage

// LoadKittyImage opens an image for the Kitty graphics protocol.
func LoadKittyImage(path string) (*KittyImage, error) { return loadTerminalImage(path, false) }

// DecodeKittyImage decodes an image for the Kitty graphics protocol.
func DecodeKittyImage(in io.Reader) (*KittyImage, error) { return decodeTerminalImage(in, 0, false) }

// LoadTerminalImage opens an image for the configured terminal graphics protocol.
func LoadTerminalImage(path string) (*TerminalImage, error) {
	return loadTerminalImage(path, sixelGraphics())
}

// DecodeTerminalImage decodes an image for the configured terminal graphics protocol.
func DecodeTerminalImage(in io.Reader) (*TerminalImage, error) {
	return decodeTerminalImage(in, 0, sixelGraphics())
}

func loadTerminalImage(path string, sixel bool) (*TerminalImage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() > maxKittyInput {
		return nil, errors.New("image exceeds the preview size limit")
	}

	return decodeTerminalRaster(file, 0, sixel)
}

func decodeTerminalImage(in io.Reader, pixelLimit int, sixel bool) (*TerminalImage, error) {
	data, err := io.ReadAll(io.LimitReader(in, maxKittyInput+1))
	if err != nil {
		return nil, err
	}

	if len(data) > maxKittyInput {
		return nil, errors.New("image exceeds the preview size limit")
	}

	return decodeTerminalRaster(bytes.NewReader(data), pixelLimit, sixel)
}

func decodeTerminalRaster(in io.ReadSeeker, pixelLimit int, sixel bool) (*TerminalImage, error) {
	config, _, err := image.DecodeConfig(io.LimitReader(in, maxKittyInput))
	if err != nil {
		return nil, err
	}

	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, errors.New("image dimensions exceed the preview limit")
	}

	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	source, _, err := image.Decode(io.LimitReader(in, maxKittyInput))
	if err != nil {
		return nil, err
	}

	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	if width < 1 || height < 1 {
		return nil, errors.New("preview frame is empty")
	}

	result := &TerminalImage{id: rand.Uint32N(0xfffffe) + 1, width: width, height: height, source: source, sixel: sixel}
	if pixelLimit == 0 {
		return result, nil
	}

	if longest := max(width, height); longest > pixelLimit {
		width, height = max(1, width*pixelLimit/longest), max(1, height*pixelLimit/longest)
	}

	result.source = result.scaled(width, height, xdraw.ApproxBiLinear)
	result.width, result.height = width, height

	return result, nil
}

func (im *TerminalImage) scaled(width, height int, scaler xdraw.Scaler) image.Image {
	source := im.source
	if rect := source.Bounds(); rect != image.Rect(0, 0, width, height) {
		scaled := image.NewNRGBA(image.Rect(0, 0, width, height))
		scaler.Scale(scaled, scaled.Bounds(), source, rect, xdraw.Src, nil)

		return scaled
	}

	return source
}

func terminalCellSize(terminal io.Writer) (float64, float64) {
	if tty, ok := terminal.(interface{ Fd() uintptr }); ok {
		geometry, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
		if err == nil && geometry.Row > 0 && geometry.Col > 0 && geometry.Xpixel > 0 && geometry.Ypixel > 0 {
			return float64(geometry.Xpixel) / float64(geometry.Col), float64(geometry.Ypixel) / float64(geometry.Row)
		}
	}

	return 8, 16
}

func kittyCommand(command string) string {
	prefix, suffix := kittyFraming(KittyPassthrough())
	return prefix + command + suffix
}

// KittyPassthrough reports whether graphics writes must be wrapped for tmux.
func KittyPassthrough() bool { return os.Getenv("TMUX") != "" && multiplexedTerminal() }

// Delete removes just this image and its placement.
func (im *TerminalImage) Delete() string {
	if im.sixel {
		return ""
	}

	return kittyCommand(fmt.Sprintf("a=d,d=I,i=%d,q=2", im.id))
}

// Preview returns a complete frame for callers that need to buffer it.
func (im *TerminalImage) Preview(terminal io.Writer, title string, cols, rows int, external bool) string {
	var out strings.Builder

	_ = im.writePreview(&out, terminal, title, cols, rows, external)

	return out.String()
}

// WritePreview writes a centered preview without buffering a second copy of its raster.
func (im *TerminalImage) WritePreview(out io.Writer, title string, cols, rows int, external bool) error {
	return im.writePreview(out, out, title, cols, rows, external)
}

func (im *TerminalImage) writePreview(out, terminal io.Writer, title string, cols, rows int, external bool) error {
	if im.source == nil {
		return errors.New("image preview has been released")
	}

	if cols < 8 || rows < 6 {
		_, err := io.WriteString(out, "Resize your terminal")
		return err
	}

	cellWidth, cellHeight := terminalCellSize(terminal)
	maxCols, maxRows := cols-2, rows-4

	passthrough := !im.sixel && KittyPassthrough()
	if passthrough {
		maxRows = min(maxRows, len(kittyRowMarks))
	}

	pixelLimit := maxPreviewSide
	if im.sixel {
		pixelLimit = 1280
	}

	scale := min(
		float64(maxCols)*cellWidth/float64(im.width),
		float64(maxRows)*cellHeight/float64(im.height),
		float64(pixelLimit)/float64(max(im.width, im.height)),
	)
	if !external {
		scale = min(1, scale)
	}

	pixelWidth := max(1, int(math.Round(float64(im.width)*scale)))
	pixelHeight := max(1, int(math.Round(float64(im.height)*scale)))

	var scaler xdraw.Scaler = xdraw.CatmullRom
	// Catmull-Rom allocates a 32-byte intermediate per source row and destination column.
	if int64(pixelWidth)*int64(im.height) > 4_000_000 {
		scaler = xdraw.ApproxBiLinear
	}

	width := min(maxCols, max(1, int(math.Ceil(float64(pixelWidth)/cellWidth))))
	height := min(maxRows, max(1, int(math.Ceil(float64(pixelHeight)/cellHeight))))

	hint := "Esc back"
	if external {
		hint = "o open externally  Esc back"
		if cols < 30 {
			hint = "o external  Esc back"
		}
	}

	if !im.sixel {
		if _, err := io.WriteString(out, im.Delete()); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(out, "\x1b[2J\x1b[1;2H%s\x1b[%d;2H%s", Clip(title, cols-2), rows, Clip(hint, cols-2)); err != nil {
		return err
	}

	row, col := 2+(rows-2-height)/2, 1+(cols-width)/2

	if im.sixel {
		if err := im.prepareSixelPreview(pixelWidth, pixelHeight, max(1, int(cellWidth)), scaler); err != nil {
			_, err := io.WriteString(out, "\x1b[3;2H"+Clip("Preview: "+err.Error(), cols-2))
			return err
		}

		for _, tile := range im.sixelColumns {
			if _, err := fmt.Fprintf(out, "\x1b[%d;%dH", row, col+tile.offset); err != nil {
				return err
			}

			if _, err := io.WriteString(out, tile.data); err != nil {
				return err
			}
		}

		return nil
	}

	source := im.scaled(pixelWidth, pixelHeight, scaler)
	if passthrough {
		placement := im.virtualPlacement(im.id, width, height)
		if err := writeKittyPNG(out, source, im.id, width, height, ",U=1", true); err != nil {
			return err
		}

		for i, line := range placement.Rows {
			if _, err := fmt.Fprintf(out, "\x1b[%d;%dH%s", row+i, col, line); err != nil {
				return err
			}
		}

		return nil
	}

	if _, err := fmt.Fprintf(out, "\x1b[%d;%dH", row, col); err != nil {
		return err
	}

	return writeKittyPNG(out, source, im.id, width, height, "", false)
}
