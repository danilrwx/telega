package telega

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/gotd/td/tg"
)

type forwardSource struct {
	author   peerKey
	name     string
	position chatPosition
	username string
}

func forwardInfo(header tg.MessageFwdHeader) *forwardSource {
	if header.Date == 0 && header.FromID == nil && header.FromName == "" &&
		header.SavedFromPeer == nil && header.SavedFromName == "" {
		return nil
	}

	source := &forwardSource{author: keyFor(header.FromID), name: limitText(strings.TrimSpace(header.FromName), 80)}
	if source.author.id == 0 && source.name == "" {
		source.author = keyFor(header.SavedFromID)
		source.name = limitText(strings.TrimSpace(header.SavedFromName), 80)
	}

	if channel := keyFor(header.FromID); channel.kind == 'c' && header.ChannelPost > 0 {
		source.position = chatPosition{chat: channel, id: header.ChannelPost}
	} else if peer := keyFor(header.SavedFromPeer); peer.id > 0 && header.SavedFromMsgID > 0 {
		source.position = chatPosition{chat: peer, id: header.SavedFromMsgID}
	}

	return source
}

func (a *app) forwardName(source *forwardSource) string {
	if source.name != "" {
		return source.name
	}

	if name := a.authors[source.author]; name != "" {
		return name
	}

	if index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == source.author }); index >= 0 {
		return a.chats[index].title
	}

	if source.author.id > 0 {
		return fmt.Sprintf("Sender #%d", source.author.id)
	}

	return "Unknown sender"
}

func (a *app) jumpToForward(out chan<- request) {
	source := a.log[a.offset].forward
	if source == nil {
		a.status = "Choose a forwarded message"
		return
	}

	target := source.position
	if target.id == 0 {
		a.status = "Telegram did not include a link to the original message"
		return
	}

	if slices.ContainsFunc(a.chats, func(c chat) bool { return c.key == target.chat }) {
		a.requestJump(out, "jump", target)
		return
	}

	if target.chat.kind == 'c' {
		path := fmt.Sprintf("c/%d", target.chat.id)
		if source.username != "" {
			path = url.PathEscape(source.username)
		}

		a.openTarget = fmt.Sprintf("https://t.me/%s/%d", path, target.id)

		return
	}

	a.status = "Original chat is outside the loaded chat list"
}
