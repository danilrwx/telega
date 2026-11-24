package telega

import "github.com/danilrwx/telega/tui"

type dmAvatarMode string

const (
	dmAvatarHeader   dmAvatarMode = "header"
	dmAvatarMessages dmAvatarMode = "messages"
	dmAvatarOff      dmAvatarMode = "off"
)

type displaySettings struct {
	tui.AvatarSettings
	HideDMNames          bool         `json:"hide_dm_names,omitempty"`
	DMRight              bool         `json:"dm_outgoing_right,omitempty"`
	DMAvatars            dmAvatarMode `json:"dm_avatars,omitempty"`
	HideDMHeaderAvatar   bool         `json:"hide_dm_header_avatar,omitempty"`
	HideChatListWhenOpen bool         `json:"hide_chat_list_when_open,omitempty"`
}

const displaySettingsCount = 6

func (s displaySettings) showDMMessageAvatars() bool {
	return s.DMAvatars != dmAvatarHeader && s.DMAvatars != dmAvatarOff
}

func (s displaySettings) rows() []string {
	onOff := func(on bool) string {
		if on {
			return "On"
		}

		return "Off"
	}

	return []string{
		"Avatars  " + s.AvatarMode(),
		"Names in DMs  " + onOff(!s.HideDMNames),
		"Outgoing on the right in DMs  " + onOff(s.DMRight),
		"Avatars beside DM messages  " + onOff(s.showDMMessageAvatars()),
		"Avatar in DM header  " + onOff(!s.HideDMHeaderAvatar),
		"Hide chat list when open  " + onOff(s.HideChatListWhenOpen),
	}
}

func (a *app) toggleDisplaySetting() {
	switch a.settingsCursor {
	case 0:
		a.display.HideAvatars = !a.display.HideAvatars
	case 1:
		a.display.HideDMNames = !a.display.HideDMNames
	case 2:
		a.display.DMRight = !a.display.DMRight
	case 3:
		if a.display.showDMMessageAvatars() {
			a.display.DMAvatars = dmAvatarOff
		} else {
			a.display.DMAvatars = dmAvatarMessages
		}
	case 4:
		a.display.HideDMHeaderAvatar = !a.display.HideDMHeaderAvatar
	case 5:
		a.display.HideChatListWhenOpen = !a.display.HideChatListWhenOpen
	}

	a.status = ""

	if !a.demo {
		if err := tui.WriteSettings("telega", a.display); err != nil {
			a.status = "Settings: " + err.Error()
		}
	}
}
