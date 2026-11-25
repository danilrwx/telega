package telega

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/danilrwx/telega/tui"

	"github.com/gotd/td/tg"
)

var telegramQuickReactions = tui.ReactionOptions("🤣")

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

var reactionNames = map[string]string{
	"👍": "like", "👎": "dislike", "❤": "heart", "🔥": "fire", "😂": "laugh", "🎉": "party", "⭐": "star",
	"🤣": "rofl", "😁": "grin", "😄": "smile", "🥰": "love", "😍": "heart-eyes", "🤩": "star-eyes", "😘": "kiss",
	"👏": "clap", "🙌": "cheer", "🙏": "thanks", "🤝": "handshake", "👌": "ok", "💯": "100",
	"💔": "broken-heart", "🤔": "think", "🤯": "mind-blown", "😱": "scream", "😢": "sad", "😭": "cry", "😡": "angry",
	"😎": "cool", "🤓": "nerd", "💩": "poop", "🤡": "clown", "👀": "eyes", "🤷": "shrug", "🗿": "moai",
	"🥳": "celebrate", "🫡": "salute", "🫶": "heart-hands", "💪": "strong", "🏆": "trophy", "⚡": "zap",
	"❤‍🔥": "heart-fire", "💘": "cupid", "💋": "kiss-mark", "🕊": "dove", "🍾": "cheers",
	"custom": "custom", "custom emoji": "custom",
}

func reactionName(emoji string) string {
	emoji = strings.ReplaceAll(emoji, "\uFE0F", "")
	if name := reactionNames[emoji]; name != "" {
		return name
	}

	return strings.Trim(fmt.Sprintf("%U", []rune(emoji)), "[]")
}

func textReactionSummary(summary string) string {
	parts := strings.Split(summary, "  ")
	for i, part := range parts {
		if emoji, count, ok := strings.Cut(part, " "); ok {
			parts[i] = "[" + reactionName(emoji) + "] " + count
		}
	}

	return strings.Join(parts, "  ")
}

func reactionUpdates(events chan<- serverEvent) tg.MessageReactionsHandler {
	return func(ctx context.Context, entities tg.Entities, update *tg.UpdateMessageReactions) error {
		text := reactionSummary(update.Reactions)

		e := serverEvent{
			kind: "reactions", chat: keyFor(update.Peer), anchor: update.MsgID, text: text,
			recentReactions: update.Reactions.RecentReactions,
			myReaction:      chosenReaction(update.Reactions), reactionChoiceKnown: !update.Reactions.Min,
			chosenReactions: reactionChoices(update.Reactions, true), messageReactions: reactionChoices(update.Reactions, false),
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

	selection := make([]tg.ReactionClass, 0, len(req.reactions))
	for _, choice := range req.reactions {
		if choice.custom != 0 {
			selection = append(selection, &tg.ReactionCustomEmoji{DocumentID: choice.custom})
		} else {
			selection = append(selection, &tg.ReactionEmoji{Emoticon: choice.emoji})
		}
	}

	query.SetReaction(selection)

	return api.MessagesSendReaction(ctx, query)
}

func (a *app) reactionSuggestions() []reactionChoice {
	msg, ok := a.reactionMessage()
	if !ok {
		return nil
	}

	var choices []reactionChoice

	for _, choice := range msg.messageReactions {
		if choice.count > 0 && !slices.ContainsFunc(msg.chosenReactions, choice.same) &&
			(msg.myReaction == "" || !choice.same(reactionChoice{emoji: msg.myReaction})) {
			choices = append(choices, choice)
		}
	}

	return choices
}

func (a *app) reactionSuggestionRow(width int) string {
	if !a.pickingReaction || a.reactionPickerOpen {
		return ""
	}

	choices := a.reactionSuggestions()
	if len(choices) == 0 {
		return ""
	}

	index := min(a.reactionSuggestionIndex, len(choices)-1)
	choice := choices[index]

	label := choice.emoji
	if choice.custom != 0 {
		label = choice.title
	} else if a.textReactions {
		label = reactionName(label)
	}

	return tui.ReactionSuggestion(label, choice.count, index, len(choices), width)
}

func (a *app) pickReaction(k key, out chan<- request) {
	if a.reactionClosing {
		return
	}

	if !a.reactionPickerOpen && k.name == "" && commandKey(k).text == "z" {
		k.name = "escape"
	}

	switch k.name {
	case "escape":
		if a.pending == "react" && len(a.reactionQueue) > 0 {
			a.reactionClosing = true
		} else {
			a.closeReactionPicker()
		}

		return
	case "tab":
		a.reactionPickerOpen = !a.reactionPickerOpen
		if a.reactionPickerOpen {
			a.reactionIndex, a.reactionQuery = 0, ""
			if a.pending == "" && !a.reactionCatalogLoaded {
				a.ask(out, request{kind: "reactionCatalog", chat: a.currentChat()})
			}
		}

		return
	}

	if a.reactionPickerOpen {
		a.reactionPickerInput(k, out)
		return
	}

	switch k.name {
	case "":
		a.submitReaction(reactionChoice{emoji: tui.QuickReaction(k.text, telegramQuickReactions)}, out)
	case "left", "right", "enter":
		choices := a.reactionSuggestions()
		if len(choices) == 0 {
			return
		}

		index := min(a.reactionSuggestionIndex, len(choices)-1)

		switch k.name {
		case "left":
			a.reactionSuggestionIndex = max(0, index-1)
		case "right":
			a.reactionSuggestionIndex = min(len(choices)-1, index+1)
		case "enter":
			if a.pending == "" {
				a.submitReaction(choices[index], out)
			}
		}
	}
}

func (a *app) closeReactionPicker() {
	a.pickingReaction, a.reactionPickerOpen, a.reactionTarget = false, false, 0
	a.reactionQueue, a.reactionClosing = nil, false
}
