package tui

import "hash/fnv"

// NickColor selects a foreground color from the terminal's ANSI palette.
type NickColor struct{ ANSI int }

// ColorForNick assigns the same palette entry to a stable user or peer ID.
func ColorForNick(identity string) NickColor {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(identity))

	return nickColors[hash.Sum32()%uint32(len(nickColors))]
}

// Keep the order and length stable to preserve existing assignments.
var nickColors = [...]NickColor{
	{31},
	{31},
	{33},
	{33},
	{32},
	{32},
	{36},
	{36},
	{34},
	{34},
	{35},
	{35},
	{35},
}
