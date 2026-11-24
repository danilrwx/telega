package telega

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

type messageLink struct {
	url    string
	label  string
	hidden bool
}

var plainLink = regexp.MustCompile(`(?i)(?:https?://|tg://|www\.)[^\s<>"\x00-\x1f]+`)

func webLink(raw string, allowBare bool) string {
	if raw == "" || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ""
	}

	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "" && allowBare && !strings.HasPrefix(raw, "/") {
		u, err = url.Parse("https://" + raw)
		if err == nil && !strings.Contains(u.Hostname(), ".") {
			return ""
		}
	}

	if err != nil || u.Hostname() == "" || u.User != nil {
		return ""
	}

	switch u.Scheme {
	case "http", "https", "tg":
		return u.String()
	default:
		return ""
	}
}

func entityString(text string, offset, length int) string {
	if offset < 0 || length <= 0 || offset+length <= offset {
		return ""
	}

	start, pos := -1, 0
	for i, r := range text {
		if pos == offset {
			start = i
		}

		if pos == offset+length && start >= 0 {
			return text[start:i]
		}

		pos += utf16.RuneLen(r)
	}

	if start >= 0 && pos == offset+length {
		return text[start:]
	}

	return ""
}

func messageLinks(text string, entities []tg.MessageEntityClass, spans []textSpan) []messageLink {
	var links []messageLink

	add := func(raw, label string, start, end int) {
		target := webLink(raw, true)
		if target == "" {
			return
		}

		hidden := slices.ContainsFunc(spans, func(s textSpan) bool {
			return s.style == spoilerStyle && start < s.end && s.start < end
		})

		for i, link := range links {
			if link.url == target && link.label == label {
				links[i].hidden = link.hidden && hidden
				return
			}
		}

		links = append(links, messageLink{url: target, label: label, hidden: hidden})
	}
	for _, entity := range entities {
		start, size := entity.GetOffset(), entity.GetLength()
		switch entity := entity.(type) {
		case *tg.MessageEntityTextURL:
			label := strings.Join(strings.Fields(entityString(text, start, size)), " ")
			add(entity.URL, label, start, start+size)
		case *tg.MessageEntityURL:
			add(entityString(text, start, size), "", start, start+size)
		}
	}
	// Sent messages can arrive before Telegram supplies their parsed entities.
	if len(entities) == 0 {
		for _, raw := range plainLink.FindAllString(text, -1) {
			add(strings.TrimRight(raw, ".,;!?)]}"), "", 0, 0)
		}
	}

	return links
}

func (a *app) chooseLink(msg message) {
	var choices []messageLink

	for _, link := range msg.links {
		if !link.hidden || a.revealID == msg.id {
			choices = append(choices, link)
		}
	}

	switch len(choices) {
	case 0:
		a.status = "No visible link on the selected message"
	case 1:
		a.openTarget = choices[0].url
	default:
		a.linkChoices, a.linkIndex = choices, 0
	}
}

func (a *app) linkInput(k key) {
	switch {
	case k.name == "escape" || k.name == "left" || k.name == "" && (k.text == "h" || k.text == "o" || k.text == "O"):
		a.linkChoices = nil
	case k.name == "enter":
		a.openTarget = a.linkChoices[a.linkIndex].url
		a.linkChoices = nil
	case k.name == "up" || k.text == "k":
		a.linkIndex = max(0, a.linkIndex-1)
	case k.name == "down" || k.text == "j":
		a.linkIndex = min(len(a.linkChoices)-1, a.linkIndex+1)
	}
}

func (a *app) toggleSelected() {
	if len(a.log) == 0 {
		return
	}

	id := a.log[a.offset].id
	if _, exists := a.selection[id]; exists {
		delete(a.selection, id)
		return
	}

	if a.selection == nil {
		a.selection = make(map[int]struct{})
	}

	a.selection[id] = struct{}{}
}

func (a *app) askDelete(forEveryone bool) {
	if a.pending != "" || len(a.log) == 0 && len(a.selection) == 0 {
		return
	}

	a.confirmDelete = a.selectedMessages()
	a.deleteForEveryone = forEveryone || a.currentChat().key.kind == 'c'
}

func (a *app) selectedMessages() []int {
	ids := make([]int, 0, max(1, len(a.selection)))
	for id := range a.selection {
		ids = append(ids, id)
	}

	if len(ids) == 0 && len(a.log) > 0 {
		ids = append(ids, a.log[a.offset].id)
	}

	slices.Sort(ids)

	return ids
}

func (a *app) confirmDeletion(k key, out chan<- request) {
	switch {
	case k.name == "escape" || k.name == "" && k.text == "n":
		a.confirmDelete = nil
	case k.name == "" && k.text == "y":
		ids := a.confirmDelete

		a.confirmDelete = nil
		if a.demo {
			a.removeMessages(ids)
			a.status = fmt.Sprintf("Deleted %d message(s)", len(ids))

			return
		}

		a.ask(out, request{kind: "deleteMessages", chat: a.currentChat(), ids: ids, revoke: a.deleteForEveryone})
	}
}

func (a *app) removeMessages(ids []int) {
	for _, id := range ids {
		delete(a.selection, id)
	}

	if len(a.log) == 0 {
		return
	}

	cursor := a.log[a.offset].id
	a.log = slices.DeleteFunc(a.log, func(msg message) bool { return slices.Contains(ids, msg.id) })

	a.offset = min(a.offset, max(0, len(a.log)-1))
	if i := slices.IndexFunc(a.log, func(msg message) bool { return msg.id == cursor }); i >= 0 {
		a.offset = i
	}
}

func deleteMessages(ctx context.Context, api *tg.Client, req request) ([]int, error) {
	var deleted []int

	for ids := range slices.Chunk(req.ids, 100) {
		var err error
		if channel, ok := req.chat.input.(*tg.InputPeerChannel); ok {
			_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
				ID:      ids,
			})
		} else {
			_, err = api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{ID: ids, Revoke: req.revoke})
		}

		if err != nil {
			return deleted, err
		}

		deleted = append(deleted, ids...)
	}

	return deleted, nil
}
