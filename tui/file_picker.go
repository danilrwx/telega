package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type FilePicker struct {
	fileInput
	dir      string
	entries  []os.DirEntry
	choices  []attachmentChoice
	selected int
	Status   string
}

func NewFilePicker() *FilePicker {
	p := &FilePicker{}
	p.refresh()

	return p
}

func (p *FilePicker) Path() string { return string(p.input) }

func (p *FilePicker) SetPath(path string) {
	p.input = []rune(path)
	p.cursor = len(p.input)
	p.Status = ""
	p.refresh()
}

type attachmentChoice struct {
	path, label string
	dir         bool
}

func ExpandAttachmentPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}

	return filepath.Abs(path)
}

func (p *FilePicker) refresh() {
	p.selected = -1
	p.choices = nil
	value := string(p.input)

	prefix, name := filepath.Split(value)
	if value == "~" {
		prefix, name = "~/", ""
	}

	dir, err := ExpandAttachmentPath(prefix)
	if err != nil {
		p.Status = err.Error()
		return
	}

	if dir != p.dir {
		p.entries, err = os.ReadDir(dir)
		if err != nil {
			p.dir = ""
			if !os.IsNotExist(err) {
				p.Status = err.Error()
			}

			return
		}

		p.dir = dir
	}

	if name == "" && dir != filepath.Dir(dir) {
		parent := filepath.Dir(strings.TrimSuffix(dir, string(os.PathSeparator)))
		p.choices = append(p.choices, attachmentChoice{path: strings.TrimRight(parent, string(os.PathSeparator)) + string(os.PathSeparator), label: "../", dir: true})
	}

	files := make([]attachmentChoice, 0)

	for _, entry := range p.entries {
		label := entry.Name()
		if !strings.HasPrefix(label, name) || (strings.HasPrefix(label, ".") && !strings.HasPrefix(name, ".")) {
			continue
		}

		isDir, regular := entry.IsDir(), entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(dir, label))
			if err == nil {
				isDir, regular = info.IsDir(), info.Mode().IsRegular()
			}
		}

		if isDir {
			label += string(os.PathSeparator)
			p.choices = append(p.choices, attachmentChoice{path: prefix + label, label: label, dir: true})
		} else if regular {
			files = append(files, attachmentChoice{path: prefix + label, label: label})
		}
	}

	p.choices = append(p.choices, files...)
}

func (p *FilePicker) complete() {
	if index := p.selected; index >= 0 {
		p.SetPath(p.choices[index].path)
		return
	}

	start := 0
	if len(p.choices) > 0 && p.choices[0].label == "../" {
		start++
	}

	choices := p.choices[start:]
	switch len(choices) {
	case 0:
		return
	case 1:
		p.SetPath(choices[0].path)
	default:
		prefix := choices[0].path
		for _, choice := range choices[1:] {
			for !strings.HasPrefix(choice.path, prefix) {
				_, size := utf8.DecodeLastRuneInString(prefix)
				prefix = prefix[:len(prefix)-size]
			}
		}

		if len(prefix) > len(string(p.input)) {
			p.SetPath(prefix)
		} else {
			p.selected = start
		}
	}
}

// Key returns an action and the selected absolute path when a file is attached.
func (p *FilePicker) Key(name, text string) (action, path string) {
	switch name {
	case "escape", "esc":
		return "close", ""
	case "ctrl+t":
		return "text", ""
	case "up", "ctrl+p":
		p.selected = max(-1, p.selected-1)
	case "down", "ctrl+n":
		p.selected = min(len(p.choices)-1, p.selected+1)
	case "tab":
		p.complete()
	case "enter":
		value := p.Path()
		if p.selected >= 0 {
			choice := p.choices[p.selected]
			if choice.dir {
				p.SetPath(choice.path)
				return "", ""
			}

			value = choice.path
		}

		if value == "" {
			p.Status = "Choose a file or type a path"
			return "", ""
		}

		path, err := ExpandAttachmentPath(value)
		if err == nil {
			info, statErr := os.Stat(path)
			if statErr == nil && info.IsDir() {
				p.SetPath(strings.TrimRight(value, string(os.PathSeparator)) + string(os.PathSeparator))
				return "", ""
			}

			_, err = AttachmentStat(path)
		}

		if err != nil {
			p.Status = err.Error()
			return "", ""
		}

		return "attach", path
	default:
		if p.key(name, text) {
			p.Status = ""
			p.refresh()
		}
	}

	return "", ""
}

type fileInput struct {
	input  []rune
	cursor int
}

func (p *fileInput) key(name, text string) (changed bool) {
	switch name {
	case "left":
		p.cursor = max(0, p.cursor-1)
	case "right":
		p.cursor = min(len(p.input), p.cursor+1)
	case "home", "ctrl+a":
		p.cursor = 0
	case "end", "ctrl+e":
		p.cursor = len(p.input)
	case "backspace", "erase":
		if p.cursor > 0 {
			p.input = slices.Delete(p.input, p.cursor-1, p.cursor)
			p.cursor--

			return true
		}
	case "delete", "deleteRune":
		if p.cursor < len(p.input) {
			p.input = slices.Delete(p.input, p.cursor, p.cursor+1)
			return true
		}
	default:
		text = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}

			return r
		}, text)
		if text != "" {
			p.input = slices.Insert(p.input, p.cursor, []rune(text)...)
			p.cursor += utf8.RuneCountInString(text)

			return true
		}
	}

	return false
}

func (p *FilePicker) Render(width, height int, render func(string, bool, bool, bool)) {
	render(Clip("Attach a file", width), true, false, false)
	p.render(width, "Path to a file", render)
	render("", false, false, false)

	room := max(1, height-5)

	start := max(0, p.selected-room+1)
	for i, choice := range p.choices[start:min(len(p.choices), start+room)] {
		active := start+i == p.selected

		prefix := "  "
		if active {
			prefix = "› "
		}

		render(Clip(prefix+choice.label, width), false, active, false)
	}

	render(Clip(p.Status, width), false, false, false)

	hint := "↑↓ choose  Tab complete  Enter attach  Ctrl+T text file  Esc back"
	if width < 60 {
		hint = "↑↓ Tab Enter ^T Esc"
	}

	render(Clip(hint, width), false, false, true)
}

func (p *fileInput) render(width int, placeholder string, render func(string, bool, bool, bool)) {
	before := string(p.input[:p.cursor])
	tail := len(before)
	cells := 0

	for tail > 0 {
		r, size := utf8.DecodeLastRuneInString(before[:tail])

		cells += uniseg.StringWidth(string(r))
		if cells >= width {
			break
		}

		tail -= size
	}

	path := before[tail:] + "▏" + string(p.input[p.cursor:])

	empty := len(p.input) == 0
	if empty {
		path += placeholder
	}

	render(Clip(path, width), false, false, empty)
}

func AttachmentStat(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("choose a non-empty regular file")
	}

	return info, nil
}
