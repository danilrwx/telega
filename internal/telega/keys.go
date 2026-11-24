package telega

import (
	"bufio"
	"context"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danilrwx/telega/tui"
	"golang.org/x/sys/unix"
)

type key struct {
	name string
	text string
}

func readKeys(ctx context.Context, fd int, in io.Reader, out chan<- key, keyDone <-chan bool) {
	defer close(out)

	r := bufio.NewReader(in)
	for {
		ch, _, err := r.ReadRune()
		if err != nil {
			return
		}

		var k key

		switch ch {
		case 1:
			k.name = "ctrl+a"
		case 5:
			k.name = "ctrl+e"
		case 2:
			k.name = "fileMode"
		case 9:
			k.name = "tab"
		case 14:
			k.name = "nextChat"
		case 16:
			k.name = "previousChat"
		case 15:
			k.name = "jumpBack"
		case 19:
			k.name = "saveDraft"
		case 20:
			k.name = "textAttachment"
		case 22:
			k.name = "pasteImage"
		case 24:
			k.name = "editor"
		case 21:
			k.name = "pageup"
		case 4:
			k.name = "pagedown"
		case 3:
			k.name = "quit"
		case 13:
			k.name = "enter"
		case 10:
			k.name = "newline"
		case 8, 127:
			k.name = "erase"
		case 27:
			k = escapeKey(fd, r)
		default:
			if !unicode.IsControl(ch) {
				k.text = string(ch)
			}
		}

		select {
		case out <- k:
		case <-ctx.Done():
			return
		}
		// A command may give the terminal to Vim until the UI finishes it.
		select {
		case edited := <-keyDone:
			if edited {
				_, _ = r.Discard(r.Buffered())
			}
		case <-ctx.Done():
			return
		}
	}
}

func escapeKey(fd int, r *bufio.Reader) key {
	if r.Buffered() == 0 {
		event := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}

		n, _ := unix.Poll(event, 35)
		if n <= 0 {
			return key{name: "escape"}
		}
	}

	start, _, err := r.ReadRune()
	if err != nil || (start != '[' && start != 'O') {
		if err == nil {
			switch tui.RussianCommandRune(start) {
			case 'a':
				return key{name: "alt+a"}
			case 't':
				return key{name: "alt+t"}
			}

			_ = r.UnreadRune()
		}

		return key{name: "escape"}
	}

	var sequence [16]byte
	for i := range sequence {
		if r.Buffered() == 0 {
			event := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}

			n, _ := unix.Poll(event, 35)
			if n <= 0 {
				return key{}
			}
		}

		b, err := r.ReadByte()
		if err != nil {
			return key{}
		}

		sequence[i] = b
		if b < 0x40 || b > 0x7e {
			continue
		}

		switch string(sequence[:i+1]) {
		case "A":
			return key{name: "up"}
		case "B":
			return key{name: "down"}
		case "C":
			return key{name: "right"}
		case "D":
			return key{name: "left"}
		case "H", "1~":
			return key{name: "home"}
		case "I":
			return key{name: "focus"}
		case "O":
			return key{name: "blur"}
		case "3~":
			return key{name: "deleteRune"}
		case "5~":
			return key{name: "pageup"}
		case "6~":
			return key{name: "pagedown"}
		case "F", "4~":
			return key{name: "end"}
		case "200~":
			return key{name: "paste", text: readPaste(r)}
		}

		return key{}
	}

	return key{}
}

func readPaste(r *bufio.Reader) string {
	const end = "\x1b[201~"

	var text strings.Builder

	matched := 0

	for {
		b, err := r.ReadByte()
		if err != nil {
			return text.String()
		}

		if b == end[matched] {
			matched++
			if matched == len(end) {
				return text.String()
			}

			continue
		}

		if matched > 0 {
			if text.Len() < inputLimit*utf8.UTFMax {
				text.WriteString(end[:matched])
			}

			matched = 0
		}

		if b == end[0] {
			matched = 1
		} else if text.Len() < inputLimit*utf8.UTFMax {
			text.WriteByte(b)
		}
	}
}

// Terminal input contains characters. Map Russian key positions in command modes.
func commandKey(k key) key {
	if k.name != "" || utf8.RuneCountInString(k.text) != 1 {
		return k
	}

	r, _ := utf8.DecodeRuneInString(k.text)

	k.text = string(tui.RussianCommandRune(r))

	return k
}
