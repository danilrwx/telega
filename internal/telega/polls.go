package telega

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type messagePoll struct {
	id                                      int64
	question, solution                      string
	closed, public, multiple, quiz          bool
	noRevote, hidden, choiceKnown, hasTotal bool
	closeDate                               int
	total                                   int
	options                                 []pollOption
}

type pollOption struct {
	id, text                  string
	chosen, correct, hasCount bool
	count                     int
}

type pollUpdate struct {
	id         int64
	peer       peerKey
	message    int
	definition *tg.Poll
	results    tg.PollResults
}

type pollRequest struct {
	id      int64
	choices [][]byte // Empty for a retraction when kind is pollVote.
}

func pollFromMedia(media tg.MessageMediaClass) *messagePoll {
	m, ok := media.(*tg.MessageMediaPoll)
	if !ok || m == nil {
		return nil
	}

	return mergePoll(&messagePoll{id: m.Poll.ID}, &pollUpdate{definition: &m.Poll, results: m.Results})
}

func mergePoll(previous *messagePoll, update *pollUpdate) *messagePoll {
	p := *previous
	p.options = slices.Clone(previous.options)

	if definition := update.definition; definition != nil {
		p.question = definition.Question.Text
		p.closed, p.public, p.multiple, p.quiz = definition.Closed, definition.PublicVoters, definition.MultipleChoice, definition.Quiz
		p.noRevote, p.hidden, p.closeDate = definition.RevotingDisabled, definition.HideResultsUntilClose, definition.CloseDate

		if definition.Answers != nil {
			p.options = nil

			for _, answer := range definition.Answers {
				item, ok := answer.(*tg.PollAnswer)
				if !ok || item == nil || len(item.Option) == 0 || pollOptionIndex(p.options, string(item.Option)) >= 0 {
					continue
				}

				option := pollOption{id: string(item.Option), text: item.Text.Text}
				if i := pollOptionIndex(previous.options, option.id); i >= 0 {
					option = previous.options[i]
					option.text = item.Text.Text
				}

				if option.text == "" {
					option.text = "[Media option]"
				}

				p.options = append(p.options, option)
			}
		}
	}

	r := update.results
	if !r.Min {
		p.choiceKnown = true
		for i := range p.options {
			p.options[i].chosen = false
		}
	}

	if r.Results != nil || r.Flags.Has(1) {
		for i := range p.options {
			p.options[i].hasCount, p.options[i].count = false, 0
		}
	}

	for _, result := range r.Results {
		if i := pollOptionIndex(p.options, string(result.Option)); i >= 0 {
			option := &p.options[i]
			if !r.Min {
				option.chosen, option.correct = result.Chosen, result.Correct
			}

			if _, ok := result.GetVoters(); ok || result.Voters > 0 {
				option.hasCount, option.count = true, max(0, result.Voters)
			}
		}
	}

	if r.Flags.Has(2) || r.TotalVoters > 0 {
		p.total, p.hasTotal = max(0, r.TotalVoters), true
	}

	if r.Solution != "" || r.Flags.Has(4) {
		p.solution = r.Solution
	}

	return &p
}

func pollOptionIndex(options []pollOption, id string) int {
	return slices.IndexFunc(options, func(option pollOption) bool { return option.id == id })
}

func (p *messagePoll) preview() string {
	label := "Poll"
	if p.quiz {
		label = "Quiz"
	}

	return strings.TrimSpace(label + ": " + strings.Join(strings.Fields(p.question), " "))
}

func (p *messagePoll) votingClosed() bool {
	return p.closed || p.closeDate > 0 && int64(p.closeDate) <= time.Now().Unix()
}

func (p *messagePoll) voted() bool {
	return slices.ContainsFunc(p.options, func(option pollOption) bool { return option.chosen })
}

func (p *messagePoll) canVote() bool {
	return len(p.options) > 0 && p.choiceKnown && !p.votingClosed() && (!p.noRevote || !p.voted())
}

func pollEvent(update *tg.UpdateMessagePoll) serverEvent {
	value := &pollUpdate{id: update.PollID, peer: keyFor(update.Peer), message: update.MsgID, results: update.Results}
	if update.Poll.ID == update.PollID && update.Poll.ID != 0 {
		value.definition = &update.Poll
	}

	return serverEvent{kind: "pollUpdate", poll: value}
}

func (u *pollUpdate) matches(c peerKey, message int, id int64) bool {
	return u.id != 0 && u.id == id && (u.peer.id == 0 || u.peer == c) && (u.message == 0 || u.message == message)
}

func runPollRequest(ctx context.Context, api *tg.Client, req request) serverEvent {
	return fetchPoll(ctx, req, api.MessagesGetPollResults, api.MessagesSendVote)
}

func fetchPoll(ctx context.Context, req request,
	results func(context.Context, *tg.MessagesGetPollResultsRequest) (tg.UpdatesClass, error),
	vote func(context.Context, *tg.MessagesSendVoteRequest) (tg.UpdatesClass, error),
) serverEvent {
	e := serverEvent{kind: req.kind, chat: req.chat.key, threadID: req.chat.threadID, anchor: req.anchor, pollRequest: req.poll}
	if req.poll == nil || req.poll.id == 0 || req.chat.input == nil || req.anchor <= 0 {
		e.hint = "Poll message is unavailable"
		return e
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	get := func() (tg.UpdatesClass, error) {
		// A zero hash requests the full results, including our selection.
		return results(ctx, &tg.MessagesGetPollResultsRequest{Peer: req.chat.input, MsgID: req.anchor})
	}

	if req.kind == "pollVote" {
		updates, err := vote(ctx, &tg.MessagesSendVoteRequest{Peer: req.chat.input, MsgID: req.anchor, Options: req.poll.choices})
		if err != nil {
			e.hint = "Could not vote: " + err.Error()
			return e
		}

		e.poll = pollResultUpdate(updates, req)
		if e.poll != nil && !e.poll.results.Min {
			return e
		}
	}

	updates, err := get()
	if err == nil {
		if found := pollResultUpdate(updates, req); found != nil {
			e.poll = found
		}
	}

	if e.poll == nil || e.poll.results.Min {
		switch {
		case req.kind == "pollVote":
			e.hint = "Vote accepted; current results unavailable"
		case err != nil:
			e.hint = "Could not refresh poll: " + err.Error()
		default:
			e.hint = "Telegram did not return full poll results"
		}
	}

	return e
}

func pollResultUpdate(value tg.UpdatesClass, req request) *pollUpdate {
	var updates []tg.UpdateClass

	switch value := value.(type) {
	case *tg.Updates:
		updates = value.Updates
	case *tg.UpdatesCombined:
		updates = value.Updates
	case *tg.UpdateShort:
		updates = []tg.UpdateClass{value.Update}
	}

	var best *pollUpdate

	for _, item := range updates {
		if update, ok := item.(*tg.UpdateMessagePoll); ok && update != nil {
			u := pollEvent(update).poll
			if u.matches(req.chat.key, req.anchor, req.poll.id) && (best == nil || best.results.Min || !u.results.Min) {
				best = u
			}
		}
	}

	if best != nil {
		// An RPC result with no location still belongs to the requested message.
		best.peer, best.message = req.chat.key, req.anchor
	}

	return best
}

type pollDialog struct {
	chat        chat
	message     int
	poll        *messagePoll
	pending     *pollRequest
	status      string
	cursor      int
	draft       map[string]bool
	start, page int
	follow      bool
}

func (a *app) openPoll(out chan<- request) bool {
	if !a.open || a.offset < 0 || a.offset >= len(a.log) || a.log[a.offset].poll == nil || a.thread == nil && (a.selected < 0 || a.selected >= len(a.chats)) {
		return false
	}

	m := a.log[a.offset]
	d := &pollDialog{chat: a.currentChat(), message: m.id, poll: m.poll}

	a.pollDialog = d
	if !a.demo && !a.disconnected && d.chat.input != nil {
		d.send(out, "pollResults", nil)
	}

	return true
}

func (d *pollDialog) send(out chan<- request, kind string, choices [][]byte) {
	d.pending = &pollRequest{id: d.poll.id, choices: choices}

	d.status = "Refreshing results…"
	if kind == "pollVote" {
		d.status = "Sending vote…"
	}

	out <- request{kind: kind, chat: d.chat, anchor: d.message, poll: d.pending}
}

func (a *app) pollInput(k key, out chan<- request) {
	d := a.pollDialog
	p := d.poll

	switch {
	case k.name == "escape" || k.text == "q":
		a.pollDialog = nil
		return
	case k.name == "up" || k.text == "k":
		d.cursor, d.follow = max(0, d.cursor-1), true
		return
	case k.name == "down" || k.text == "j":
		d.cursor, d.follow = min(d.cursor+1, max(0, len(p.options)-1)), true
		return
	case k.name == "pageup":
		d.start, d.follow = max(0, d.start-max(1, d.page)), false
		return
	case k.name == "pagedown":
		d.start, d.follow = d.start+max(1, d.page), false
		return
	}

	if d.pending != nil {
		return
	}

	if k.text == "r" || k.text == "x" || k.text == " " || k.name == "enter" {
		if a.demo || a.disconnected || d.chat.input == nil {
			d.status = "Poll is read-only while offline"
			return
		}
	}

	if k.text == "r" {
		d.send(out, "pollResults", nil)
		return
	}

	if !p.canVote() {
		return
	}

	switch {
	case k.text == "x" && p.voted():
		d.send(out, "pollVote", nil)
	case k.text == " " && p.multiple:
		if d.draft == nil {
			d.draft = make(map[string]bool)
			for _, item := range p.options {
				d.draft[item.id] = item.chosen
			}
		}

		id := p.options[d.cursor].id
		d.draft[id] = !d.draft[id]
	case k.name == "enter":
		var choices [][]byte

		if p.multiple {
			for _, option := range p.options {
				if d.draft[option.id] {
					choices = append(choices, []byte(option.id))
				}
			}
		} else {
			choices = append(choices, []byte(p.options[d.cursor].id))
		}

		if len(choices) > 0 {
			d.send(out, "pollVote", choices)
		} else {
			d.status = "Select at least one option with Space"
		}
	}
}

func (a *app) receivePoll(e serverEvent) {
	if u := e.poll; u != nil {
		if a.open && (a.thread != nil || a.selected >= 0 && a.selected < len(a.chats)) {
			c := a.currentChat()
			for i := range a.log {
				m := &a.log[i]
				if m.poll != nil && u.matches(c.key, m.id, m.poll.id) {
					preview := m.poll.preview()

					m.poll = mergePoll(m.poll, u)
					if m.text == "" || m.text == preview {
						m.text = m.poll.preview()
					}
				}
			}
		}

		if d := a.pollDialog; d != nil && u.matches(d.chat.key, d.message, d.poll.id) {
			id := ""
			if d.cursor >= 0 && d.cursor < len(d.poll.options) {
				id = d.poll.options[d.cursor].id
			}

			d.poll = mergePoll(d.poll, u)
			if i := pollOptionIndex(d.poll.options, id); i >= 0 {
				d.cursor = i
			} else {
				d.cursor = min(d.cursor, max(0, len(d.poll.options)-1))
			}
		}
	}

	if d := a.pollDialog; d != nil && e.pollRequest != nil && d.pending == e.pollRequest {
		d.pending, d.status = nil, e.hint
		if e.kind == "pollVote" && (e.hint == "" || e.poll != nil && !e.poll.results.Min) {
			d.draft = nil
		}
	}
}

func (d *pollDialog) render(width, height int, row func(string, bool, bool, bool)) {
	if height <= 0 || width <= 0 {
		return
	}

	p := d.poll

	title := "Poll"
	if p.quiz {
		title = "Quiz"
	}

	if p.public {
		title += " · public"
	} else {
		title += " · anonymous"
	}

	if p.votingClosed() {
		title += " · closed"
	}

	showCounts := !p.hidden || p.closed
	if showCounts && p.hasTotal {
		title += fmt.Sprintf(" · %d voters", p.total)
	}

	type line struct {
		text     string
		selected bool
	}

	var lines []line

	appendText := func(text string, selected bool) {
		for _, text := range tui.Wrap(text, width, 0) {
			lines = append(lines, line{text: text, selected: selected})
		}
	}

	appendText(p.question, false)

	lines = append(lines, line{})
	selectedAt := 0

	for i, option := range p.options {
		mark := "[ ] "
		if option.chosen {
			mark = "[✓] "
		}

		if p.canVote() && p.multiple && d.draft != nil && option.chosen != d.draft[option.id] {
			if d.draft[option.id] {
				mark = "[+] "
			} else {
				mark = "[−] "
			}
		}

		text := mark + option.text
		if option.correct && showCounts {
			text += " (correct)"
		}

		if showCounts && option.hasCount {
			text += fmt.Sprintf(" · %d", option.count)
			if p.hasTotal && p.total > 0 {
				text += fmt.Sprintf(" (%.0f%%)", min(100, float64(option.count)*100/float64(p.total)))
			}
		}

		if i == d.cursor {
			selectedAt = len(lines)
		}

		appendText(text, i == d.cursor)
	}

	if len(p.options) == 0 {
		appendText("No options available", false)
	}

	if p.hidden && !p.closed {
		appendText("Results hidden until the poll closes", false)
	}

	if showCounts && p.solution != "" {
		lines = append(lines, line{})

		appendText("Explanation: "+p.solution, false)
	}

	status := d.status
	if status == "" && !p.choiceKnown && !p.votingClosed() {
		status = "Refresh to load your vote"
	}

	footer := 1
	if status != "" && height >= 4 {
		footer++
	}

	d.page = max(0, height-1-footer)

	d.start = min(d.start, max(0, len(lines)-d.page))
	if d.follow && d.page > 0 {
		d.start = min(selectedAt, max(d.start, selectedAt-d.page+1))
		d.follow = false
	}

	row(tui.Clip(title, width), true, false, false)

	for _, l := range lines[d.start:min(len(lines), d.start+d.page)] {
		row(l.text, false, l.selected, false)
	}

	if footer == 2 {
		row(tui.Clip(status, width), false, false, true)
	}

	if height > 1 {
		hint := "Esc  j/k  PgUp/PgDn  r refresh"
		if p.canVote() {
			hint = "Esc  Enter vote  j/k  r refresh"
			if p.multiple {
				hint = "Esc  Space select  Enter vote  j/k  r refresh"
			}

			if p.voted() {
				hint += "  x retract"
			}
		}

		row(tui.Clip(hint, width), false, false, true)
	}
}
