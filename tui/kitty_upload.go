package tui

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
)

const kittyChunkSize = 4096

func kittyFraming(passthrough bool) (prefix, suffix string) {
	if passthrough {
		return "\x1bPtmux;\x1b\x1b_G", "\x1b\x1b\\\x1b\\"
	}

	return "\x1b_G", "\x1b\\"
}

// kittyUpload buffers only the next PNG chunk. A full chunk stays pending
// until another byte tells us it is not the final chunk.
type kittyUpload struct {
	out                    io.Writer
	prefix, suffix, header string
	pending                [kittyChunkSize / 4 * 3]byte
	used                   int
	packet                 [kittyChunkSize + 160]byte
}

func (u *kittyUpload) Write(data []byte) (int, error) {
	written := 0

	for len(data) != 0 {
		if u.used == len(u.pending) {
			if err := u.send('1'); err != nil {
				return written, err
			}
		}

		n := copy(u.pending[u.used:], data)
		u.used += n
		written += n
		data = data[n:]
	}

	return written, nil
}

func (u *kittyUpload) send(more byte) error {
	packet := append(u.packet[:0], u.prefix...)
	packet = append(packet, u.header...)
	packet = append(packet, 'm', '=', more, ';')
	packet = base64.StdEncoding.AppendEncode(packet, u.pending[:u.used])
	packet = append(packet, u.suffix...)

	n, err := u.out.Write(packet)
	if err == nil && n != len(packet) {
		err = io.ErrShortWrite
	}

	u.used = 0
	u.header = ""

	return err
}

func writeKittyPNG(out io.Writer, source image.Image, id uint32, cols, rows int, attrs string, passthrough bool) error {
	prefix, suffix := kittyFraming(passthrough)

	upload := kittyUpload{
		out: out, prefix: prefix, suffix: suffix,
		header: fmt.Sprintf("a=T,f=100,t=d,C=1,q=2,i=%d,c=%d,r=%d%s,", id, cols, rows, attrs),
	}
	if err := png.Encode(&upload, source); err != nil {
		return err
	}

	return upload.send('0')
}
