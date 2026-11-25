package tui

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/danilrwx/telega/updater"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateLaterPromptsAgainAndOptOutPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	previous := http.DefaultClient
	requests := 0
	http.DefaultClient = &http.Client{Transport: updateTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.2.0"}`))}, nil
	})}

	t.Cleanup(func() { http.DefaultClient = previous })

	app := updater.App{Name: "test-updates", Repository: "test/tool", Version: "v0.1.0"}

	dialog, err := CheckForUpdate(t.Context(), app)
	if err != nil || dialog == nil {
		t.Fatalf("missing startup prompt: %v", err)
	}

	if dialog.Selected != 1 || dialog.Key("enter") != "close" {
		t.Fatal("Later should be the initial choice")
	}

	dialog, err = CheckForUpdate(t.Context(), app)
	if err != nil || dialog == nil {
		t.Fatal("Later suppressed the next startup prompt")
	}

	dialog.Key("j")

	if dialog.Key("enter") != "close" {
		t.Fatal("permanent dismissal did not close the prompt")
	}

	dialog, err = CheckForUpdate(t.Context(), app)
	if err != nil || dialog != nil || requests != 2 {
		t.Fatalf("opt-out did not persist: %v, %d requests", err, requests)
	}
}

func TestUpdateDialogInstallationAndFailure(t *testing.T) {
	dialog := UpdateDialog{Selected: 1}
	if dialog.Key("y") != "install" || !dialog.Installing {
		t.Fatal("installation was not requested")
	}

	if dialog.Key("esc") != "" || dialog.Key("enter") != "" {
		t.Fatal("installation can be dismissed or started twice")
	}

	dialog.Complete(io.ErrUnexpectedEOF)

	if dialog.Installing || dialog.Installed || !strings.Contains(dialog.Status, "Update failed") {
		t.Fatal("installation failure was hidden")
	}

	if dialog.Key("enter") != "install" {
		t.Fatal("a failed download should be retryable")
	}

	dialog.Complete(nil)

	if !dialog.Installed || dialog.Key("enter") != "close" {
		t.Fatal("successful installation should close on Enter")
	}
}
