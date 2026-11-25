package telega

import (
	"context"
	"errors"
	"math"
	"net/url"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type chatAction struct{ code, label string }

type chatActionDialog struct {
	chat     chat
	items    []chatAction
	selected int
}

func (a *app) beginChatActions() {
	if a.demo || a.pending != "" || len(a.chats) == 0 || !a.open && len(a.visibleChats()) == 0 {
		return
	}

	c := a.chats[a.selected]

	label := "Pin chat"
	if c.unread.pinned {
		label = "Unpin chat"
	}

	d := &chatActionDialog{chat: c, items: []chatAction{{"toggleChatPin", label}, {"toggleChatMute", "Toggle chat notifications"}}}
	if c.username != "" {
		d.items = append(d.items, chatAction{"copyChatLink", "Copy public chat link"})
	}

	if a.open {
		d.items = append([]chatAction{{"listPins", "Pinned messages"}}, d.items...)
		if len(a.log) > 0 && !a.log[a.offset].service {
			pin := chatAction{"pinMessage", "Pin selected message"}
			if a.log[a.offset].pinned {
				pin = chatAction{"unpinMessage", "Unpin selected message"}
			}

			d.items = append(d.items, pin)
		}
	}

	a.chatActions = d
}

func (a *app) chatActionsInput(k key, out chan<- request) {
	d := a.chatActions

	k = commandKey(k)
	switch {
	case k.name == "escape":
		a.chatActions = nil
	case k.name == "up" || k.text == "k":
		d.selected = max(0, d.selected-1)
	case k.name == "down" || k.text == "j":
		d.selected = min(len(d.items)-1, d.selected+1)
	case k.name == "enter":
		a.chatActions = nil

		code := d.items[d.selected].code
		switch code {
		case "listPins":
			a.showPins(out)
			return
		case "pinMessage", "unpinMessage":
			a.pinSelected(out, code == "pinMessage")
			return
		case "copyChatLink":
			a.copyText = "https://t.me/" + url.PathEscape(d.chat.username)
			return
		}

		a.ask(out, request{kind: code, chat: d.chat})
	}
}

func (d *chatActionDialog) render(width, height int, row func(string, bool, bool, bool)) {
	choices := make([]string, 0, len(d.items))
	for _, action := range d.items {
		choices = append(choices, action.label)
	}

	tui.ListDialog("Chat actions · "+d.chat.title, choices, d.selected, width, height, "", "Esc cancel  Enter apply", row)
}

func toggleChatPin(ctx context.Context, api *tg.Client, req request) error {
	ok, err := api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{
		Pinned: !req.chat.unread.pinned, Peer: &tg.InputDialogPeer{Peer: req.chat.input},
	})
	if err == nil && !ok {
		return errors.New("chat pin was not changed")
	}

	return err
}

func toggleChatMute(ctx context.Context, api *tg.Client, c chat, now time.Time) (string, error) {
	peer := &tg.InputNotifyPeer{Peer: c.input}

	current, err := api.AccountGetNotifySettings(ctx, peer)
	if err != nil {
		return "", err
	}

	var settings tg.InputPeerNotifySettings

	until, _ := current.GetMuteUntil()
	text := "Chat notifications unmuted"

	if int64(until) <= now.Unix() {
		settings.SetMuteUntil(math.MaxInt32)

		text = "Chat notifications muted"
	} else {
		settings.SetMuteUntil(0)
	}

	ok, err := api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{Peer: peer, Settings: settings})
	if err == nil && !ok {
		err = errors.New("chat notifications were not changed")
	}

	return text, err
}
