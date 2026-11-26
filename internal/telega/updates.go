package telega

import (
	"context"
	"os"

	"github.com/danilrwx/telega/tui"
)

func (a *app) showPendingUpdate(cols, rows int) {
	if a.pendingUpdate != nil && a.ready && !a.composing && !a.searching && !a.pickingReaction && a.voice == nil && a.imagePreview == nil && len(a.dialogPopup(cols, rows).Lines) == 0 {
		a.updateDialog, a.pendingUpdate = a.pendingUpdate, nil
	}
}

func (a *app) updateInput(k key) {
	d := a.updateDialog

	name := k.name
	if name == "" {
		name = commandKey(k).text
	}

	switch d.Key(name) {
	case "close":
		a.updateDialog = nil
	case "install":
		a.startUpdate(d)
	}
}

func installUpdate(ctx context.Context, d *tui.UpdateDialog) error {
	target, err := os.Executable()
	if err != nil {
		return err
	}

	return d.App.Install(ctx, d.Release, target)
}
