package telega

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gotd/td/tg"
)

type messageReply struct {
	id, topic, top int
	peer           peerKey
	quote          string
}

func replyInfo(raw tg.MessageReplyHeaderClass) messageReply {
	header, ok := raw.(*tg.MessageReplyHeader)
	if !ok {
		return messageReply{}
	}

	result := messageReply{id: header.ReplyToMsgID, top: header.ReplyToTopID, peer: keyFor(header.ReplyToPeerID)}
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
	replyID, topic := req.replyID, req.replyTopic
	if req.chat.threadID != 0 {
		if req.chat.threadKind == forumThread {
			topic = req.chat.threadID
		}

		if replyID == 0 && (req.chat.threadKind != forumThread || req.chat.threadID != 1) {
			replyID = req.chat.threadID
		}
	}

	if replyID == 0 {
		return nil
	}

	reply := &tg.InputReplyToMessage{ReplyToMsgID: replyID}
	if req.chat.threadKind != commentThread && topic > 1 && topic != replyID {
		reply.SetTopMsgID(topic)
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
	a.composing, a.imagePath, a.filePath = !image, image, false
}

func (a *app) clearReply() {
	a.replyID, a.replyTopic, a.replyPreview = 0, 0, ""
}

func (a *app) messageJumpHints() string {
	var hints []string
	if len(a.jumps) > a.threadJumpBase || a.thread != nil {
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

	if a.thread == nil {
		if a.currentChat().forum {
			hints = append(hints, "t topics")
		}

		if len(a.log) > 0 && a.log[a.offset].comments {
			hints = append(hints, fmt.Sprintf("c comments (%d)", a.log[a.offset].commentCount))
		}
	}

	return strings.Join(hints, "   ")
}

func (a *app) messageLines(msg message, width, limit int) []string {
	showReactions := msg.reactions != "" && limit > 1
	if showReactions {
		limit--
	}

	var headers []string
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
		if quote == "" && (msg.reply.peer.id == 0 || msg.reply.peer == a.currentChat().key) {
			if i := slices.IndexFunc(a.log, func(candidate message) bool { return candidate.id == msg.reply.id }); i >= 0 {
				quote = messageExcerpt(a.log[i], a.revealID)
			}
		}

		if quote == "" {
			quote = fmt.Sprintf("#%d", msg.reply.id)
		}

		headers = append(headers, "\x1b[36m"+wrap("↪ [J] Reply to "+quote, width, 1)[0]+"\x1b[0m")
	}

	lines := append(headers, formattedMessage(msg, width, limit-len(headers), a.revealID == msg.id)...)
	if showReactions {
		summary := wrap("↳ "+msg.reactions, width, 2)
		line := summary[0]

		if len(summary) == 2 && width > 1 {
			line = strings.TrimRight(wrap(line, width-1, 1)[0], " ") + "…"
		}

		lines = append(lines, "\x1b[33m"+line+"\x1b[0m")
	}

	return lines
}
