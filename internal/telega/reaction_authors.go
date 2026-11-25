package telega

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func (a *app) showReactionAuthors(out chan<- request) {
	if a.demo || len(a.log) == 0 {
		return
	}

	a.reactionAuthors = &tui.ReactionAuthors{Status: "Loading reactions…"}
	out <- request{
		kind: "reactionAuthors", chat: a.currentChat(), anchor: a.log[a.offset].id, text: a.log[a.offset].reactions,
		reactionAuthors: a.reactionAuthors, textReactions: a.textReactions, recentReactions: a.log[a.offset].recentReactions,
	}
}

func fetchReactionAuthors(ctx context.Context, req request,
	fetch func(context.Context, *tg.MessagesGetMessageReactionsListRequest) (*tg.MessagesMessageReactionsList, error),
	documents func(context.Context, []int64) ([]tg.DocumentClass, error),
	refresh func(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error),
) ([]string, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if req.chat.key.kind == 'u' && len(req.recentReactions) == 0 && req.text != "" {
		result, err := refresh(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: req.anchor}})
		if err != nil {
			return nil, "Could not load reaction authors: " + err.Error()
		}

		data, ok := result.AsModified()
		if !ok {
			return nil, "Could not refresh reaction authors"
		}

		found := false

		for _, raw := range data.GetMessages() {
			if peer, msg, ok := unwrapMessage(raw); ok && peer == req.chat.key && msg.id == req.anchor {
				req.recentReactions, req.text, found = msg.recentReactions, msg.reactions, true
				break
			}
		}

		if !found {
			return nil, "Message is no longer available"
		}

		if len(req.recentReactions) == 0 && req.text != "" {
			return nil, "Reaction authors are unavailable for this message"
		}
	}

	query := &tg.MessagesGetMessageReactionsListRequest{Peer: req.chat.input, ID: req.anchor, Limit: 100}

	type authorReaction struct {
		label, name string
		custom      int64
	}

	var (
		entries []authorReaction
		status  string
	)

	customLabels := make(map[int64]string)

	for {
		var (
			page *tg.MessagesMessageReactionsList
			err  error
		)

		if req.chat.key.kind == 'u' {
			// Private chats include their reaction authors in the message itself.
			page = &tg.MessagesMessageReactionsList{Reactions: req.recentReactions}
		} else {
			page, err = fetch(ctx, query)
		}

		if tgerr.Is(err, "BROADCAST_FORBIDDEN") {
			return nil, "Telegram hides reaction authors in this channel"
		}

		if err != nil {
			if len(entries) == 0 {
				status = "Could not load reaction authors: " + err.Error()
			} else {
				status = "Could not load all reaction authors: " + err.Error()
			}

			break
		}

		entities := tg.EntitiesFrom(page.Users, page.Chats)
		for _, item := range page.Reactions {
			peer := keyFor(item.PeerID)

			name := entityName(peer, entities)
			switch {
			case item.My:
				name = "You"
			case peer == req.chat.key && req.chat.title != "":
				name = req.chat.title
			case name == "":
				name = fmt.Sprintf("Unknown author #%d", peer.id)
			}

			choice := reactionChoiceFor(item.Reaction)

			label := choice.emoji
			switch {
			case choice.custom != 0:
				label = choice.title
				customLabels[choice.custom] = ""
			case label == "":
				if _, paid := item.Reaction.(*tg.ReactionPaid); paid {
					label = "⭐"
				} else {
					continue
				}
			}

			if req.textReactions && choice.custom == 0 {
				label = "[" + reactionName(label) + "]"
			}

			entries = append(entries, authorReaction{label, name, choice.custom})
		}

		if page.NextOffset == "" {
			break
		}

		if page.NextOffset == query.Offset {
			status = "Could not load all reaction authors"
			break
		}

		query.SetOffset(page.NextOffset)
	}

	if !req.textReactions && len(customLabels) > 0 {
		lookupCtx, stopLookup := context.WithTimeout(ctx, 2*time.Second)
		defer stopLookup()

		ids := make([]int64, 0, len(customLabels))
		for id := range customLabels {
			ids = append(ids, id)
		}

		for batch := range slices.Chunk(ids, 100) {
			docs, err := documents(lookupCtx, batch)
			if err != nil {
				break
			}

			for _, doc := range docs {
				if doc, ok := doc.(*tg.Document); ok {
					for _, attr := range doc.Attributes {
						if emoji, ok := attr.(*tg.DocumentAttributeCustomEmoji); ok {
							customLabels[doc.ID] = emoji.Alt
						}
					}
				}
			}
		}
	}

	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		if glyph := customLabels[entry.custom]; glyph != "" {
			entry.label = glyph + " " + entry.label
		}

		rows = append(rows, entry.label+"  "+entry.name)
	}

	slices.Sort(rows)

	return rows, status
}
