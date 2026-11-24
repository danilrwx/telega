package telega

import (
	"time"

	"github.com/gotd/td/tg"
)

func presenceText(status tg.UserStatusClass, now time.Time) string {
	switch status := status.(type) {
	case *tg.UserStatusOnline:
		if expiry := time.Unix(int64(status.Expires), 0); expiry.After(now) {
			return "online"
		}

		return "last seen recently"
	case *tg.UserStatusOffline:
		if status.WasOnline <= 0 {
			return ""
		}

		last := time.Unix(int64(status.WasOnline), 0).In(now.Location())
		if last.Format(time.DateOnly) == now.Format(time.DateOnly) {
			return "last seen at " + last.Format("15:04")
		}

		return "last seen " + last.Format("02 Jan 2006 15:04")
	case *tg.UserStatusRecently:
		return "last seen recently"
	case *tg.UserStatusLastWeek:
		return "last seen within a week"
	case *tg.UserStatusLastMonth:
		return "last seen within a month"
	default:
		return ""
	}
}

func (a *app) presenceRemaining() time.Duration {
	if a.open {
		if status, ok := a.currentChat().presence.(*tg.UserStatusOnline); ok {
			return time.Until(time.Unix(int64(status.Expires), 0))
		}
	}

	return 0
}
