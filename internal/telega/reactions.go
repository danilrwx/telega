package telega

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
)

func reactionSummary(reactions tg.MessageReactions) string {
	if reactions.ReactionsAsTags {
		return ""
	}

	var parts []string

	var custom int64

	for _, result := range reactions.Results {
		if result.Count <= 0 {
			continue
		}

		var emoji string

		switch reaction := result.Reaction.(type) {
		case *tg.ReactionEmoji:
			emoji = limitText(strings.TrimSpace(reaction.Emoticon), 16)
		case *tg.ReactionPaid:
			emoji = "⭐"
		case *tg.ReactionCustomEmoji:
			custom += int64(result.Count)
		}

		if emoji != "" {
			parts = append(parts, fmt.Sprintf("%s %d", emoji, result.Count))
		}
	}

	if custom > 0 {
		parts = append(parts, fmt.Sprintf("custom %d", custom))
	}

	return strings.Join(parts, "  ")
}

func reactionUpdates(events chan<- serverEvent) tg.MessageReactionsHandler {
	return func(ctx context.Context, entities tg.Entities, update *tg.UpdateMessageReactions) error {
		text := reactionSummary(update.Reactions)

		e := serverEvent{
			kind: "reactions", chat: keyFor(update.Peer), anchor: update.MsgID, text: text,
			myReaction: chosenReaction(update.Reactions), reactionChoiceKnown: !update.Reactions.Min,
		}
		e.source = notificationSource(e.chat, entities)
		e.threadID = update.TopMsgID

		if text != "" {
			for _, recent := range update.Reactions.RecentReactions {
				if !recent.Unread || recent.My || recent.Date <= e.reactionAt {
					continue
				}

				e.reactionAt, e.reactionActor = recent.Date, keyFor(recent.PeerID)
				e.reactionAuthor = entityName(e.reactionActor, entities)
				e.reactionEmoji = ""

				switch reaction := recent.Reaction.(type) {
				case *tg.ReactionEmoji:
					e.reactionEmoji = limitText(reaction.Emoticon, 16)
				case *tg.ReactionPaid:
					e.reactionEmoji = "⭐"
				case *tg.ReactionCustomEmoji:
					e.reactionEmoji = "custom emoji"
				}
			}
		}

		return emit(ctx, events, e)
	}
}

var quickReactions = [...]string{"👍", "❤", "🔥", "😂", "🎉", "👎"}

func chosenReaction(reactions tg.MessageReactions) string {
	for _, result := range reactions.Results {
		emoji, ok := result.Reaction.(*tg.ReactionEmoji)
		if _, chosen := result.GetChosenOrder(); ok && chosen {
			return emoji.Emoticon
		}
	}

	return ""
}

func sendReaction(ctx context.Context, api *tg.Client, req request) (tg.UpdatesClass, error) {
	query := &tg.MessagesSendReactionRequest{Peer: req.chat.input, MsgID: req.anchor}

	var selection []tg.ReactionClass
	if req.text != "" {
		selection = append(selection, &tg.ReactionEmoji{Emoticon: req.text})
	}

	query.SetReaction(selection)

	return api.MessagesSendReaction(ctx, query)
}

func (a *app) pickReaction(k key, out chan<- request) {
	if k.name == "escape" {
		a.pickingReaction = false
		return
	}

	if k.name != "" || len(k.text) != 1 || k.text[0] < '1' || int(k.text[0]-'1') >= len(quickReactions) || len(a.log) == 0 {
		return
	}

	msg := a.log[a.offset]

	emoji := quickReactions[k.text[0]-'1']
	if strings.TrimSuffix(emoji, "️") == strings.TrimSuffix(msg.myReaction, "️") {
		emoji = ""
	}

	a.pickingReaction = false
	a.ask(out, request{kind: "react", chat: a.currentChat(), anchor: msg.id, text: emoji})
}
