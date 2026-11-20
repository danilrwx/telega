package telega

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestRetryKeepsAttachmentAndRandomID(t *testing.T) {
	c := chat{key: peerKey{'u', 8}, title: "Example", input: &tg.InputPeerUser{UserID: 8}}
	a := app{ready: true, open: true, chats: []chat{c}}
	out := make(chan request, 1)

	path := filepath.Join(t.TempDir(), "trace.log")
	if err := os.WriteFile(path, []byte("fixture log"), 0o600); err != nil {
		t.Fatal(err)
	}

	upload, err := stageAttachment(t.Context(), request{kind: "file", text: path}, "")
	if err != nil {
		t.Fatal(err)
	}

	a.staged, a.composing = upload, true
	a.input(key{text: "caption"}, out)
	a.input(key{name: "enter"}, out)

	first := <-out
	if first.kind != "send" || first.randomID == 0 {
		t.Fatalf("missing send identity: %+v", first)
	}

	a.receive(out, serverEvent{kind: "offline", text: "lost connection"})
	a.input(key{text: "r"}, out)

	if req := <-out; req.kind != "reconnect" {
		t.Fatalf("wrong reconnect request: %+v", req)
	}

	a.receive(out, serverEvent{kind: "ready"})
	<-out
	a.receive(out, serverEvent{kind: "chats", chats: []chat{c}})

	if req := <-out; req.kind != "history" || req.chat.key != c.key {
		t.Fatalf("wrong resumed chat: %+v", req)
	}

	a.receive(out, serverEvent{kind: "history", chat: c.key})
	a.input(key{name: "enter"}, out)

	retry := <-out
	if retry.randomID != first.randomID || retry.upload != first.upload || retry.text != "caption" {
		t.Fatalf("reconnect lost the draft or send ID: %+v", retry)
	}

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		switch query := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			if string(query.Bytes) != "fixture log" {
				t.Fatal("the document upload changed the file bytes")
			}

			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			doc, ok := query.Media.(*tg.InputMediaUploadedDocument)
			if !ok || !doc.ForceFile || doc.Attributes[0].(*tg.DocumentAttributeFilename).FileName != "trace.log" ||
				query.RandomID != retry.randomID || query.Message != retry.text {
				t.Fatal("retry did not use the original document payload")
			}

			output.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 14}
		default:
			t.Fatalf("unexpected request %T", input)
		}

		return nil
	}))
	if _, err := sendMessage(t.Context(), api, retry); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalScreenshotCanBeSentAsDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(path, []byte("original PNG bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := app{
		ready: true, open: true, composing: true, chats: []chat{{input: &tg.InputPeerSelf{}}},
		staged: &uploadAttachment{path: path, format: "png", temporary: true},
	}
	out := make(chan request, 1)
	a.input(key{name: "fileMode"}, out)
	a.input(key{name: "enter"}, out)

	req := <-out
	if !req.asFile {
		t.Fatal("Ctrl+B did not select document mode")
	}

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		switch query := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			if string(query.Bytes) != "original PNG bytes" {
				t.Fatal("the screenshot was changed before upload")
			}

			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			doc, ok := query.Media.(*tg.InputMediaUploadedDocument)
			if !ok || !doc.ForceFile || doc.MimeType != "image/png" ||
				doc.Attributes[0].(*tg.DocumentAttributeFilename).FileName != "Screenshot.png" {
				t.Fatal("the screenshot was not sent as an original PNG file")
			}

			output.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 25}
		default:
			t.Fatalf("unexpected request %T", input)
		}

		return nil
	}))
	if _, err := sendMessage(t.Context(), api, req); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveAndDialogCursor(t *testing.T) {
	user := &tg.PeerUser{UserID: 4}
	pages := 0

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		switch query := input.(type) {
		case *tg.MessagesGetDialogsRequest:
			if f, set := query.GetFolderID(); !set || f != 1 {
				t.Fatal("archive must be requested using folder 1")
			}

			if pages == 1 && (!query.ExcludePinned || query.OffsetID != 71 || query.OffsetDate != 1234) {
				t.Fatalf("wrong dialog offset: %+v", query)
			}

			pages++

			dialogs := make([]tg.DialogClass, chatLimit)
			for i := range dialogs {
				dialogs[i] = &tg.Dialog{Peer: &tg.PeerUser{UserID: int64(i + 20)}}
			}

			dialogs[len(dialogs)-1] = &tg.Dialog{Peer: user, TopMessage: 71}
			output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogsSlice{
				Count: chatLimit * 2, Dialogs: dialogs,
				Messages: []tg.MessageClass{&tg.Message{PeerID: user, ID: 71, Date: 1234}},
				Users:    []tg.UserClass{&tg.User{ID: 4, AccessHash: 44, FirstName: "Ada"}},
			}
		case *tg.FoldersEditPeerFoldersRequest:
			if len(query.FolderPeers) != 1 || query.FolderPeers[0].FolderID != 0 {
				t.Fatalf("unarchive must move the peer to folder 0: %+v", query)
			}

			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			t.Fatalf("unexpected request %T", input)
		}

		return nil
	}))
	a := app{ready: true}
	out := make(chan request, 1)
	a.input(key{name: "tab"}, out)
	first := <-out

	chats, next, err := loadDialogPage(t.Context(), api, first.page)
	if err != nil || len(chats) != 1 || next.input == nil {
		t.Fatalf("archive cursor was lost: %v, %+v", err, next)
	}

	first.page.next = next
	a.receive(out, serverEvent{kind: "chats", page: first.page, chats: chats})
	a.input(key{text: "]"}, out)

	page := <-out
	if _, _, err := loadDialogPage(t.Context(), api, page.page); err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: "chats", page: page.page, chats: chats})
	a.input(key{text: "A"}, out)

	move := <-out
	if err := moveDialog(t.Context(), api, move); err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: "archiveChat", chat: move.chat.key, page: move.page})

	if len(a.chats) != 0 || a.pending != "" {
		t.Fatal("a moved archive entry should disappear immediately")
	}
}

func TestTopicAndCommentQueriesStayInTheThread(t *testing.T) {
	parent := chat{
		key: peerKey{'c', 5}, title: "Example forum", group: true, forum: true,
		input: &tg.InputPeerChannel{ChannelID: 5, AccessHash: 55},
	}
	out := make(chan request, 1)
	a := app{ready: true, open: true, chats: []chat{parent}, log: []message{{id: 80}}}
	a.input(key{text: "t"}, out)

	topics := <-out
	if topics.kind != "topics" {
		t.Fatalf("expected topics request: %+v", topics)
	}

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		switch query := input.(type) {
		case *tg.MessagesGetForumTopicsRequest:
			if query.Peer != parent.input {
				t.Fatal("topic list used the wrong forum")
			}

			*output.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{
				Count: 2, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 30, TopMessage: 35, Title: "Builds"}},
				Messages: []tg.MessageClass{&tg.Message{ID: 35, Date: 2000}},
			}
		case *tg.MessagesGetRepliesRequest:
			if query.MsgID != 30 || query.Peer != parent.input {
				t.Fatal("history request escaped the selected topic")
			}

			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
				&tg.Message{ID: 35, Message: "Inside the topic", PeerID: &tg.PeerChannel{ChannelID: 5}},
			}}
		case *tg.MessagesGetDiscussionMessageRequest:
			if query.Peer != parent.input || query.MsgID != 80 {
				t.Fatal("discussion query used the wrong parent post")
			}

			*output.(*tg.MessagesDiscussionMessage) = tg.MessagesDiscussionMessage{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 72, PeerID: &tg.PeerChannel{ChannelID: 6}},
					&tg.Message{ID: 70, PeerID: &tg.PeerChannel{ChannelID: 6}},
				},
				Chats: []tg.ChatClass{&tg.Channel{ID: 6, AccessHash: 66, Megagroup: true}},
			}
		default:
			return errors.New("unexpected mock request")
		}

		return nil
	}))

	entries, next, err := loadTopics(t.Context(), api, topics)
	if err != nil || len(entries) != 1 || next.topic != 30 {
		t.Fatalf("wrong topic page: %+v, %v", entries, err)
	}

	a.receive(out, serverEvent{kind: "topics", chats: entries, topics: topicPage{next: next}})
	a.input(key{name: "enter"}, out)
	query := <-out

	history, err := loadHistory(t.Context(), api, query)
	if err != nil || len(history) != 1 {
		t.Fatalf("could not load topic: %v", err)
	}

	a.receive(out, serverEvent{kind: "history", chat: parent.key, threadID: 30, log: history})
	a.receive(out, serverEvent{kind: "new", chat: parent.key, log: []message{{id: 90, reply: messageReply{topic: 31}}}})

	if len(a.log) != 1 {
		t.Fatal("another topic appeared in this thread")
	}

	a.input(key{text: "r"}, out)
	a.input(key{text: "Reply"}, out)
	a.input(key{name: "enter"}, out)
	send := <-out

	reply, ok := send.inputReply().(*tg.InputReplyToMessage)
	if !ok || reply.ReplyToMsgID != 35 || reply.TopMsgID != 30 {
		t.Fatalf("topic reply escaped its thread: %+v", reply)
	}

	a.receive(out, serverEvent{kind: "send", chat: parent.key, threadID: 30, log: []message{{id: 36, out: true}}})
	a.input(key{name: "jumpBack"}, out)

	back := <-out
	if back.kind != "threadParent" || back.anchor != 80 {
		t.Fatalf("return should restore the parent post: %+v", back)
	}

	discussion, err := loadDiscussion(t.Context(), api, request{chat: parent, anchor: 80})
	if err != nil || discussion.key.id != 6 || discussion.threadID != 70 {
		t.Fatalf("wrong linked comment thread: %+v, %v", discussion, err)
	}

	comment, ok := (request{chat: *discussion}).inputReply().(*tg.InputReplyToMessage)
	if !ok || comment.ReplyToMsgID != 70 || comment.TopMsgID != 0 {
		t.Fatalf("new comment must reply to the forwarded root: %+v", comment)
	}
}

func TestChooseReactionAndKeepNotificationPrivate(t *testing.T) {
	c := chat{key: peerKey{'g', 5}, title: "Example team", input: &tg.InputPeerChat{ChatID: 5}}
	out := make(chan request, 1)
	a := app{ready: true, open: true, chats: []chat{c}, log: []message{{id: 45, myReaction: "👍"}}}
	a.input(key{text: "z"}, out)
	a.input(key{text: "1"}, out)
	req := <-out

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesSendReactionRequest)

		reactions, present := query.GetReaction()
		if !present || len(reactions) != 0 || query.MsgID != 45 {
			t.Fatalf("choosing the same reaction must remove it: %+v", query)
		}

		output.(*tg.UpdatesBox).Updates = &tg.Updates{}

		return nil
	}))
	if _, err := sendReaction(t.Context(), api, req); err != nil {
		t.Fatal(err)
	}

	const messageText = "This text must not appear in the screenshot"

	notice := a.notification(serverEvent{kind: "new", chat: c.key, log: []message{
		{id: 46, authorName: "Ada", text: messageText},
	}}, true)
	if notice.title != "Ada · Example team" || notice.body != "New message" ||
		strings.Contains(notice.title+notice.body, messageText) {
		t.Fatalf("alert should identify the sender without exposing message text: %+v", notice)
	}

	reaction := a.notification(serverEvent{
		kind: "reactions", chat: c.key, anchor: 44,
		reactionAt: 200, reactionAuthor: "Ada", reactionEmoji: "🔥",
	}, true)
	if reaction.title != "Ada · Example team" || reaction.body != "Reaction 🔥 on your message" {
		t.Fatalf("reaction alert lost its author or emoji: %+v", reaction)
	}
}
