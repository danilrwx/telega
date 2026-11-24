package telega

import (
	"fmt"
	"slices"
	"strings"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
	"github.com/rivo/uniseg"
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

	a.composing = true
	if image {
		a.beginAttachment(false)
	}
}

func (a *app) clearReply() {
	a.replyID, a.replyTopic, a.replyPreview = 0, 0, ""
}

func (a *app) messageJumpHints() string {
	var hints []string
	if len(a.jumps) > a.threadJumpBase || a.thread != nil {
		hints = append(hints, "H go back")
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

func (a *app) replyLabel(reply messageReply, width int) string {
	const marker = "↪ [J] "

	quote, prefix := reply.quote, "Reply to "
	current := a.currentChat()

	if reply.peer.id == 0 || reply.peer == current.key {
		if i := slices.IndexFunc(a.log, func(candidate message) bool { return candidate.id == reply.id }); i >= 0 {
			target := a.log[i]
			if quote == "" {
				quote = messageExcerpt(target, a.revealID)
			}

			author := a.authorLabel(target)
			if target.out {
				author = "You"
			} else if author == "" && !current.group && !target.service {
				author = current.title
			}

			author = strings.Join(strings.Fields(author), " ")
			if author != "" {
				nameWidth := max(1, (width-uniseg.StringWidth(marker)-3)/2)
				if uniseg.StringWidth(author) > nameWidth {
					author = shortSender(author, nameWidth)
				}

				prefix = author + " · "
			}
		}
	}

	if quote == "" {
		quote = fmt.Sprintf("#%d", reply.id)
	}

	return marker + prefix + quote
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
		headers = append(headers, "\x1b[36m"+tui.Wrap(label, width, 1)[0]+"\x1b[0m")
	}

	if msg.reply.id != 0 && limit-len(headers) > 1 {
		headers = append(headers, "\x1b[36m"+tui.Wrap(a.replyLabel(msg.reply, width), width, 1)[0]+"\x1b[0m")
	}

	var thumbnail []string

	if key, _ := inlineAttachment(msg.attachment); key != "" {
		if image := a.inlineImages.Get(key); image != nil && image.Width <= width && len(image.Rows) < limit-len(headers) {
			thumbnail = image.Rows
			limit -= len(thumbnail)
		}
	}

	lines := append(headers, formattedMessage(msg, width, limit-len(headers), a.revealID == msg.id, a.palette())...)

	lines = append(lines, thumbnail...)
	if showReactions {
		lines = append(lines, a.palette().reaction+a.inlineReactionLine(msg, width)+"\x1b[0m")
	}

	return lines
}
