package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gotd/td/tg"
)

type messageReply struct {
	id, topic int
	peer      peerKey
	quote     string
}

func replyInfo(raw tg.MessageReplyHeaderClass) messageReply {
	header, ok := raw.(*tg.MessageReplyHeader)
	if !ok {
		return messageReply{}
	}

	result := messageReply{id: header.ReplyToMsgID, peer: keyFor(header.ReplyToPeerID)}
	if header.ForumTopic {
		result.topic = header.ReplyToTopID
		if result.topic == 0 {
			result.topic = header.ReplyToMsgID
		}
	}

	if !slices.ContainsFunc(messageSpans(header.QuoteEntities), func(s textSpan) bool { return s.style == spoilerStyle }) {
		result.quote = limitText(strings.Join(strings.Fields(header.QuoteText), " "), 100)
	}

	return result
}

func (req request) inputReply() tg.InputReplyToClass {
	if req.replyID == 0 {
		return nil
	}

	reply := &tg.InputReplyToMessage{ReplyToMsgID: req.replyID}
	if req.replyTopic > 1 && req.replyTopic != req.replyID {
		reply.SetTopMsgID(req.replyTopic)
	}

	return reply
}

func messageExcerpt(msg message, revealID int) string {
	if msg.id != revealID && slices.ContainsFunc(msg.spans, func(s textSpan) bool { return s.style == spoilerStyle }) {
		return "[Message with spoiler]"
	}

	text := strings.Join(strings.Fields(msg.text), " ")
	if msg.attachment != nil && (text == "" || text == "[Attachment]") {
		text = msg.attachment.name
	}

	if text == "" {
		text = fmt.Sprintf("message #%d", msg.id)
	}

	return limitText(text, 100)
}

func (a *app) beginReply(image bool) {
	if a.pending != "" || len(a.log) == 0 || image && a.demo {
		return
	}

	msg := a.log[a.offset]
	a.replyID, a.replyTopic, a.replyPreview = msg.id, msg.reply.topic, messageExcerpt(msg, a.revealID)
	a.status = ""
	a.clearDraft()
	a.composing, a.imagePath = !image, image
}

func (a *app) clearReply() {
	a.replyID, a.replyTopic, a.replyPreview = 0, 0, ""
}

func (a *app) messageJumpHints() string {
	var hints []string
	if len(a.jumps) > 0 {
		hints = append(hints, "Ctrl+O go back")
	}

	if len(a.log) > 0 {
		msg := a.log[a.offset]
		if msg.reply.id != 0 {
			hints = append(hints, "J open quoted message")
		}

		if msg.forward != nil {
			if msg.forward.position.id != 0 {
				hints = append(hints, "F open source")
			} else {
				hints = append(hints, "No source link")
			}
		}
	}

	return strings.Join(hints, "   ")
}

func (a *app) messageLines(msg message, width, limit int) []string {
	var headers []string
	if author := a.authorLabel(msg); author != "" && limit > 1 {
		headers = append(headers, "\x1b[1m"+wrap(author, width, 1)[0]+"\x1b[0m")
	}

	if msg.forward != nil && limit-len(headers) > 1 {
		label := "⇉ "
		if msg.forward.position.id != 0 {
			label += "[F] "
		}

		label += "Forwarded from " + a.forwardName(msg.forward)
		headers = append(headers, "\x1b[36m"+wrap(label, width, 1)[0]+"\x1b[0m")
	}

	if msg.reply.id != 0 && limit-len(headers) > 1 {
		quote := msg.reply.quote
		if quote == "" && (msg.reply.peer.id == 0 || msg.reply.peer == a.chats[a.selected].key) {
			if i := slices.IndexFunc(a.log, func(candidate message) bool { return candidate.id == msg.reply.id }); i >= 0 {
				quote = messageExcerpt(a.log[i], a.revealID)
			}
		}

		if quote == "" {
			quote = fmt.Sprintf("#%d", msg.reply.id)
		}

		headers = append(headers, "\x1b[36m"+wrap("↪ [J] Reply to "+quote, width, 1)[0]+"\x1b[0m")
	}

	return append(headers, formattedMessage(msg, width, limit-len(headers), a.revealID == msg.id)...)
}
