package tui

import "strings"

// MessageBlock represents either prose or a fenced code block.
type MessageBlock struct {
	Text, Language string
	Code           bool
}

// SplitCodeFences recognizes complete Markdown fences while preserving prose and indentation.
func SplitCodeFences(text string) []MessageBlock {
	if !strings.Contains(text, "```") && !strings.Contains(text, "~~~") {
		return []MessageBlock{{Text: text}}
	}

	lines := strings.Split(text, "\n")

	var blocks []MessageBlock

	start := 0

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if len(line) < 3 || line[0] != '`' && line[0] != '~' {
			continue
		}

		fence := line[:1]

		fenceSize := len(line) - len(strings.TrimLeft(line, fence))
		if fenceSize < 3 || fence == "`" && strings.Contains(line[fenceSize:], "`") {
			continue
		}

		end := i + 1
		for end < len(lines) {
			closing := strings.TrimSpace(lines[end])
			if len(closing) >= fenceSize && strings.Trim(closing, fence) == "" {
				break
			}

			end++
		}

		if end == len(lines) {
			break
		}

		if start < i {
			blocks = append(blocks, MessageBlock{Text: strings.Join(lines[start:i], "\n")})
		}

		info := strings.Fields(line[fenceSize:])

		code := MessageBlock{Text: strings.Join(lines[i+1:end], "\n"), Code: true}
		if len(info) > 0 {
			code.Language = info[0]
		}

		blocks = append(blocks, code)
		i, start = end, end+1
	}

	if start < len(lines) {
		blocks = append(blocks, MessageBlock{Text: strings.Join(lines[start:], "\n")})
	}

	return blocks
}

// CodeLines renders a fenced block in a terminal-sized frame.
func CodeLines(block MessageBlock, width int) []string {
	if width < 4 {
		return Wrap(block.Text, width, 0)
	}

	language := block.Language
	if language == "" {
		language = "code"
	}

	lines := []string{Clip("╭─ "+language, width)}
	for _, line := range Wrap(strings.ReplaceAll(block.Text, "\t", "    "), width-2, 0) {
		lines = append(lines, "│ "+line)
	}

	return append(lines, "╰─")
}
