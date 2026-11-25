package telega

import (
	"errors"
	"strings"
	"testing"

	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
)

func TestUpdateDialogInstallsAndKeepsErrorsVisible(t *testing.T) {
	dialog := &tui.UpdateDialog{App: updater.App{Name: "telega"}, Release: updater.Release{Version: "v0.3.0"}, Selected: 1}
	installed := false
	a := app{ready: true, updateDialog: dialog, startUpdate: func(d *tui.UpdateDialog) { installed = d == dialog }}
	a.input(key{text: "y"}, nil)

	if !installed || !dialog.Installing {
		t.Fatal("update confirmation did not start installation")
	}

	a.input(key{name: "escape"}, nil)

	if a.updateDialog != dialog {
		t.Fatal("the installation dialog closed while replacing the binary")
	}

	a.receive(nil, serverEvent{kind: "installUpdate", updateDialog: dialog, updateError: errors.New("permission denied")})

	var frame strings.Builder
	if err := paint(&frame, &a, 96, 24); err != nil || !strings.Contains(frame.String(), "permission denied") {
		t.Fatalf("update error was hidden: %v", err)
	}

	a.input(key{name: "escape"}, nil)

	if a.updateDialog != nil {
		t.Fatal("failed update cannot be dismissed")
	}
}
