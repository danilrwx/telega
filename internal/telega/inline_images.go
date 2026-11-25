package telega

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader/fileref"
	"github.com/gotd/td/tg"
)

const (
	maxInlineDownload = 4 << 20
	avatarGutter      = 5
)

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

	return inlineThumbnail(file)
}

func inlineThumbnail(file *attachment) (string, *attachment) {
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

func inlineAvatarKey(file *attachment) string {
	if file == nil {
		return ""
	}

	photo, ok := file.location.(*tg.InputPeerPhotoFileLocation)
	if !ok {
		return ""
	}

	return fmt.Sprintf("avatar:%d", photo.PhotoID)
}

func inlineReactionKey(id int64) string { return fmt.Sprintf("emoji:%d", id) }

func (a *app) avatarAuthor(msg message) peerKey {
	if msg.service {
		return peerKey{}
	}

	if msg.avatarPeer.id != 0 {
		return msg.avatarPeer
	}

	if msg.out {
		return peerKey{'u', a.accountID}
	}

	return msg.author
}

func (a *app) rememberAvatar(author peerKey, avatar *attachment) {
	if author.id == 0 || avatar == nil {
		return
	}

	if a.authorAvatars == nil || len(a.authorAvatars) >= 512 {
		own := peerKey{'u', a.accountID}
		self := a.authorAvatars[own]
		a.authorAvatars = make(map[peerKey]*attachment)

		if self != nil {
			a.authorAvatars[own] = self
		}
	}

	a.authorAvatars[author] = avatar
}

func (a *app) avatarFile(msg message, current chat) *attachment {
	author := a.avatarAuthor(msg)
	if avatar := a.authorAvatars[author]; avatar != nil {
		return avatar
	}

	if msg.avatar == nil && !msg.out && current.key.kind == 'u' && (author.id == 0 || current.key == author) {
		return current.avatar
	}

	return msg.avatar
}

func (a *app) showMessageAvatars(c chat) bool {
	return !a.display.HideAvatars && (c.key.kind != 'u' || a.display.showDMMessageAvatars())
}

func (a *app) showDirectAvatar(c chat) bool {
	return c.key.kind == 'u' && c.key.id != a.accountID && !a.display.HideAvatars && !a.display.HideDMHeaderAvatar
}

func (a *app) directAvatarFile(c chat) *attachment {
	if !a.showDirectAvatar(c) {
		return nil
	}

	if avatar := a.authorAvatars[c.key]; avatar != nil {
		return avatar
	}

	return c.avatar
}

func (a *app) renderAvatar(file *attachment, peer peerKey, name string) ([2]string, int) {
	if avatar := a.inlineImages.Get(inlineAvatarKey(file)); avatar != nil && len(avatar.Rows) == 2 {
		return [2]string{avatar.Rows[0], avatar.Rows[1]}, avatar.Width + 1
	}

	style := a.avatarStyle(peer, name)

	return [2]string{style + placeholderInitials(name) + "\x1b[0m", style + "    \x1b[0m"}, avatarGutter
}

func (a *app) loadVisibleImages(out chan<- request) {
	if a.inlineImages == nil || a.inlineLoading || !a.open || a.pending != "" || a.transcript != nil || len(out) == cap(out) {
		return
	}

	if a.attachmentGallery != nil {
		a.loadGalleryThumbnails(out)

		return
	}

	if a.chatLink != nil || a.pollDialog != nil || a.pins != nil || a.chatActions != nil || a.settingsOpen || a.attachmentTitle != "" || a.filePicker != nil || a.textAttachmentPrompt != nil || a.reactionAuthors != nil || len(a.confirmDelete) != 0 || a.forwardIDs != nil || a.chatPicker || a.messageSearch != nil || a.reactionPickerOpen || len(a.linkChoices) != 0 {
		return
	}

	current := a.currentChat()
	file := a.directAvatarFile(current)

	if key := inlineAvatarKey(file); key != "" && a.inlineImages.Start(key) {
		a.inlineLoading = true

		out <- request{kind: "inlineImage", attachment: file, text: key}

		return
	}

	visible := a.log[a.offset:min(len(a.log), a.offset+min(a.visibleMessages, 12))]

	for _, msg := range visible {
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

	if !a.showMessageAvatars(current) {
		return
	}

	for _, msg := range visible {
		file := a.avatarFile(msg, current)
		if key := inlineAvatarKey(file); key != "" && a.inlineImages.Start(key) {
			a.inlineLoading = true

			out <- request{kind: "inlineImage", attachment: file, text: key}

			return
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

func fetchInlineImage(ctx context.Context, client *telegram.Client, accountID int64, req request) *tui.InlineImage {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	if req.reactionDocument != 0 {
		path, err := downloadReactionImage(ctx, client, accountID, req.reactionDocument)
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

	var key string

	if accountID != 0 {
		key = strconv.FormatInt(accountID, 10) + "\x00" + req.text
	}

	decode := func(r io.Reader) (*tui.InlineImage, error) { return tui.DecodeInlineImage(r, 12, 4) }
	_, avatar := file.location.(*tg.InputPeerPhotoFileLocation)

	if avatar {
		decode = tui.DecodeInlineAvatar
	}

	preview, _ := tui.CachedImage("telega", key, 0, decode,
		func() ([]byte, error) {
			if len(file.inlineData) > 0 {
				return file.inlineData, nil
			}

			invoker, err := client.FileDC(ctx, file.dc)
			if err != nil {
				return nil, err
			}

			stream := client.DownloadVia(invoker, file.location)
			if !avatar {
				stream.WithRefresh(fileref.Message(client.API(), req.chat.input, req.anchor, file.location)).
					WithSize(file.size)
			}

			var data imageBuffer
			if _, err := stream.Stream(ctx, &data); err != nil {
				return nil, err
			}

			return data.Bytes(), nil
		})

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
