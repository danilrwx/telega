package tui

import (
	"fmt"
	"hash/fnv"

	"github.com/muesli/termenv"
)

// RGB is a 24-bit terminal color.
type RGB uint32

// Hex returns a hex color string.
func (c RGB) Hex() string {
	return fmt.Sprintf("#%06X", uint32(c))
}

// SGR returns the true-color SGR parameters for a foreground or background.
func (c RGB) SGR(background bool) string {
	return termenv.TrueColor.Color(c.Hex()).Sequence(background)
}

// NickColor holds matching foreground colors for light, dark and ANSI terminals.
// Light and Dark are 24-bit RGB values. ANSI is a basic foreground SGR code.
type NickColor struct {
	Light, Dark RGB
	ANSI        int
}

// ColorForNick assigns the same palette entry to a stable user or peer ID.
func ColorForNick(identity string) NickColor {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(identity))

	return nickColors[hash.Sum32()%uint32(len(nickColors))]
}

// Keep the order and length stable to preserve existing assignments.
var nickColors = [...]NickColor{
	{0x992D42, 0xF4A0AA, 31},
	{0x8B432B, 0xF1AD89, 31},
	{0x76550E, 0xE8C981, 33},
	{0x566319, 0xCDD991, 33},
	{0x276345, 0xA7D5A7, 32},
	{0x236A5E, 0x94D7BE, 32},
	{0x216573, 0x92D6DE, 36},
	{0x255E84, 0x98CCEB, 36},
	{0x38579B, 0xA4B8F5, 34},
	{0x554B94, 0xBCACF1, 34},
	{0x6E468F, 0xD3ACEA, 35},
	{0x863D73, 0xE5A5CF, 35},
	{0x8D3F5E, 0xEEABC5, 35},
}
