package tui

import (
	"context"
	"fmt"

	"github.com/danilrwx/telega/updater"
)

type updatePreferences struct {
	Disabled bool `json:"disabled"`
}

// UpdateDialog is shared by the clients' startup update prompts.
type UpdateDialog struct {
	App                   updater.App
	Release               updater.Release
	Selected              int
	Status                string
	Installing, Installed bool
}

// CheckForUpdate respects a persistent opt-out and skips development builds.
func CheckForUpdate(ctx context.Context, app updater.App) (*UpdateDialog, error) {
	if !updater.IsRelease(app.Version) {
		return nil, nil
	}

	var preferences updatePreferences
	if err := ReadSettings(app.Name+"-updates", &preferences); err != nil {
		return nil, err
	}

	if preferences.Disabled {
		return nil, nil
	}

	release, err := app.Check(ctx)
	if err != nil || !app.Newer(release) {
		return nil, err
	}

	return &UpdateDialog{App: app, Release: release, Selected: 1}, nil
}

// Key returns install or close; saving a permanent dismissal can fail visibly.
func (d *UpdateDialog) Key(key string) string {
	if d.Installing {
		return ""
	}

	switch key {
	case "esc", "escape", "q", "n":
		return "close"
	case "up", "k":
		d.Selected = max(0, d.Selected-1)
	case "down", "j", "tab":
		d.Selected = (d.Selected + 1) % 3
	case "enter", "y":
		if d.Installed {
			return "close"
		}

		if key == "y" {
			d.Selected = 0
		}

		switch d.Selected {
		case 0:
			d.Installing, d.Status = true, "Downloading and verifying the update…"
			return "install"
		case 1:
			return "close"
		case 2:
			if err := WriteSettings(d.App.Name+"-updates", updatePreferences{Disabled: true}); err != nil {
				d.Status = "Could not save update preferences: " + err.Error()
				return ""
			}

			return "close"
		}
	}

	return ""
}

func (d *UpdateDialog) Complete(err error) {
	d.Installing = false
	if err != nil {
		d.Status = "Update failed: " + err.Error()
		return
	}

	d.Installed, d.Status = true, "Update installed. Restart the client to use it."
}

func (d *UpdateDialog) Render(width, height int, row func(string, bool, bool, bool)) {
	choices := []string{"Install update", "Later", "Don't show again"}
	if d.Installing || d.Installed {
		choices = nil
	}

	title := fmt.Sprintf("Update %s to %s", d.App.Name, d.Release.Version)

	hint := "jk choose  Enter confirm  Esc later"
	if d.Installed {
		hint = "Enter/Esc close"
	} else if d.Installing {
		hint = "Installing…"
	}

	ListDialog(title, choices, d.Selected, width, height, d.Status, hint, row)
}
