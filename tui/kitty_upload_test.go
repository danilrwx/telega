package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"
)

func kittyPNGBytes(t *testing.T, frame string, passthrough bool) []byte {
	t.Helper()

	prefix, suffix := kittyFraming(passthrough)

	var decoded []byte

	seen, done := false, false

	for packet := range strings.SplitSeq(frame, prefix) {
		control, payload, ok := strings.Cut(packet, ";")
		if !ok || !strings.Contains(control, "m=") {
			continue
		}

		if done || (!seen && !strings.HasPrefix(control, "a=T,f=100,")) ||
			(seen && control != "m=1" && control != "m=0") {
			t.Fatalf("unexpected Kitty packet control data: %s", control)
		}

		payload, _, ok = strings.Cut(payload, suffix)

		done = strings.HasSuffix(control, "m=0")
		if !ok || len(payload) > kittyChunkSize || !done && len(payload) != kittyChunkSize {
			t.Fatalf("invalid Kitty packet size: %d", len(payload))
		}

		var err error

		decoded, err = base64.StdEncoding.AppendDecode(decoded, []byte(payload))
		if err != nil {
			t.Fatal(err)
		}

		seen = true
	}

	if !done {
		t.Fatal("missing final Kitty packet")
	}

	return decoded
}

func TestKittyUploadEndsAnExactChunkOnTheFinalPacket(t *testing.T) {
	var out strings.Builder

	prefix, suffix := kittyFraming(false)
	upload := kittyUpload{out: &out, prefix: prefix, suffix: suffix, header: "a=T,f=100,"}

	raster := bytes.Repeat([]byte("png"), kittyChunkSize/2)
	for data := raster; len(data) > 0; {
		size := min(97, len(data))

		n, err := upload.Write(data[:size])
		if err != nil || n != size {
			t.Fatalf("write = %d, %v", n, err)
		}

		data = data[size:]
	}

	if err := upload.send('0'); err != nil {
		t.Fatal(err)
	}

	if strings.Count(out.String(), prefix) != 2 || !bytes.Equal(kittyPNGBytes(t, out.String(), false), raster) {
		t.Fatal("exact chunk boundary changed the payload or produced an extra packet")
	}
}

func TestKittyUploadStreamsTheOriginalPNG(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 79, 53))

	rng := rand.New(rand.NewPCG(1, 2))
	for i := range src.Pix {
		src.Pix[i] = byte(rng.Uint32())
	}

	var expected bytes.Buffer
	if err := png.Encode(&expected, src); err != nil {
		t.Fatal(err)
	}

	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			var out strings.Builder
			if err := writeKittyPNG(&out, src, 21, 8, 4, ",U=1", passthrough); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), ",U=1,m=1;") || !bytes.Equal(kittyPNGBytes(t, out.String(), passthrough), expected.Bytes()) {
				t.Fatal("the stream changed the placement or the PNG bytes")
			}
		})
	}
}
