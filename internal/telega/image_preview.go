package telega

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func (a *app) previewReaction(choice reactionChoice, out chan<- request) {
	if !tui.ImageGraphics() {
		a.status = "Image preview needs Kitty or Sixel graphics"
		return
	}

	if choice.custom == 0 {
		a.status = "Select a custom reaction to preview its icon"
		return
	}

	if a.pending == "" {
		a.ask(out, request{kind: "reactionImage", chat: a.currentChat(), reactionDocument: choice.custom})
	}
}

func downloadReactionImage(ctx context.Context, client *telegram.Client, accountID, id int64) (string, error) {
	return cacheAttachment(fmt.Sprintf("reaction-%d-%d", accountID, id), "reaction.webp", 0, func(out io.Writer) error {
		documents, err := client.API().MessagesGetCustomEmojiDocuments(ctx, []int64{id})
		if err != nil {
			return err
		}

		for _, document := range documents {
			doc, ok := document.(*tg.Document)
			if !ok || doc.ID != id {
				continue
			}

			file, err := reactionImageFile(doc)
			if err != nil {
				return err
			}

			if len(file.inlineData) > 0 {
				_, err := out.Write(file.inlineData)
				return err
			}

			invoker, err := client.FileDC(ctx, file.dc)
			if err != nil {
				return err
			}

			builder := client.DownloadVia(invoker, file.location)
			if file.size > 0 {
				builder.WithSize(file.size)
			}

			var data imageBuffer
			if _, err := builder.Stream(ctx, &data); err != nil {
				return err
			}

			if file.size > 0 && int64(data.Len()) != file.size {
				return errors.New("incomplete custom reaction image")
			}

			_, err = data.WriteTo(out)

			return err
		}

		return errors.New("custom reaction missing from Telegram response")
	})
}

func reactionImageFile(doc *tg.Document) (*attachment, error) {
	file := &attachment{name: "reaction.webp", dc: doc.DCID}
	if doc.MimeType == "image/webp" || doc.MimeType == "image/png" || doc.MimeType == "image/jpeg" {
		file.size, file.location = doc.Size, doc.AsInputDocumentFileLocation("")
		return file, nil
	}

	thumb := stillThumbnail(doc.Thumbs, func(kind string) tg.InputFileLocationClass {
		return doc.AsInputDocumentFileLocation(kind)
	}, doc.DCID)
	if thumb == nil {
		return nil, errors.New("static reaction preview missing from Telegram response")
	}

	file = thumb

	return file, nil
}
