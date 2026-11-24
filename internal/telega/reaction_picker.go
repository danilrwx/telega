package telega

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type reactionChoice struct {
	emoji, title string
	custom       int64
	count        int
}

func (r reactionChoice) same(other reactionChoice) bool {
	return r.custom == other.custom && strings.TrimSuffix(r.emoji, "️") == strings.TrimSuffix(other.emoji, "️")
}

func reactionChoiceFor(r tg.ReactionClass) reactionChoice {
	switch r := r.(type) {
	case *tg.ReactionEmoji:
		return reactionChoice{emoji: r.Emoticon}
	case *tg.ReactionCustomEmoji:
		return reactionChoice{custom: r.DocumentID, title: fmt.Sprintf("Custom emoji %d", r.DocumentID)}
	default:
		return reactionChoice{}
	}
}

func (r reactionChoice) valid() bool { return r.emoji != "" || r.custom != 0 }

func reactionChoices(reactions tg.MessageReactions, chosen bool) []reactionChoice {
	type ordered struct {
		option reactionChoice
		order  int
	}

	var orderedChoices []ordered

	for _, item := range reactions.Results {
		order, set := item.GetChosenOrder()
		if chosen && !set {
			continue
		}

		option := reactionChoiceFor(item.Reaction)

		option.count = item.Count
		if option.valid() {
			orderedChoices = append(orderedChoices, ordered{option, order})
		}
	}

	if chosen {
		slices.SortStableFunc(orderedChoices, func(a, b ordered) int { return a.order - b.order })
	}

	var result []reactionChoice
	for _, item := range orderedChoices {
		result = append(result, item.option)
	}

	return result
}

func toggleReactions(current []reactionChoice, choice reactionChoice) []reactionChoice {
	next := slices.Clone(current)
	if index := slices.IndexFunc(next, choice.same); index >= 0 {
		return slices.Delete(next, index, index+1)
	}

	return append(next, choice)
}

func telegramReactionLimit(ctx context.Context, api *tg.Client, premium bool) int {
	key, limit := "reactions_user_max_default", 1
	if premium {
		key, limit = "reactions_user_max_premium", 3
	}

	response, err := api.HelpGetAppConfig(ctx, 0)

	config, ok := response.(*tg.HelpAppConfig)
	if err != nil || !ok {
		return limit
	}

	object, ok := config.Config.(*tg.JSONObject)
	if !ok {
		return limit
	}

	for _, item := range object.Value {
		if value, ok := item.Value.(*tg.JSONNumber); item.Key == key && ok && value.Value >= 1 && value.Value <= 100 {
			return int(value.Value)
		}
	}

	return limit
}

func loadReactionCatalog(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, premium bool) ([]reactionChoice, error) {
	var (
		full *tg.MessagesChatFull
		err  error
	)

	switch peer := peer.(type) {
	case *tg.InputPeerChannel:
		full, err = api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
	case *tg.InputPeerChat:
		full, err = api.MessagesGetFullChat(ctx, peer.ChatID)
	}

	if err != nil {
		return nil, err
	}

	var allowed []reactionChoice

	restricted := false

	if full != nil {
		if chat, ok := full.FullChat.(interface {
			GetAvailableReactions() (tg.ChatReactionsClass, bool)
		}); ok {
			reactions, _ := chat.GetAvailableReactions()
			switch reactions := reactions.(type) {
			case *tg.ChatReactionsNone:
				return nil, nil
			case *tg.ChatReactionsSome:
				restricted = true

				for _, item := range reactions.Reactions {
					if choice := reactionChoiceFor(item); choice.valid() {
						allowed = append(allowed, choice)
					}
				}
			}
		}
	}

	response, err := api.MessagesGetAvailableReactions(ctx, 0)
	if err != nil {
		return nil, err
	}

	catalog, ok := response.(*tg.MessagesAvailableReactions)
	if !ok {
		return allowed, nil
	}

	var options []reactionChoice

	for _, item := range catalog.Reactions {
		if item.Inactive || (item.Premium && !premium) {
			continue
		}

		option := reactionChoice{emoji: item.Reaction, title: item.Title}
		if !restricted || slices.ContainsFunc(allowed, option.same) {
			options = append(options, option)
		}
	}

	for _, option := range allowed {
		if option.custom != 0 {
			options = append(options, option)
		}
	}

	return options, nil
}

func (a *app) reactionMessage() (message, bool) {
	if a.reactionTarget != 0 {
		index := slices.IndexFunc(a.log, func(msg message) bool { return msg.id == a.reactionTarget })
		if index >= 0 {
			return a.log[index], true
		}

		return message{}, false
	}

	if len(a.log) > 0 {
		return a.log[a.offset], true
	}

	return message{}, false
}

func (a *app) reactionMatches() []reactionChoice {
	term := strings.ToLower(strings.TrimSpace(a.reactionQuery))

	var result []reactionChoice

	add := func(choice reactionChoice) {
		if !slices.ContainsFunc(result, choice.same) && strings.Contains(strings.ToLower(choice.emoji+" "+choice.title+" "+reactionName(choice.emoji)), term) {
			result = append(result, choice)
		}
	}
	if msg, ok := a.reactionMessage(); ok {
		for _, choice := range msg.messageReactions {
			add(choice)
		}
	}

	for _, choice := range a.reactionCatalog {
		add(choice)
	}

	return result
}

func (a *app) reactionPickerInput(k key, out chan<- request) {
	results := a.reactionMatches()

	switch k.name {
	case "up":
		a.reactionIndex = max(0, a.reactionIndex-1)
	case "down":
		a.reactionIndex = min(a.reactionIndex+1, max(0, len(results)-1))
	case "erase":
		if _, size := utf8.DecodeLastRuneInString(a.reactionQuery); size > 0 {
			a.reactionQuery = a.reactionQuery[:len(a.reactionQuery)-size]
		}

		a.reactionIndex = 0
	case "enter", "jumpBack":
		if a.reactionIndex < len(results) {
			choice := results[a.reactionIndex]
			if k.name == "jumpBack" {
				a.previewReaction(choice, out)
			} else {
				a.submitReaction(choice, out)
			}
		}
	case "", "paste":
		a.reactionQuery = limitText(a.reactionQuery+k.text, 80)
		a.reactionIndex = 0
	}
}

func (a *app) submitReaction(choice reactionChoice, out chan<- request) {
	if !choice.valid() {
		return
	}

	if a.pending == "react" {
		a.reactionQueue = append(a.reactionQueue, choice)
		return
	}

	if a.pending != "" {
		return
	}

	msg, ok := a.reactionMessage()
	if !ok {
		return
	}

	chosen := msg.chosenReactions
	if len(chosen) == 0 && msg.myReaction != "" {
		chosen = []reactionChoice{{emoji: msg.myReaction}}
	}

	a.ask(out, request{kind: "react", chat: a.currentChat(), anchor: msg.id, reactions: toggleReactions(chosen, choice)})
}

func (a *app) paintReactionPicker(label func(int, int, int, string), statusRow, rows, cols int) {
	results := a.reactionMatches()
	label(2, statusRow, cols-2, "/ "+a.reactionQuery)
	room := rows - statusRow - 1

	notice := a.status
	if notice == "Reaction updated" {
		notice = ""
	}

	switch a.pending {
	case "reactionCatalog":
		notice = "Loading reactions…"
	case "reactionImage":
		notice = "Loading reaction image…"
	case "react":
		notice = "Saving reactions…"
	}

	if notice == "" && len(results) == 0 {
		notice = "No matching reactions"
	}

	if notice != "" && room > 0 {
		room--

		label(2, rows-1, cols-2, notice)
	}

	start := max(0, min(a.reactionIndex-room+1, len(results)-room))
	msg, _ := a.reactionMessage()

	for i, choice := range results[start:min(len(results), start+room)] {
		text := strings.TrimSpace(choice.emoji + " " + choice.title)
		if choice.emoji != "" {
			switch {
			case a.textReactions && choice.title != "":
				text = choice.title
			case a.textReactions:
				text = reactionName(choice.emoji)
			case choice.title == "":
				text += " " + reactionName(choice.emoji)
			}
		}

		if slices.ContainsFunc(msg.chosenReactions, choice.same) {
			text += " ✓"
		}

		prefix := "  "
		if i+start == a.reactionIndex {
			prefix = "› "
		}

		label(2, statusRow+i+1, cols-2, prefix+tui.Clip(text, cols-4))
	}
}
