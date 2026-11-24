package telega

import (
	"bytes"
	"strings"
	"testing"
)

func TestChatPickerFindsArchivedChatAndRestoresSidebar(t *testing.T) {
	source := workflowChat(1, "Current chat")
	archived := workflowChat(3, "Archived workshop")
	a := app{ready: true, open: true, chats: []chat{source}, log: []message{{id: 50, text: "Current conversation"}}}
	out := make(chan request, 1)
	a.input(key{name: "chatPicker"}, out)

	if req := <-out; req.kind != "chatDirectory" {
		t.Fatalf("chat picker should request Telegram dialogs: %+v", req)
	}

	a.input(key{name: "paste", text: "workshop"}, out)
	a.receive(out, serverEvent{kind: "chatDirectory", chats: []chat{archived}})

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil || !strings.Contains(screen.String(), "Archived workshop") ||
		!strings.Contains(screen.String(), "Find a chat") {
		t.Fatalf("missing archived chat from the floating search: %v %s", err, screen.String())
	}

	a.input(key{name: "enter"}, out)

	req := <-out
	if req.kind != "history" || req.chat.key != archived.key || a.currentChat().key != archived.key ||
		a.chatPicker || a.chatQuery != "" || len(a.visibleChats()) != 2 {
		t.Fatalf("opening an older dialog did not restore the sidebar: %+v", req)
	}
}
