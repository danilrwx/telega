package telega

import (
	"context"
	"slices"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type pinDialog struct {
	scope          chat
	messages       []message
	selected, next int
	busy, changed  bool
	status         string
}

func (a *app) showPins(out chan<- request) {
	if !a.ready || !a.open || a.demo || a.pending != "" || a.currentChat().input == nil {
		return
	}

	a.pins = &pinDialog{scope: a.currentChat()}
	a.pins.load(out, 0)
}

func (d *pinDialog) load(out chan<- request, anchor int) {
	d.busy, d.status = true, "Loading pinned messages…"
	out <- request{kind: "listPins", chat: d.scope, pins: d, anchor: anchor}
}

func (a *app) pinSelected(out chan<- request, pin bool) {
	if !a.ready || !a.open || a.demo || a.pending != "" || len(a.log) == 0 || a.currentChat().input == nil {
		return
	}

	msg := a.log[a.offset]
	if msg.id <= 0 || msg.service {
		return
	}

	a.ask(out, request{kind: "setPin", chat: a.currentChat(), anchor: msg.id, pinState: pin})
}

func (a *app) pinsInput(k key, out chan<- request) {
	d := a.pins
	if d == nil {
		return
	}

	k = commandKey(k)
	if k.name == "escape" || (k.name == "" && k.text == "q") {
		a.pins = nil
		return
	}

	last := len(d.messages) - 1
	if d.next != 0 && !d.changed {
		last++ // Load older pins is the last row.
	}

	switch {
	case k.name == "up" || (k.name == "" && k.text == "k"):
		d.selected = max(0, d.selected-1)
	case k.name == "down" || (k.name == "" && k.text == "j"):
		d.selected = min(max(0, last), d.selected+1)
	case k.name == "" && k.text == "r":
		if !d.busy {
			a.showPins(out)
		}
	case k.name == "enter" && !d.busy:
		if d.selected < len(d.messages) {
			msg := d.messages[d.selected]
			a.pins = nil
			scope := d.scope
			a.jumpToSearchHit(out, messageHit{chat: scope, msg: msg}, scope.threadID != 0 || !scope.forum || msg.reply.topic == 0)
		} else if d.next != 0 && !d.changed {
			d.load(out, d.next)
		}
	}
}

func (a *app) drawPins(width, height int, row func(string, bool, bool, bool)) {
	d := a.pins
	labels := make([]string, 0, len(d.messages)+1)

	for _, msg := range d.messages {
		labels = append(labels, msg.date.Local().Format("02 Jan 15:04")+" · "+messageExcerpt(msg, 0))
	}

	if d.next != 0 && !d.changed {
		labels = append(labels, "Load older pins…")
	}

	status := d.status
	if status == "" && d.changed {
		status = "Pinned messages changed · r refresh"
	}

	if status == "" && len(labels) == 0 {
		status = "No pinned messages"
	}

	hint := "jk choose  Enter jump  r refresh  Esc back"
	if width < 42 {
		hint = "jk Enter r Esc"
	}

	tui.ListDialog("Pinned messages", labels, d.selected, width, height, status, hint, row)
}

func (a *app) receivePins(e serverEvent) {
	switch e.kind {
	case "listPins":
		d := a.pins
		if d == nil || d != e.pins || !a.inConversation(e) {
			return
		}

		d.busy, d.status = false, e.hint
		if e.hint == "" && !d.changed {
			d.messages = append(d.messages, e.log...)
			d.next = e.step
			d.selected = min(d.selected, max(0, len(d.messages)-1))
		}
	case "setPin":
		if a.pending != "setPin" || !a.inConversation(e) {
			return
		}

		a.pending, a.status = "", e.hint
		if e.hint != "" {
			return
		}

		a.updatePins(e.chat, []int{e.anchor}, e.pinState)

		a.status = "Message unpinned"
		if e.pinState {
			a.status = "Message pinned"
		}
	case "pinnedMessages":
		a.updatePins(e.chat, e.ids, e.pinState)
	}
}

func (a *app) updatePins(peer peerKey, ids []int, pin bool) {
	if a.open && a.currentChat().key == peer {
		for i := range a.log {
			if slices.Contains(ids, a.log[i].id) {
				a.log[i].pinned = pin
			}
		}
	}

	if d := a.pins; d != nil && d.scope.key == peer {
		d.changed = true
		if !pin {
			d.messages = slices.DeleteFunc(d.messages, func(m message) bool { return slices.Contains(ids, m.id) })
		}

		d.selected = min(d.selected, max(0, len(d.messages)-1))
	}
}

func runPinsRequest(ctx context.Context, api *tg.Client, req request) serverEvent {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	e := serverEvent{
		kind: req.kind, chat: req.chat.key, threadID: req.chat.threadID,
		pins: req.pins, anchor: req.anchor, pinState: req.pinState,
	}
	if req.kind == "listPins" {
		var err error

		e.log, e.step, err = loadPins(ctx, api, req)
		if err != nil {
			e.hint = "Could not load pinned messages: " + err.Error()
		}

		return e
	}

	_, err := api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
		Peer: req.chat.input, ID: req.anchor, Unpin: !req.pinState, Silent: req.pinState,
	})
	if err != nil {
		if req.pinState {
			e.hint = "Could not pin message: " + err.Error()
		} else {
			e.hint = "Could not unpin message: " + err.Error()
		}
	}

	return e
}

func loadPins(ctx context.Context, api *tg.Client, req request) ([]message, int, error) {
	query := &tg.MessagesSearchRequest{
		Peer: req.chat.input, Filter: &tg.InputMessagesFilterPinned{}, OffsetID: req.anchor, Limit: historyLimit,
	}
	if req.chat.threadID != 0 {
		query.SetTopMsgID(req.chat.threadID)
	}

	result, err := api.MessagesSearch(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, 0, nil
	}

	entities := tg.EntitiesFrom(data.GetUsers(), data.GetChats())
	items := data.GetMessages()
	pins := make([]message, 0, len(items))
	next := 0

	for _, item := range items {
		id := item.GetID()
		if id <= 0 || (req.anchor != 0 && id >= req.anchor) {
			continue
		}

		if next == 0 || id < next {
			next = id
		}

		if key, msg, ok := unwrapMessage(item); ok && key == req.chat.key {
			pins = append(pins, resolveAuthor(msg, entities))
		}
	}

	if len(items) < historyLimit {
		next = 0
	}

	return pins, next, nil
}

func registerPinUpdates(dispatch tg.UpdateDispatcher, events chan<- serverEvent) {
	dispatch.OnPinnedMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdatePinnedMessages) error {
		return emit(ctx, events, serverEvent{kind: "pinnedMessages", chat: keyFor(update.Peer), ids: update.Messages, pinState: update.Pinned})
	})
	dispatch.OnPinnedChannelMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdatePinnedChannelMessages) error {
		return emit(ctx, events, serverEvent{kind: "pinnedMessages", chat: peerKey{'c', update.ChannelID}, ids: update.Messages, pinState: update.Pinned})
	})
}
