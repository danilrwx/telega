package telega

import "time"

func demoChats() []chat {
	return []chat{
		{key: peerKey{'g', 1}, title: "Observatory Lab", group: true, unread: unreadState{count: 3, inboxID: 7, topID: 11}},
		{key: peerKey{'u', 2}, title: "Saved Messages"},
		{key: peerKey{'u', 3}, title: "Mira Chen", unread: unreadState{count: 2}},
		{key: peerKey{'c', 4}, title: "Release Notes", unread: unreadState{count: 8}},
		{key: peerKey{'g', 5}, title: "OpenBSD Workshop", group: true},
		{key: peerKey{'g', 6}, title: "Signal Log", group: true, unread: unreadState{count: 1, mentions: 1}},
		{key: peerKey{'u', 7}, title: "Theo Park"},
		{key: peerKey{'c', 8}, title: "Field Reports", unread: unreadState{count: 14}},
		{key: peerKey{'g', 9}, title: "Terminal Design", group: true},
		{key: peerKey{'g', 10}, title: "Weekend Repair", group: true},
		{key: peerKey{'u', 11}, title: "Nora Voss"},
		{key: peerKey{'g', 12}, title: "Maintenance", group: true},
		{key: peerKey{'g', 13}, title: "Bench Notes", group: true, unread: unreadState{marked: true}},
		{key: peerKey{'g', 14}, title: "Hardware Desk", group: true, unread: unreadState{count: 4}},
		{key: peerKey{'c', 15}, title: "Camp Weather"},
	}
}

func demoHistory(current chat) []message {
	morning := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	if current.key.id != 1 {
		return []message{
			{id: 1, text: "This is a fictional conversation. The demo runs offline.", date: morning},
			{id: 2, text: "Press i to write or h to go back to the chat list.", date: morning.Add(time.Minute)},
		}
	}

	code, entities := codeBlocks("Quick check on the field unit.\n```sh\n./telega --version\n./telega --demo\n```")
	nextMorning := morning.AddDate(0, 0, 1)

	return []message{
		{id: 1, date: morning, authorName: "Mira Chen", text: "The field terminal is on the bench. We can test the ARM build today."},
		{id: 2, date: morning.Add(2 * time.Minute), out: true, text: "Perfect. I packed the power adapter and the serial cable."},
		{id: 3, date: morning.Add(5 * time.Minute), authorName: "Theo Park", text: "The release archive fits on the spare card."},
		{id: 4, date: morning.Add(7 * time.Minute), authorName: "Nora Voss", text: "I'll check that the screen is readable outside."},
		{id: 5, date: morning.Add(9 * time.Minute), authorName: "Theo Park", text: code, spans: messageSpans(entities)},
		{id: 6, date: morning.Add(12 * time.Minute), out: true, text: "[Attachment]", attachment: &attachment{name: "bench-test.png", kind: "Photo"}},
		{id: 7, date: morning.Add(15 * time.Minute), authorName: "Nora Voss", text: "The screen is still readable with the backlight turned down."},
		{id: 8, date: nextMorning.Add(17 * time.Minute), authorName: "Theo Park", text: "Clipboard paste worked on the laptop. The caption came through."},
		{id: 9, date: nextMorning.Add(19 * time.Minute), authorName: "Nora Voss", text: "I'll add that to the setup notes."},
		{id: 10, date: nextMorning.Add(20 * time.Minute), out: true, text: "I can leave a spare card in the field kit.", reactions: "👍 2  ❤️ 1"},
		{id: 11, date: nextMorning.Add(22 * time.Minute), authorName: "Mira Chen", text: "Please do. Then we're ready for Saturday.", reply: messageReply{id: 10}},
	}
}
