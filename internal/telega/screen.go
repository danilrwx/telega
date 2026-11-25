package telega

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const beginSynchronizedOutput, endSynchronizedOutput = "\x1b[?2026h", "\x1b[?2026l"

// Keep image uploads and multi-write Sixel output inside the same terminal frame.
func synchronizedOutput(w io.Writer, draw func() error) (err error) {
	if _, err = io.WriteString(w, beginSynchronizedOutput); err != nil {
		return err
	}

	defer func() {
		_, endErr := io.WriteString(w, endSynchronizedOutput)
		err = errors.Join(err, endErr)
	}()

	return draw()
}

// screenPainter remembers rows as positioned drawing commands, including styles.
type screenPainter struct {
	lines      []string
	cursor     string
	cols, rows int
}

func (p *screenPainter) writeFrame(w io.Writer, frame string, cols, rows int) error {
	lines, cursor, ok := screenRows(frame, rows)
	if !ok {
		*p = screenPainter{}

		return synchronizedOutput(w, func() error {
			_, err := io.WriteString(w, frame)
			return err
		})
	}

	full := p.cols != cols || p.rows != rows || len(p.lines) != rows

	var update strings.Builder
	if full {
		update.WriteString("\x1b[0m\x1b[2J")
	}

	for y, line := range lines {
		if !full && p.lines[y] == line {
			continue
		}

		if !full {
			fmt.Fprintf(&update, "\x1b[%d;1H\x1b[0m\x1b[2K", y+1)
		}

		update.WriteString(line)
	}

	if update.Len() == 0 && cursor == p.cursor {
		return nil
	}

	err := synchronizedOutput(w, func() error {
		_, err := io.WriteString(w, "\x1b[?25l"+update.String()+"\x1b[0m"+cursor)
		return err
	})
	if err != nil {
		*p = screenPainter{}
		return err
	}

	*p = screenPainter{lines: lines, cursor: cursor, cols: cols, rows: rows}

	return nil
}

// Only interpret the commands emitted by the UI; other streams get a full redraw.
func screenRows(frame string, height int) ([]string, string, bool) {
	if height < 1 {
		return nil, "", false
	}

	lines := make([]strings.Builder, height)
	styles := make([]string, height)
	parser := ansi.NewParser()
	style, visible := "", false
	x, y := 1, 1

	var state byte

	for len(frame) > 0 {
		seq, width, n, next := ansi.DecodeSequence(frame, state, parser)
		frame, state = frame[n:], next

		switch {
		case seq == "\x1b[?25h":
			visible = true
		case seq == "\x1b[?25l":
			visible = false
		case seq == "\x1b[2J":
		case strings.HasPrefix(seq, "\x1b[") && parser.Command() == 'H':
			y, _ = parser.Param(0, 1)
			x, _ = parser.Param(1, 1)

			if y < 1 || y > height || x < 1 {
				return nil, "", false
			}

			if styles[y-1] != style {
				lines[y-1].WriteString("\x1b[0m" + style)
				styles[y-1] = style
			}

			lines[y-1].WriteString(seq)
		case strings.HasPrefix(seq, "\x1b[") && parser.Command() == 'm':
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				style = ""
			} else {
				style += seq
			}

			lines[y-1].WriteString(seq)
			styles[y-1] = style
		case width > 0:
			lines[y-1].WriteString(seq)

			x += width
		default:
			return nil, "", false
		}
	}

	result := make([]string, height)

	for i := range lines {
		if lines[i].Len() > 0 {
			result[i] = "\x1b[0m" + lines[i].String()
		}
	}

	cursor := "\x1b[?25l"
	if visible {
		cursor = fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", y, x)
	}

	return result, cursor, true
}
