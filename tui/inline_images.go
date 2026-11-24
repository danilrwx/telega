package tui

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rivo/uniseg"
)

const (
	inlineCacheSize   = 96
	inlinePlaceholder = "\U0010EEEE"
)

// InlineImage holds graphic placements anchored in the text rows of a conversation.
type InlineImage struct {
	Rows        []string
	Width       int
	tag, upload string
	delete      string
	sent        bool
	sixel       *inlineSixel
}

// DecodeInlineImage decodes a small raster for an inline placement.
func DecodeInlineImage(in io.Reader, cols, rows int) (*InlineImage, error) {
	image, err := decodeTerminalImage(in, max(32, cols*16, rows*32), sixelGraphics())
	if err != nil {
		return nil, err
	}

	return image.inlinePlacement(cols, rows)
}

// Inline creates a virtual placement. A 1×1 placement is used for reaction icons.
func (im *TerminalImage) Inline(maxCols, maxRows int) *InlineImage {
	placement, _ := im.inlinePlacement(maxCols, maxRows)
	return placement
}

var kittyRowMarks = []rune("\u0305\u030d\u030e\u0310\u0312\u033d\u033e\u033f\u0346\u034a\u034b\u034c" +
	"\u0350\u0351\u0352\u0357\u035b\u0363\u0364\u0365\u0366\u0367\u0368\u0369\u036a\u036b\u036c\u036d\u036e\u036f" +
	"\u0483\u0484\u0485\u0486\u0487\u0592\u0593\u0594\u0595\u0597\u0598\u0599\u059c\u059d\u059e")

func (im *TerminalImage) inlinePlacement(maxCols, maxRows int) (*InlineImage, error) {
	maxRows = max(1, min(maxRows, len(kittyRowMarks)))
	maxCols = max(1, maxCols)
	cols := max(1, min(maxCols, int(math.Round(float64(maxRows)*2*float64(im.width)/float64(im.height)))))
	rows := max(1, min(maxRows, int(math.Round(float64(cols)*float64(im.height)/(2*float64(im.width))))))
	id := rand.Uint32N(0xfffffe) + 1

	result := im.virtualPlacement(id, cols, rows)
	if im.sixel {
		result.sixel = &inlineSixel{source: im.source}
	} else {
		var packet strings.Builder
		if err := writeKittyPNG(&packet, im.source, id, cols, rows, ",U=1", KittyPassthrough()); err != nil {
			return nil, err
		}

		result.upload = packet.String()
		result.delete = kittyCommand(fmt.Sprintf("a=d,d=I,i=%d,q=2", id))
	}

	return result, nil
}

func (im *TerminalImage) virtualPlacement(id uint32, cols, rows int) *InlineImage {
	tag := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16, (id>>8)&255, id&255)

	result := &InlineImage{Width: cols, tag: tag}
	for row := range rows {
		result.Rows = append(result.Rows, tag+inlinePlaceholder+string(kittyRowMarks[row])+strings.Repeat(inlinePlaceholder, cols-1)+"\x1b[39m")
	}

	return result
}

// InlineImages writes the virtual placements needed by terminal frames.
type InlineImages struct {
	*os.File
	mu        sync.Mutex
	items     map[string]*InlineImage
	order     []string
	sixel     bool
	sixelTags map[string]*InlineImage
}

func NewInlineImages(out *os.File) *InlineImages {
	sixel := sixelGraphics()
	if !sixel && !KittyGraphics() {
		return nil
	}

	g := &InlineImages{File: out, items: make(map[string]*InlineImage), sixel: sixel}
	if sixel {
		g.sixelTags = make(map[string]*InlineImage)
	}

	return g
}

func (g *InlineImages) Get(key string) *InlineImage {
	if g == nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.items[key]
}

// Start avoids scheduling repeated downloads, including known missing images.
func (g *InlineImages) Start(key string) bool {
	if g == nil {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, seen := g.items[key]; seen {
		return false
	}

	if len(g.order) >= inlineCacheSize {
		old := g.order[0]

		g.order = g.order[1:]
		if image := g.items[old]; image != nil {
			delete(g.sixelTags, image.tag)

			if image.sent {
				_, _ = g.File.WriteString(image.delete)
			}
		}

		delete(g.items, old)
	}

	g.items[key] = nil
	g.order = append(g.order, key)

	return true
}

func (g *InlineImages) Store(key string, image *InlineImage) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if old, ok := g.items[key]; ok {
		if old != nil {
			delete(g.sixelTags, old.tag)
		}

		g.items[key] = image
		if g.sixel && image != nil && image.sixel != nil {
			g.sixelTags[image.tag] = image
		}
	}
}

func (g *InlineImages) Write(data []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.sixel {
		return g.writeSixel(data)
	}

	written := 0

	for _, mode := range []string{"\x1b[?1049h", "\x1b[?1049l"} {
		if index := bytes.Index(data, []byte(mode)); index >= 0 {
			end := index + len(mode)
			n, err := g.File.Write(data[:end])

			written += n
			if err != nil {
				return written, err
			}

			data = data[end:]

			for _, image := range g.items {
				if image != nil {
					image.sent = false
				}
			}
		}
	}

	if bytes.Contains(data, []byte(inlinePlaceholder)) {
		for _, image := range g.items {
			if image != nil && !image.sent && bytes.Contains(data, []byte(image.tag+inlinePlaceholder)) {
				if _, err := g.File.WriteString(image.upload); err != nil {
					return written, err
				}

				image.sent = true
			}
		}
	}

	n, err := g.File.Write(data)

	return written + n, err
}

// WriteString keeps string writes on the same serialized output path.
func (g *InlineImages) WriteString(s string) (int, error) {
	if g.sixel && !strings.Contains(s, inlinePlaceholder) {
		g.mu.Lock()
		defer g.mu.Unlock()

		return g.File.WriteString(s)
	}

	return g.Write([]byte(s))
}

func (g *InlineImages) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, image := range g.items {
		if image != nil && image.sent {
			_, _ = g.File.WriteString(image.delete)
		}
	}

	clear(g.items)
	clear(g.sixelTags)
	g.order = nil
}

// InlineImageFile reports whether the message attachment is a supported still image.
func InlineImageFile(name, mime string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}

	switch strings.ToLower(mime) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}

	return false
}

// ReactionBadge pairs a reaction label and count with an optional inline icon.
type ReactionBadge struct {
	Label string
	Count int
	Image *InlineImage
}

// ReactionBadges prints as many whole badges as fit on one line.
func ReactionBadges(prefix string, badges []ReactionBadge, width int) string {
	text, used := prefix, uniseg.StringWidth(prefix)

	for i, badge := range badges {
		spacer := ""
		if i > 0 {
			spacer = "  "
		}

		count := fmt.Sprintf(" %d", badge.Count)

		label, cells := badge.Label, uniseg.StringWidth(badge.Label)
		if badge.Image != nil && len(badge.Image.Rows) == 1 {
			label, cells = badge.Image.Rows[0], badge.Image.Width
		} else {
			label = Clip(label, width)
		}

		full := cells + len(count) + len(spacer)
		if used+full > width {
			if i == 0 && cells > 0 {
				return Clip(prefix+badge.Label+count, width)
			}

			if used < width {
				text += "…"
			}

			break
		}

		text += spacer + label + count
		used += full
	}

	return text
}
