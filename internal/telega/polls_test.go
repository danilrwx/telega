package telega

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func pollAnswer(text string, token ...byte) *tg.PollAnswer {
	return &tg.PollAnswer{Text: tg.TextWithEntities{Text: text}, Option: token}
}

func pollFixture() *tg.MessageMediaPoll {
	return &tg.MessageMediaPoll{Poll: tg.Poll{
		ID: 50, Question: tg.TextWithEntities{Text: "Favorite\ncolors?"},
		Answers: []tg.PollAnswerClass{pollAnswer("Blue", 0, 0xff), pollAnswer("Green", 4)},
	}}
}

func pollResult(id string, count int, chosen, correct bool) tg.PollAnswerVoters {
	r := tg.PollAnswerVoters{Option: []byte(id), Chosen: chosen, Correct: correct}
	r.SetVoters(count)
	return r
}

func TestPollParsingAndMerging(t *testing.T) {
	if pollFromMedia(&tg.MessageMediaEmpty{}) != nil {
		t.Fatal("non-poll media parsed as a poll")
	}

	m := pollFixture()
	m.Poll.Quiz = true
	m.Poll.Answers = append(m.Poll.Answers, pollAnswer("duplicate", 4), pollAnswer("invalid"))
	m.Results.SetResults([]tg.PollAnswerVoters{
		pollResult("\x04", 3, true, true), pollResult("\x00\xff", 0, false, false), pollResult("unknown", 2, false, false),
	})
	m.Results.SetTotalVoters(3)
	m.Results.SetSolution("Green is correct")
	p := pollFromMedia(m)
	if p.preview() != "Quiz: Favorite colors?" || !p.choiceKnown || len(p.options) != 2 || p.total != 3 {
		t.Fatalf("unexpected poll: %#v", p)
	}
	if a, b := p.options[0], p.options[1]; a.id != "\x00\xff" || !a.hasCount || a.count != 0 || !b.chosen || !b.correct || b.count != 3 {
		t.Fatalf("wrong result/option mapping: %#v", p.options)
	}

	// A min result does not have the user's chosen answer. Definition refreshes
	// may also reorder options; the opaque tokens identify unchanged answers.
	reordered := m.Poll
	reordered.Answers = []tg.PollAnswerClass{pollAnswer("Green!", 4), pollAnswer("Blue", 0, 0xff)}
	partial := tg.PollResults{Min: true, Results: []tg.PollAnswerVoters{pollResult("\x04", 5, false, false)}}
	partial.SetTotalVoters(5)
	q := mergePoll(p, &pollUpdate{definition: &reordered, results: partial})
	if !q.options[0].chosen || !q.options[0].correct || q.options[0].text != "Green!" || q.options[0].count != 5 || q.options[1].hasCount || q.solution == "" {
		t.Fatalf("partial merge: %#v, options %#v", q, q.options)
	}
	if p.options[1].count != 3 || p.options[1].text != "Green" {
		t.Fatal("merge mutated the original poll")
	}

	q = mergePoll(q, &pollUpdate{results: tg.PollResults{Results: []tg.PollAnswerVoters{pollResult("\x04", 4, false, true)}}})
	if !q.choiceKnown || q.voted() {
		t.Fatalf("full update did not clear withdrawn vote: %#v", q)
	}
}

func TestPollRPCUsesMessageAndOpaqueOptions(t *testing.T) {
	c := chat{key: peerKey{'c', 5}, input: &tg.InputPeerChannel{ChannelID: 5}, threadID: 3}
	partial := &tg.UpdateMessagePoll{PollID: 50, Results: tg.PollResults{Min: true}}
	full := &tg.UpdateMessagePoll{PollID: 50, Results: tg.PollResults{TotalVoters: 3}}
	bad := &tg.UpdateMessagePoll{PollID: 50, Peer: &tg.PeerChannel{ChannelID: 99}, MsgID: 42}
	for _, tc := range []struct {
		name    string
		kind    string
		choices [][]byte
		voteErr error
	}{
		{name: "refresh", kind: "pollResults"},
		{name: "vote", kind: "pollVote", choices: [][]byte{{0, 255}, {4}}},
		{name: "retract", kind: "pollVote"},
		{name: "reject", kind: "pollVote", voteErr: errors.New("POLL_CLOSED")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request{kind: tc.kind, anchor: 42, chat: c, poll: &pollRequest{id: 50, choices: tc.choices}}
			var gets, votes int
			e := fetchPoll(t.Context(), req,
				func(ctx context.Context, r *tg.MessagesGetPollResultsRequest) (tg.UpdatesClass, error) {
					gets++
					if _, ok := ctx.Deadline(); !ok || r.MsgID != 42 || r.Peer != c.input || r.PollHash != 0 {
						t.Fatalf("wrong request: %#v", r)
					}
					return &tg.UpdatesCombined{Updates: []tg.UpdateClass{full, partial, bad}}, nil
				},
				func(_ context.Context, r *tg.MessagesSendVoteRequest) (tg.UpdatesClass, error) {
					votes++
					if r.MsgID != 42 || r.Peer != c.input || !reflect.DeepEqual(r.Options, tc.choices) {
						t.Fatalf("wrong vote: %#v", r)
					}
					return &tg.UpdateShort{Update: partial}, tc.voteErr
				})
			if e.pollRequest != req.poll || e.chat != c.key || e.threadID != c.threadID || e.anchor != req.anchor {
				t.Fatalf("lost request identity: %#v", e)
			}
			if tc.voteErr != nil {
				if gets != 0 || votes != 1 || e.poll != nil || !strings.Contains(e.hint, "POLL_CLOSED") {
					t.Fatalf("bad failure: got %d refresh, %d vote, event %#v", gets, votes, e)
				}
				return
			}
			if gets != 1 || votes > 1 || (votes == 1) != (tc.kind == "pollVote") || e.hint != "" || e.poll == nil || e.poll.results.TotalVoters != 3 || !e.poll.matches(c.key, 42, 50) || e.poll.matches(c.key, 41, 50) {
				t.Fatalf("bad result: got %d refresh, %d vote, event %#v", gets, votes, e)
			}
		})
	}
}

func TestPollDialogVotesAndIgnoresStaleCompletion(t *testing.T) {
	c := chat{key: peerKey{'g', 5}, input: &tg.InputPeerChat{ChatID: 5}, threadID: 3}
	m := pollFixture()
	m.Poll.MultipleChoice = true
	p := pollFromMedia(m)
	a := &app{open: true, thread: &c, log: []message{{id: 42, poll: p, text: p.preview()}}}
	out := make(chan request, 8)
	if !a.openPoll(out) {
		t.Fatal("could not open poll")
	}
	refresh := <-out
	if refresh.kind != "pollResults" || refresh.anchor != 42 || refresh.chat.threadID != c.threadID {
		t.Fatalf("bad refresh: %#v", refresh)
	}

	a.receivePoll(serverEvent{kind: "pollResults", pollRequest: &pollRequest{id: 50}})
	if a.pollDialog.pending != refresh.poll {
		t.Fatal("stale completion cleared refresh")
	}
	a.receivePoll(serverEvent{kind: "pollResults", pollRequest: refresh.poll})
	a.pollInput(key{name: "enter"}, out)
	if len(out) != 0 {
		t.Fatal("voted without selecting any option")
	}

	a.pollInput(key{text: " "}, out)
	a.pollInput(key{name: "down"}, out)
	a.pollInput(key{text: " "}, out)
	a.pollInput(key{name: "enter"}, out)
	v := <-out
	if v.kind != "pollVote" || !reflect.DeepEqual(v.poll.choices, [][]byte{{0, 255}, {4}}) {
		t.Fatalf("wrong vote: %#v", v.poll)
	}
	// Ignore updates with matching poll ID but wrong message identity.
	wrong := pollEvent(&tg.UpdateMessagePoll{PollID: 50, MsgID: 43, Poll: tg.Poll{ID: 50, Closed: true}})
	a.receivePoll(wrong)
	if a.pollDialog.poll.closed || a.log[0].poll.closed {
		t.Fatal("update changed a different message")
	}

	e := pollEvent(&tg.UpdateMessagePoll{PollID: 50, Peer: &tg.PeerChat{ChatID: 5}, MsgID: 42,
		Results: tg.PollResults{Results: []tg.PollAnswerVoters{pollResult("\x04", 1, true, false)}}})
	e.kind, e.pollRequest = "pollVote", v.poll
	a.receivePoll(e)
	if !a.log[0].poll.voted() || a.pollDialog.draft != nil || a.pollDialog.pending != nil {
		t.Fatal("vote acknowledgement not applied")
	}

	a.pollInput(key{text: "x"}, out)
	x := <-out
	if x.kind != "pollVote" || len(x.poll.choices) != 0 {
		t.Fatalf("wrong retraction: %#v", x)
	}
	a.pollInput(key{name: "escape"}, out)
	if a.pollDialog != nil {
		t.Fatal("Escape did not close poll")
	}
}

func TestPollVotingRestrictionsAndRenderedResults(t *testing.T) {
	m := pollFixture()
	m.Poll.Quiz, m.Poll.HideResultsUntilClose, m.Poll.RevotingDisabled = true, true, true
	m.Results.SetTotalVoters(4)
	m.Results.SetResults([]tg.PollAnswerVoters{pollResult("\x04", 3, true, true)})
	m.Results.SetSolution("A reason")
	p := pollFromMedia(m)
	d := &pollDialog{poll: p}
	render := func(width, height int) string {
		var lines []string
		d.render(width, height, func(text string, _, _, _ bool) { lines = append(lines, text) })
		if len(lines) > height {
			t.Fatalf("overflow: %d rows for %d", len(lines), height)
		}
		return strings.Join(lines, "\n")
	}
	if text := render(56, 16); p.canVote() || strings.Contains(text, "75%") || strings.Contains(text, "reason") || strings.Contains(text, "voters") {
		t.Fatalf("hidden/locked poll: %s", text)
	}
	p.closed = true
	if text := render(56, 16); p.canVote() || !strings.Contains(text, "75%") || !strings.Contains(text, "A reason") || !strings.Contains(text, "correct") {
		t.Fatalf("closed results: %s", text)
	}
	p.closed, p.noRevote, p.closeDate = false, false, int(time.Now().Add(-time.Hour).Unix())
	if p.canVote() {
		t.Fatal("poll past close date was votable")
	}
	m.Results.Min = true
	if pollFromMedia(m).canVote() {
		t.Fatal("min results make own vote unknown")
	}
	_ = render(5, 1)
}
