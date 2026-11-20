package telega

import (
	"context"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type sendInvoker struct{ tg.Invoker }

func (i sendInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	return i.Invoker.Invoke(ctx, input, output)
}

func runTelegram(ctx context.Context, cfg config, qr bool, requests <-chan request, events chan<- serverEvent, checks <-chan noticeCheck) {
	for {
		err := telegramLoop(ctx, cfg, qr, requests, events, checks)
		if ctx.Err() != nil || err == nil {
			return
		}

		if emit(ctx, events, serverEvent{kind: "offline", text: err.Error()}) != nil {
			return
		}

	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-requests:
				if req.kind == "reconnect" {
					break wait
				}
			}
		}
	}
}
