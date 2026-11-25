package telega

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestPinsListLoadsScopedResultsAndJumpsToOlderMessage(t *testing.T) {
	current := workflowChat(1, "Forum")
	current.forum, current.threadKind, current.threadID = true, forumThread, 7
	a := app{ready: true, open: true, thread: &current, log: []message{{id: 500}}}
	out := make(chan request, 1)
	a.showPins(out)

	req := <-out
	if req.kind != "listPins" || req.pins != a.pins || a.pending != "" {
		t.Fatalf("unexpected list request: %+v", req)
	}

	api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
		q, ok := in.(*tg.MessagesSearchRequest)
		if !ok || q.Peer != current.input || q.Q != "" || q.OffsetID != 0 {
			t.Fatalf("unexpected search request: %+v", in)
		}

		if _, ok := q.Filter.(*tg.InputMessagesFilterPinned); !ok {
			t.Fatalf("expected pinned filter, got %T", q.Filter)
		}

		if top, ok := q.GetTopMsgID(); !ok || top != current.threadID {
			t.Fatalf("expected topic scope, got top=%d present=%v", top, ok)
		}

		peer := &tg.PeerChat{ChatID: current.key.id}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 65, PeerID: peer, Message: "Topic announcement", Pinned: true},
			&tg.Message{ID: 44, PeerID: peer, Message: "Hidden reveal", Pinned: true, Entities: []tg.MessageEntityClass{&tg.MessageEntitySpoiler{Length: 6}}},
		}}

		return nil
	}))
	a.receivePins(runPinsRequest(t.Context(), api, req))

	var rows []string

	a.drawPins(72, 16, func(row string, _, _, _ bool) { rows = append(rows, row) })

	screen := strings.Join(rows, "\n")
	if !strings.Contains(screen, "Pinned messages") || !strings.Contains(screen, "Topic announcement") ||
		!strings.Contains(screen, "[Message with spoiler]") || strings.Contains(screen, "Hidden reveal") {
		t.Fatalf("unexpected pins dialog: %s", screen)
	}

	a.pinsInput(key{name: "down"}, out)
	a.pinsInput(key{name: "enter"}, out)

	jump := <-out
	if jump.kind != "jump" || jump.anchor != 44 || jump.chat.threadID != current.threadID || a.pins != nil || a.jumpOrigin.id != 500 {
		t.Fatalf("unexpected jump: %+v origin=%+v pins=%+v", jump, a.jumpOrigin, a.pins)
	}
}

func TestPinsListPaginatesFromRawMessageIDs(t *testing.T) {
	current := workflowChat(1, "Chat")
	peer := &tg.PeerChat{ChatID: current.key.id}
	api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
		q := in.(*tg.MessagesSearchRequest)
		var items []tg.MessageClass

		switch q.OffsetID {
		case 0:
			for i := range historyLimit - 1 {
				items = append(items, &tg.Message{ID: 90 - i, PeerID: peer})
			}

			items = append(items, &tg.MessageEmpty{ID: 90 - historyLimit + 1})
		case 90 - historyLimit + 1:
			items = []tg.MessageClass{&tg.Message{ID: q.OffsetID, PeerID: peer}, &tg.Message{ID: q.OffsetID - 1, PeerID: peer}}
		default:
			t.Fatalf("unexpected page cursor %d", q.OffsetID)
		}

		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Messages: items}

		return nil
	}))
	a := app{ready: true, open: true, chats: []chat{current}}
	out := make(chan request, 1)
	a.showPins(out)
	a.receivePins(runPinsRequest(t.Context(), api, <-out))

	d := a.pins
	if len(d.messages) != historyLimit-1 || d.next != 90-historyLimit+1 {
		t.Fatalf("unexpected first page: %d items, next=%d", len(d.messages), d.next)
	}

	for range len(d.messages) {
		a.pinsInput(key{name: "down"}, out)
	}

	a.pinsInput(key{name: "enter"}, out)

	next := <-out
	if next.pins != d || next.anchor != d.next || !d.busy {
		t.Fatalf("unexpected second page request: %+v", next)
	}

	a.receivePins(runPinsRequest(t.Context(), api, next))
	if len(d.messages) != historyLimit || d.selected != historyLimit-1 || d.next != 0 || d.messages[d.selected].id != 90-historyLimit {
		t.Fatalf("unexpected appended page: %d items, selected=%d next=%d", len(d.messages), d.selected, d.next)
	}
}

func TestPinsListIgnoresResponsesForClosedDialog(t *testing.T) {
	a := app{ready: true, open: true, chats: []chat{workflowChat(1, "Chat")}}
	out := make(chan request, 1)
	a.showPins(out)

	stale := <-out
	a.pinsInput(key{name: "escape"}, out)
	a.showPins(out)

	fresh := <-out
	a.receivePins(serverEvent{kind: "listPins", pins: stale.pins, chat: stale.chat.key, log: []message{{id: 5}}})
	if a.pins != fresh.pins || !a.pins.busy || len(a.pins.messages) != 0 {
		t.Fatalf("stale results reached the reopened pins dialog: %+v", a.pins)
	}

	a.receivePins(serverEvent{kind: "listPins", pins: fresh.pins, chat: fresh.chat.key, hint: "Permission denied"})
	if a.pins.busy || a.pins.status != "Permission denied" {
		t.Fatalf("error did not finish the active list request: %+v", a.pins)
	}
}

func TestPinSelectedSendsSilentPinAndUnpin(t *testing.T) {
	current := workflowChat(1, "Chat")
	a := app{ready: true, open: true, chats: []chat{current}, log: []message{{id: 30}, {id: 22}}, offset: 1}
	out := make(chan request, 1)

	for _, pin := range []bool{true, false} {
		a.pinSelected(out, pin)

		req := <-out
		if req.kind != "setPin" || a.pending != req.kind || req.pinState != pin || req.anchor != 22 {
			t.Fatalf("unexpected mutation: %+v", req)
		}

		api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
			q, ok := in.(*tg.MessagesUpdatePinnedMessageRequest)
			if !ok || q.Peer != current.input || q.ID != 22 || q.Unpin == pin || q.Silent != pin {
				t.Fatalf("unexpected mutation RPC: %+v", in)
			}

			out.(*tg.UpdatesBox).Updates = &tg.Updates{}

			return nil
		}))
		a.receivePins(runPinsRequest(t.Context(), api, req))

		if a.pending != "" || a.log[0].pinned || a.log[1].pinned != pin {
			t.Fatalf("unexpected result for pin=%t: %q %+v", pin, a.pending, a.log)
		}
	}

	a.pinSelected(out, true)

	denied := tg.NewClient(invokeFunc(func(bin.Encoder, bin.Decoder) error { return errors.New("CHAT_ADMIN_REQUIRED") }))
	a.receivePins(runPinsRequest(t.Context(), denied, <-out))

	if a.pending != "" || a.log[1].pinned || !strings.Contains(a.status, "Could not pin message") {
		t.Fatalf("failed mutation updated local pin state: %q %+v", a.status, a.log)
	}
}

func TestPinsLiveUpdatesApplyToCorrectPeer(t *testing.T) {
	current := workflowChat(1, "Chat")
	a := app{ready: true, open: true, chats: []chat{current}, log: []message{{id: 22, pinned: true}, {id: 17}}}
	out := make(chan request, 1)
	a.showPins(out)

	req := <-out
	events := make(chan serverEvent, 1)
	dispatch := tg.NewUpdateDispatcher()
	registerPinUpdates(dispatch, events)

	for _, update := range []tg.UpdateClass{
		&tg.UpdatePinnedChannelMessages{ChannelID: current.key.id, Pinned: true, Messages: []int{17}},
		&tg.UpdatePinnedMessages{Peer: &tg.PeerChat{ChatID: current.key.id}, Messages: []int{22}},
	} {
		if err := dispatch.Handle(t.Context(), &tg.UpdateShort{Update: update}); err != nil {
			t.Fatal(err)
		}

		a.receivePins(<-events)
	}

	a.receivePins(serverEvent{kind: "listPins", chat: current.key, pins: req.pins, log: []message{{id: 22, pinned: true}}})
	if !a.pins.changed || a.pins.busy || len(a.pins.messages) != 0 || slices.ContainsFunc(a.log, func(m message) bool { return m.pinned }) {
		t.Fatalf("unexpected pins after live updates: dialog=%+v log=%+v", a.pins, a.log)
	}
}
