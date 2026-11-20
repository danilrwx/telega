package main

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/gotd/td/tg"
	"github.com/rivo/uniseg"
)

type textStyle uint16

const (
	boldStyle textStyle = 1 << iota
	italicStyle
	underlineStyle
	strikeStyle
	linkStyle
	codeStyle
	preStyle
	quoteStyle
	spoilerStyle
)

type textSpan struct {
	start, end int
	style      textStyle
	language   string
}

func codeBlocks(text string) (string, []tg.MessageEntityClass) {
	const fence = "```"
	if !strings.Contains(text, fence) {
		return text, nil
	}

	lines := strings.Split(text, "\n")

	var (
		content  []string
		entities []tg.MessageEntityClass
	)

	offset := 0

	appendLine := func(line string) {
		content = append(content, line)
		offset += utf16Length(line) + 1
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		language, opening := strings.CutPrefix(strings.TrimSpace(line), fence)
		if opening && !strings.ContainsAny(language, " \t`") {
			end := i + 1
			for end < len(lines) && strings.TrimSpace(lines[end]) != fence {
				end++
			}

			if end == len(lines) {
				content = append(content, lines[i:]...)
				break
			}

			code := strings.Join(lines[i+1:end], "\n")
			if length := utf16Length(strings.TrimRightFunc(code, unicode.IsSpace)); length > 0 {
				entities = append(entities, &tg.MessageEntityPre{Offset: offset, Length: length, Language: language})
				line, i = code, end
			} else {
				for _, literal := range lines[i:end] {
					appendLine(literal)
				}

				i, line = end, lines[end]
			}
		}

		appendLine(line)
	}

	return strings.Join(content, "\n"), entities
}

func editableCode(msg message) string {
	var draft strings.Builder

	written := 0

	for _, span := range msg.spans {
		if span.style != preStyle {
			continue
		}

		start, end := utf16Index(msg.text, span.start), utf16Index(msg.text, span.end)
		if start < written || end <= start || start > 0 && msg.text[start-1] != '\n' {
			continue
		}

		if tail, _, _ := strings.Cut(msg.text[end:], "\n"); strings.TrimSpace(tail) == "" {
			end += len(tail)
		} else {
			continue
		}

		if strings.Contains(msg.text[start:end], "```") || strings.ContainsAny(span.language, " \t\r\n`") {
			continue
		}

		draft.WriteString(msg.text[written:start])
		draft.WriteString("```" + span.language + "\n")
		draft.WriteString(msg.text[start:end])
		draft.WriteString("\n```")

		written = end
	}

	if written == 0 {
		return msg.text
	}

	draft.WriteString(msg.text[written:])

	return draft.String()
}

func utf16Index(s string, units int) int {
	for i, r := range s {
		if units == 0 {
			return i
		}

		units -= utf16.RuneLen(r)
		if units < 0 {
			return -1
		}
	}

	if units == 0 {
		return len(s)
	}

	return -1
}

func utf16Length(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}

	return length
}

func messageSpans(entities []tg.MessageEntityClass) []textSpan {
	var result []textSpan

	for _, entity := range entities {
		var (
			style    textStyle
			language string
		)

		switch entity := entity.(type) {
		case *tg.MessageEntityBold:
			style = boldStyle
		case *tg.MessageEntityItalic:
			style = italicStyle
		case *tg.MessageEntityUnderline:
			style = underlineStyle
		case *tg.MessageEntityStrike:
			style = strikeStyle
		case *tg.MessageEntityURL, *tg.MessageEntityTextURL, *tg.MessageEntityEmail, *tg.MessageEntityMention:
			style = linkStyle
		case *tg.MessageEntityCode:
			style = codeStyle
		case *tg.MessageEntityPre:
			style, language = preStyle, entity.Language
		case *tg.MessageEntityBlockquote:
			style = quoteStyle
		case *tg.MessageEntitySpoiler:
			style = spoilerStyle
		default:
			continue
		}

		start, length := entity.GetOffset(), entity.GetLength()
		if start >= 0 && length > 0 && start+length > start {
			result = append(result, textSpan{start: start, end: start + length, style: style, language: language})
		}
	}

	return result
}

func writeStyle(out *strings.Builder, style textStyle) {
	out.WriteString("\x1b[0")

	for _, entry := range []struct {
		mask textStyle
		sgr  string
	}{
		{boldStyle, ";1"},
		{italicStyle | quoteStyle, ";3"},
		{underlineStyle | linkStyle, ";4"},
		{strikeStyle, ";9"},
		{codeStyle | preStyle, ";36"},
		{linkStyle, ";36"},
	} {
		if style&entry.mask != 0 {
			out.WriteString(entry.sgr)
		}
	}

	out.WriteByte('m')
}

func formattedMessage(msg message, width, limit int, reveal bool) []string {
	if msg.attachment == nil && len(msg.spans) == 0 {
		return wrap(msg.text, width, limit)
	}

	var lines []string
	if msg.attachment != nil {
		lines = append(lines, "\x1b[36m"+wrap("[File] "+msg.attachment.name, width, 1)[0]+"\x1b[0m")
		if len(lines) == limit || msg.text == "[Attachment]" || msg.text == "" {
			return lines
		}
	}

	if len(msg.spans) == 0 {
		return append(lines, wrap(msg.text, width, limit-len(lines))...)
	}

	var line strings.Builder

	cells, offset := 0, 0

	var current textStyle

	graphemes := uniseg.NewGraphemes(msg.text)
	finish := func() {
		if current != 0 {
			line.WriteString("\x1b[0m")
		}

		lines = append(lines, line.String())
		line.Reset()

		cells, current = 0, 0
	}

	for graphemes.Next() {
		ch, span := graphemes.Str(), graphemes.Width()

		var style textStyle

		for _, entity := range msg.spans {
			if offset >= entity.start && offset < entity.end {
				style |= entity.style
			}
		}
		// Telegram measures formatting offsets in UTF-16 code units.
		offset += utf16Length(ch)
		if ch == "\n" || ch == "\r\n" {
			finish()

			if len(lines) == limit {
				return lines
			}

			continue
		}

		if ch == "\t" && style&(codeStyle|preStyle) != 0 {
			span = min(width-2, 4)
			ch = strings.Repeat(" ", span)
		} else if strings.ContainsFunc(ch, unicode.IsControl) {
			ch, span = " ", 1
		}

		if cells+span > width && cells > 0 {
			finish()

			if len(lines) == limit {
				return lines
			}
		}

		if cells == 0 && style&(quoteStyle|preStyle) != 0 && span+2 <= width {
			line.WriteString("\x1b[36m│ \x1b[0m")

			cells = 2
		}

		if span > width-cells {
			continue
		}

		if style&spoilerStyle != 0 && !reveal {
			ch = strings.Repeat("█", span)
		}

		if current != style {
			writeStyle(&line, style)
			current = style
		}

		line.WriteString(ch)

		cells += span
	}

	finish()

	return lines
}
