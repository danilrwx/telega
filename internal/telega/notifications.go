package telega

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gotd/td/tg"
)

type desktopNotice struct {
	title, body string
}

type noticeCheck struct {
	notice desktopNotice
	chat   chat
	topic  int
}

func notificationSource(key peerKey, entities tg.Entities) chat {
	c := chat{key: key, title: entityName(key, entities)}

	switch key.kind {
	case 'u':
		if u := entities.Users[key.id]; u != nil && !u.Min && u.AccessHash != 0 {
			c.input = &tg.InputPeerUser{UserID: key.id, AccessHash: u.AccessHash}
		}
	case 'g':
		c.group, c.input = true, &tg.InputPeerChat{ChatID: key.id}
	case 'c':
		if ch := entities.Channels[key.id]; ch != nil && !ch.Min && ch.AccessHash != 0 {
			c.group, c.forum = ch.Megagroup, ch.Forum
			c.input = &tg.InputPeerChannel{ChannelID: key.id, AccessHash: ch.AccessHash}
		}
	}

	return c
}

func (a *app) noticeCheck(e serverEvent, notice desktopNotice) noticeCheck {
	c := e.source
	if c.input == nil {
		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			c = a.chats[i]
		} else if a.thread != nil && a.thread.key == e.chat {
			c = *a.thread
		}
	}

	topic := e.threadID
	if e.kind == "new" {
		msg := e.log[0]

		topic = msg.reply.topic
		if msg.topicStart {
			topic = msg.id
		}
	}

	if c.forum && topic == 0 {
		topic = 1 // General.
	}

	return noticeCheck{notice: notice, chat: c, topic: topic}
}

type muteSetting struct {
	expires time.Time
	until   int
	set     bool
}

func checkNotifications(ctx context.Context, api *tg.Client, checks <-chan noticeCheck, events chan<- serverEvent, changed *atomic.Bool) {
	cache := make(map[chatPosition]muteSetting)

	for {
		select {
		case <-ctx.Done():
			return
		case check := <-checks:
			if changed.Swap(false) {
				clear(cache)
			}

			if notificationAllowed(ctx, api, check, cache) && !changed.Load() {
				if emit(ctx, events, serverEvent{kind: "notify", notice: check.notice}) != nil {
					return
				}
			}
		}
	}
}

func notificationAllowed(ctx context.Context, api *tg.Client, check noticeCheck, cache map[chatPosition]muteSetting) bool {
	c := check.chat
	if c.input == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	get := func(key chatPosition, target tg.InputNotifyPeerClass) (muteSetting, error) {
		if cached := cache[key]; time.Now().Before(cached.expires) {
			return cached, nil
		}

		settings, err := api.AccountGetNotifySettings(ctx, target)
		if err != nil {
			return muteSetting{}, err
		}

		until, set := settings.GetMuteUntil()
		value := muteSetting{expires: time.Now().Add(time.Minute), until: until, set: set}

		if len(cache) >= 2*chatLimit {
			clear(cache)
		}

		cache[key] = value

		return value, nil
	}

	if check.topic != 0 {
		setting, err := get(chatPosition{chat: c.key, id: check.topic}, &tg.InputNotifyForumTopic{Peer: c.input, TopMsgID: check.topic})
		if err != nil || setting.set {
			return err == nil && int64(setting.until) <= time.Now().Unix()
		}
	}

	setting, err := get(chatPosition{chat: c.key}, &tg.InputNotifyPeer{Peer: c.input})
	if err != nil || setting.set {
		return err == nil && int64(setting.until) <= time.Now().Unix()
	}

	var defaults tg.InputNotifyPeerClass = &tg.InputNotifyBroadcasts{}

	kind := byte('c')

	switch {
	case c.key.kind == 'u':
		kind, defaults = 'u', &tg.InputNotifyUsers{}
	case c.group:
		kind, defaults = 'g', &tg.InputNotifyChats{}
	}

	setting, err = get(chatPosition{chat: peerKey{kind: kind}}, defaults)

	return err == nil && setting.set && int64(setting.until) <= time.Now().Unix()
}

func notifyDesktop(ctx context.Context, executable string, notices <-chan desktopNotice, failures chan<- desktopNotice) {
	var env []string

	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		runtime := os.Getenv("XDG_RUNTIME_DIR")
		if runtime == "" {
			runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
		}

		bus := filepath.Join(runtime, "bus")
		if info, err := os.Stat(bus); err == nil && info.Mode()&os.ModeSocket != 0 {
			env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case notice := <-notices:
			deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
			cmd := exec.CommandContext(deadline, executable, "--app-name=telega", "--", notice.title, notice.body)
			cmd.Env = env
			err := cmd.Run()

			cancel()

			if err != nil {
				select {
				case <-ctx.Done():
					return
				case failures <- notice:
				default:
				}
			}
		}
	}
}

func (a *app) alertInTerminal(out io.Writer, notice desktopNotice) {
	_, _ = io.WriteString(out, "\a")
	a.noticeStatus = "Terminal alert · " + notice.title + " · " + notice.body
}

func (a *app) deliverNotice(out io.Writer, desktop chan<- desktopNotice, notice desktopNotice, inTmux bool) {
	select {
	case desktop <- notice:
		if !inTmux {
			return
		}
	default:
	}

	a.alertInTerminal(out, notice)
}

func (a *app) canSeeHistory() bool {
	return a.open && a.focused && !a.searching && a.filePicker == nil && a.textAttachmentPrompt == nil && a.attachmentTitle == "" &&
		len(a.topicEntries) == 0 && a.serverResults == nil && a.forwardIDs == nil && a.confirmDelete == nil && a.linkChoices == nil
}

func (a *app) notification(e serverEvent, viewportOK bool) desktopNotice {
	if !a.ready || a.demo {
		return desktopNotice{}
	}

	var (
		body, actorName string
		actor           peerKey
	)

	switch e.kind {
	case "new":
		if len(e.log) == 0 || e.log[0].out {
			return desktopNotice{}
		}

		if viewportOK && a.canSeeHistory() && a.currentChat().key == e.chat && a.currentChat().includesMessage(e.log[0]) &&
			!a.newerTrimmed && a.offset == 0 && a.pending != "older" {
			return desktopNotice{}
		}

		body = "New message"
		actor, actorName = e.log[0].author, e.log[0].authorName
	case "reactions":
		if e.reactionAt == 0 || !a.newReaction(e) {
			return desktopNotice{}
		}

		if a.open && a.currentChat().key == e.chat {
			index := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor })
			if index >= 0 && (!a.log[index].out || viewportOK && a.canSeeHistory() &&
				index >= a.offset && index-a.offset < a.visibleMessages) {
				return desktopNotice{}
			}
		}

		body = "Reaction on your message"

		if emoji := noticeLabel(e.reactionEmoji); emoji != "" {
			if a.textReactions {
				emoji = reactionName(emoji)
			}

			body = "Reaction " + emoji + " on your message"
		}

		actor, actorName = e.reactionActor, e.reactionAuthor
	default:
		return desktopNotice{}
	}

	chatTitle := e.source.title

	if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
		c := a.chats[i]
		if e.kind == "new" && e.log[0].id <= max(c.unread.topID, c.unread.inboxID) {
			return desktopNotice{}
		}

		chatTitle = c.title
	}

	if actorName == "" {
		actorName = a.authors[actor]
		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == actor }); actorName == "" && i >= 0 {
			actorName = a.chats[i].title
		}
	}

	if actorName == "" && actor.id != 0 && actor != e.chat {
		actorName = fmt.Sprintf("Sender #%d", actor.id)
	}

	chatTitle, actorName = noticeLabel(chatTitle), noticeLabel(actorName)
	if chatTitle == "" {
		chatTitle = "Telegram"
	} else if actorName == chatTitle {
		actorName = ""
	}

	if actorName != "" {
		chatTitle = actorName + " · " + chatTitle
	}

	return desktopNotice{title: chatTitle, body: body}
}

func noticeLabel(text string) string {
	return limitText(strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " "), 90)
}

func (a *app) newReaction(e serverEvent) bool {
	pos := chatPosition{chat: e.chat, id: e.anchor}

	previous, known := a.recentReactionAlerts[pos]
	if e.reactionAt <= previous {
		return false
	}

	if a.recentReactionAlerts == nil || !known && len(a.recentReactionAlerts) >= historyWindow {
		a.recentReactionAlerts = make(map[chatPosition]int)
	}

	a.recentReactionAlerts[pos] = e.reactionAt

	return true
}
