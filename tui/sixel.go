package tui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi/sixel"
	xdraw "golang.org/x/image/draw"
)

// ImageGraphics reports whether the terminal can display Kitty or Sixel images.
func ImageGraphics() bool { return KittyGraphics() || sixelGraphics() }

func sixelGraphics() bool {
	switch os.Getenv("TUI_GRAPHICS") {
	case "sixel":
		return true
	case "kitty", "off":
		return false
	}

	if KittyGraphics() {
		return false
	}

	if multiplexedTerminal() {
		return os.Getenv("TMUX") != "" && tmuxUsesSixel()
	}

	term := os.Getenv("TERM")

	return term == "foot" || strings.HasPrefix(term, "foot-") ||
		term == "xterm-sixel" || term == "mlterm" || term == "yaft-256color"
}

func tmuxUsesSixel() bool { return tmuxGraphics().sixel }

func renderSixel(img image.Image) (string, error) {
	var out bytes.Buffer
	out.WriteString("\x1bP0;1q")

	if err := new(sixel.Encoder).Encode(&out, img); err != nil {
		return "", err
	}
	// The encoder ends with a line advance that can move the cursor beyond the raster.
	if out.Bytes()[out.Len()-1] == '-' {
		out.Truncate(out.Len() - 1)
	}

	out.WriteString("\x1b\\")

	return out.String(), nil
}

// tmux discards a complete DCS when its input buffer fills.
var tmuxSixelLimit = sync.OnceValue(func() int {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := exec.CommandContext(ctx, "tmux", "show-options", "-sv", "input-buffer-size").Output()
	if limit, parseErr := strconv.Atoi(strings.TrimSpace(string(result))); err == nil && parseErr == nil && limit > 0 {
		return max(1, limit-128)
	}

	return (1 << 20) - 128
})

type sixelColumn struct {
	offset int
	data   string
}

func sixelColumns(source image.Image, cellWidth, limit int) ([]sixelColumn, error) {
	bounds := source.Bounds()

	var (
		columns []sixelColumn
		encode  func(left, right int) error
	)

	encode = func(left, right int) error {
		raster := source

		if left != 0 || right != bounds.Dx() {
			tile := image.NewNRGBA(image.Rect(0, 0, right-left, bounds.Dy()))
			xdraw.Draw(tile, tile.Bounds(), source, bounds.Min.Add(image.Pt(left, 0)), xdraw.Src)
			raster = tile
		}

		data, err := renderSixel(raster)
		if err != nil {
			return err
		}

		if limit == 0 || len(data) <= limit {
			columns = append(columns, sixelColumn{offset: left / cellWidth, data: data})
			return nil
		}

		middle := left + (right-left)/2/cellWidth*cellWidth
		if middle == left {
			middle += cellWidth
		}

		if middle >= right {
			return errors.New("image exceeds the tmux Sixel input buffer")
		}

		if err := encode(left, middle); err != nil {
			return err
		}

		return encode(middle, right)
	}
	// The encoder's working set grows with both the area and palette size.
	const tilePixels = 256 * 1024

	tileWidth := max(cellWidth, tilePixels/bounds.Dy()/cellWidth*cellWidth)
	for left := 0; left < bounds.Dx(); left += tileWidth {
		if err := encode(left, min(left+tileWidth, bounds.Dx())); err != nil {
			return nil, err
		}
	}

	return columns, nil
}

func (im *TerminalImage) prepareSixelPreview(width, height, cellWidth int, scaler xdraw.Scaler) error {
	if im.rasterWidth != width || im.rasterHeight != height || im.sixelCellWidth != cellWidth {
		limit := 0
		if os.Getenv("TMUX") != "" && multiplexedTerminal() {
			limit = tmuxSixelLimit()
		}

		im.sixelColumns = nil
		im.rasterWidth, im.rasterHeight = 0, 0

		columns, err := sixelColumns(im.scaled(width, height, scaler), cellWidth, limit)
		if err != nil {
			return err
		}

		im.sixelColumns = columns
		im.rasterWidth, im.rasterHeight, im.sixelCellWidth = width, height, cellWidth
	}

	return nil
}

type inlineSixel struct {
	source                image.Image
	data                  string
	rows                  []string
	cellWidth, cellHeight int
}

func (s *inlineSixel) prepare(width, height, cellWidth, cellHeight int) {
	if s.cellWidth == cellWidth && s.cellHeight == cellHeight {
		return
	}

	s.cellWidth, s.cellHeight = cellWidth, cellHeight
	pixelWidth, pixelHeight := width*cellWidth, height*cellHeight
	frame := image.NewNRGBA(image.Rect(0, 0, pixelWidth, pixelHeight))
	bounds := s.source.Bounds()

	w, h := pixelWidth, pixelHeight
	if w*bounds.Dy() > h*bounds.Dx() {
		w = max(1, h*bounds.Dx()/bounds.Dy())
	} else {
		h = max(1, w*bounds.Dy()/bounds.Dx())
	}

	x, y := (pixelWidth-w)/2, (pixelHeight-h)/2
	xdraw.ApproxBiLinear.Scale(frame, image.Rect(x, y, x+w, y+h), s.source, bounds, xdraw.Src, nil)

	s.data, _ = renderSixel(frame)

	s.rows = make([]string, height)
	for row := range height {
		strip := image.NewNRGBA(image.Rect(0, 0, pixelWidth, cellHeight))
		xdraw.Draw(strip, strip.Bounds(), frame, image.Pt(0, row*cellHeight), xdraw.Src)
		s.rows[row], _ = renderSixel(strip)
	}
}

func (s *inlineSixel) writeRow(out *bufio.Writer, width, row int, passthrough bool) {
	if s.data == "" {
		_, _ = out.WriteString(strings.Repeat(" ", width))
		return
	}

	_, _ = fmt.Fprintf(out, "\x1b[%dX", width)

	var packet strings.Builder
	packet.WriteString("\x1b7")
	// Native tmux Sixel erases whole images; Foot passthrough can update individual rows.
	if row != 0 && !passthrough {
		_, _ = fmt.Fprintf(&packet, "\x1b[%dA", row)
	}

	if passthrough {
		packet.WriteString(s.rows[row])
	} else {
		packet.WriteString(s.data)
	}

	packet.WriteString("\x1b8")

	if passthrough {
		_, _ = out.WriteString("\x1bPtmux;" + strings.ReplaceAll(packet.String(), "\x1b", "\x1b\x1b") + "\x1b\\")
	} else {
		_, _ = out.WriteString(packet.String())
	}

	_, _ = fmt.Fprintf(out, "\x1b[%dC", width)
}

func (g *InlineImages) writeSixel(data []byte) (int, error) {
	if !bytes.Contains(data, []byte(inlinePlaceholder)) {
		return g.File.Write(data)
	}

	originalSize := len(data)
	width, height := terminalCellSize(g.File)
	cellWidth, cellHeight := max(1, min(128, int(width))), max(1, min(128, int(height)))
	out := bufio.NewWriter(g.File)
	passthrough := KittyPassthrough() && tmuxGraphics().sixelPassthrough

	const (
		sgr    = "\x1b[38;2;"
		maxTag = len("\x1b[38;2;255;255;255m")
	)

	for {
		start := bytes.Index(data, []byte(sgr))
		if start < 0 {
			break
		}

		_, _ = out.Write(data[:start])
		data = data[start:]

		end := bytes.IndexByte(data[:min(len(data), maxTag)], 'm') + 1
		if end > 0 && bytes.HasPrefix(data[end:], []byte(inlinePlaceholder)) {
			if image := g.sixelTags[string(data[:end])]; image != nil {
				if row := slices.IndexFunc(image.Rows, func(pattern string) bool {
					return bytes.HasPrefix(data, []byte(pattern))
				}); row >= 0 {
					image.sixel.prepare(image.Width, len(image.Rows), cellWidth, cellHeight)
					image.sixel.writeRow(out, image.Width, row, passthrough)
					data = data[len(image.Rows[row]):]

					continue
				}
			}
		}

		_, _ = out.WriteString(sgr)
		data = data[len(sgr):]
	}

	_, _ = out.Write(data)
	if err := out.Flush(); err != nil {
		return 0, err
	}

	return originalSize, nil
}
