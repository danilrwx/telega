package telega

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader/fileref"
	"github.com/gotd/td/tg"
)

const maxInlineDownload = 4 << 20

func stillThumbnail(sizes []tg.PhotoSizeClass, makeLocation func(string) tg.InputFileLocationClass, dc int) *attachment {
	var chosen *attachment

	for _, size := range sizes {
		var thumb attachment

		thumb.name, thumb.dc = "thumbnail.jpg", dc

		switch value := size.(type) {
		case *tg.PhotoSize:
			thumb.location = makeLocation(value.Type)
			thumb.width, thumb.height, thumb.size = value.W, value.H, int64(value.Size)
		case *tg.PhotoCachedSize:
			thumb.location = makeLocation(value.Type)
			thumb.width, thumb.height, thumb.size = value.W, value.H, int64(len(value.Bytes))
			thumb.inlineData = value.Bytes
		default:
			continue
		}

		if thumb.size <= 0 || thumb.size > maxInlineDownload {
			continue
		}

		largeEnough := min(thumb.width, thumb.height) >= 128
		if chosen == nil || (largeEnough &&
			(min(chosen.width, chosen.height) < 128 || thumb.size < chosen.size)) ||
			(!largeEnough && min(chosen.width, chosen.height) < 128 && thumb.size > chosen.size) {
			chosen = &thumb
		}
	}

	return chosen
}

func inlineAttachment(file *attachment) (string, *attachment) {
	if file == nil || !tui.InlineImageFile(file.name, file.mime) {
		return "", nil
	}

	if file.thumbnail != nil {
		file = file.thumbnail
	}

	if file.size <= 0 || file.size > maxInlineDownload {
		return "", nil
	}

	switch location := file.location.(type) {
	case *tg.InputDocumentFileLocation:
		return fmt.Sprintf("document:%d:%s", location.ID, location.ThumbSize), file
	case *tg.InputPhotoFileLocation:
		return fmt.Sprintf("photo:%d:%s", location.ID, location.ThumbSize), file
	default:
		return "", nil
	}
}

func inlineReactionKey(id int64) string { return fmt.Sprintf("emoji:%d", id) }

func (a *app) loadVisibleImages(out chan<- request) {
	if a.inlineImages == nil || a.inlineLoading || !a.open || a.visibleMessages == 0 || a.pending != "" || len(out) == cap(out) {
		return
	}

	current := a.currentChat()
	for _, msg := range a.log[a.offset:min(len(a.log), a.offset+min(a.visibleMessages, 12))] {
		if key, file := inlineAttachment(msg.attachment); key != "" && a.inlineImages.Start(key) {
			a.inlineLoading = true

			out <- request{kind: "inlineImage", chat: current, anchor: msg.id, attachment: file, text: key}

			return
		}

		if a.textReactions {
			continue
		}

		for _, reaction := range msg.messageReactions {
			if reaction.custom != 0 {
				key := inlineReactionKey(reaction.custom)
				if a.inlineImages.Start(key) {
					a.inlineLoading = true

					out <- request{kind: "inlineImage", chat: current, reactionDocument: reaction.custom, text: key}

					return
				}
			}
		}
	}
}

type imageBuffer struct{ bytes.Buffer }

func (buf *imageBuffer) Write(p []byte) (int, error) {
	if len(p) > maxInlineDownload-buf.Len() {
		return 0, errors.New("thumbnail exceeds the download limit")
	}

	return buf.Buffer.Write(p)
}

func fetchInlineImage(ctx context.Context, client *telegram.Client, req request) *tui.InlineImage {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	if req.reactionDocument != 0 {
		path, err := downloadReactionImage(ctx, client, req.reactionDocument)
		if err != nil {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = file.Close() }()

		preview, _ := tui.DecodeInlineImage(file, 1, 1)

		return preview
	}

	file := req.attachment
	if len(file.inlineData) > 0 {
		preview, _ := tui.DecodeInlineImage(bytes.NewReader(file.inlineData), 12, 4)
		return preview
	}

	invoker, err := client.FileDC(ctx, file.dc)
	if err != nil {
		return nil
	}

	stream := client.DownloadVia(invoker, file.location).
		WithRefresh(fileref.Message(client.API(), req.chat.input, req.anchor, file.location)).
		WithSize(file.size)

	var data imageBuffer
	if _, err := stream.Stream(ctx, &data); err != nil {
		return nil
	}

	preview, _ := tui.DecodeInlineImage(&data, 12, 4)

	return preview
}

func (a *app) inlineReactionLine(msg message, width int) string {
	if a.textReactions {
		return tui.ReactionLine(textReactionSummary(msg.reactions), width, true)
	}

	var (
		badges        []tui.ReactionBadge
		missingCustom int
	)

	hasImages := false

	for _, reaction := range msg.messageReactions {
		if reaction.custom != 0 {
			image := a.inlineImages.Get(inlineReactionKey(reaction.custom))
			if image != nil {
				badges = append(badges, tui.ReactionBadge{Label: "custom", Count: reaction.count, Image: image})
				hasImages = true
			} else {
				missingCustom += reaction.count
			}
		} else if reaction.emoji != "" {
			badges = append(badges, tui.ReactionBadge{Label: reaction.emoji, Count: reaction.count})
		}
	}

	if !hasImages {
		return tui.ReactionLine(msg.reactions, width, false)
	}

	if missingCustom > 0 {
		badges = append(badges, tui.ReactionBadge{Label: "custom", Count: missingCustom})
	}

	return tui.ReactionBadges("↳ ", badges, width)
}
