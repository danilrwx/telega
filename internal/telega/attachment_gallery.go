package telega

import (
	"fmt"
	"strings"

	"github.com/danilrwx/telega/tui"
)

var attachmentGalleryTabs = [...]struct {
	label  string
	filter int
}{
	{"Media", searchPhotoVideo},
	{"Files", 2},
	{"Links", 3},
	{"Music", searchMusic},
	{"Voice", 4},
}

const galleryCellWidth, galleryCellHeight = 16, 6

type attachmentGallery struct {
	tab, columns, first, visible int
}

func (a *app) beginAttachmentGallery(out chan<- request) {
	if a.pending != "" {
		return
	}

	scope := a.currentChat()
	if a.demo || scope.input == nil {
		a.showChatAttachments()

		return
	}

	scope.threadKind, scope.threadID = 0, 0
	a.status = ""
	a.messageSearch = &tui.MessageSearch{}
	a.messageSearchScope = scope
	a.attachmentGallery = &attachmentGallery{}
	a.requestGalleryPage(out, false)
}

func (a *app) requestGalleryPage(out chan<- request, more bool) {
	dialog := a.messageSearch
	if !more {
		dialog.Revision++
		dialog.Selected, dialog.More = 0, false
		a.attachmentGallery.first = 0
		a.messageSearchHits = nil
		a.messageSearchCursor = messageSearchCursor{}
	}

	dialog.Busy, dialog.Status = true, ""

	out <- request{
		kind: "messageSearch", chat: a.messageSearchScope, messageSearch: dialog,
		searchID: dialog.Revision, searchCursor: a.messageSearchCursor,
		searchFilter:      attachmentGalleryTabs[a.attachmentGallery.tab].filter,
		browseAttachments: true,
	}
}

func (a *app) attachmentGalleryInput(k key, out chan<- request) {
	gallery, dialog := a.attachmentGallery, a.messageSearch

	name := k.name
	if name == "" {
		name = k.text
	}

	hits := a.messageSearchHits
	last := len(hits) - 1
	step := max(1, gallery.columns)

	var delta int

	switch name {
	case "escape", "V":
		if a.pending != "download" {
			a.closeMessageSearch()
		}
	case "tab", "shift+tab":
		if a.pending == "download" {
			return
		}

		direction := 1
		if name == "shift+tab" {
			direction = -1
		}

		gallery.tab = (gallery.tab + direction + len(attachmentGalleryTabs)) % len(attachmentGalleryTabs)
		a.status = ""
		a.requestGalleryPage(out, false)
	case "left", "h":
		delta = -1
	case "right", "l":
		delta = 1
	case "up", "k":
		delta = -step
	case "down", "j":
		delta = step
	case "pageup":
		delta = -max(step, gallery.visible)
	case "pagedown":
		delta = max(step, gallery.visible)
	case "R":
		if !dialog.Busy {
			a.requestGalleryPage(out, dialog.More)
		}
	case "enter", "g", "alt+r":
		if last < 0 || a.pending != "" {
			return
		}

		hit := hits[dialog.Selected]
		if name == "g" {
			a.closeMessageSearch()
			a.jumpToSearchHit(out, hit, false)
		} else if name == "alt+r" {
			a.beginVoiceTranscript(out, hit.chat, hit.msg)
		} else if gallery.tab == 2 {
			a.chooseLink(hit.msg)
		} else if file := hit.msg.attachment; file != nil && file.location != nil {
			a.ask(out, request{kind: "download", chat: hit.chat, anchor: hit.msg.id, attachment: file})
		} else {
			a.status = "Attachment unavailable"
		}
	}

	if delta == 0 || last < 0 {
		return
	}

	target := dialog.Selected + delta
	dialog.Selected = min(last, max(0, target))

	if target > last && dialog.More && !dialog.Busy {
		a.requestGalleryPage(out, true)
	}
}

func galleryThumbnail(file *attachment) (string, *attachment) {
	if file != nil && file.kind == "Video" && file.thumbnail != nil {
		return inlineThumbnail(file)
	}

	return inlineAttachment(file)
}

func (a *app) loadGalleryThumbnails(out chan<- request) {
	gallery := a.attachmentGallery
	if gallery.columns < 2 || gallery.tab != 0 || a.imagePreview != nil {
		return
	}

	hits := a.messageSearchHits
	for _, hit := range hits[gallery.first:min(len(hits), gallery.first+gallery.visible)] {
		key, file := galleryThumbnail(hit.msg.attachment)
		if key != "" && a.inlineImages.Start(key) {
			a.inlineLoading = true

			out <- request{kind: "inlineImage", chat: hit.chat, anchor: hit.msg.id, attachment: file, text: key}

			return
		}
	}
}

func (a *app) paintAttachmentGallery(b *strings.Builder, label func(int, int, int, string), cols, rows int) {
	gallery, dialog := a.attachmentGallery, a.messageSearch
	hits := a.messageSearchHits
	width := cols - 2
	colors := a.palette()
	tab := attachmentGalleryTabs[gallery.tab].label
	styled := func(x, y, width int, style, text string) {
		b.WriteString(style)
		label(x, y, width, text)
		b.WriteString("\x1b[0m")
	}

	styled(2, 1, width, colors.heading, "Attachments · "+a.messageSearchScope.title)

	tabs := make([]string, len(attachmentGalleryTabs))
	for i, pane := range attachmentGalleryTabs {
		tabs[i] = pane.label
		if i == gallery.tab {
			tabs[i] = "[" + pane.label + "]"
		}
	}

	tabBar := strings.Join(tabs, "  ")
	if len(tabBar) > width {
		tabBar = "‹ [" + tab + "] ›"
	}

	styled(2, 2, width, colors.heading, tabBar)

	columns, lines := 1, 1

	grid := gallery.tab == 0 && a.inlineImages != nil && rows-4 >= galleryCellHeight && width >= 2*galleryCellWidth
	if grid {
		columns, lines = min(8, width/galleryCellWidth), galleryCellHeight
	}

	visibleRows := max(1, (rows-4)/lines)
	if grid {
		visibleRows = min(8, visibleRows)
	}

	capacity := visibleRows * columns
	first := max(0, dialog.Selected/columns-visibleRows+1) * columns
	gallery.columns, gallery.first, gallery.visible = columns, first, capacity

	for i, hit := range hits[first:min(len(hits), first+capacity)] {
		msg := hit.msg

		title := messageExcerpt(msg, 0)
		if gallery.tab != 2 && msg.attachment != nil {
			title = tui.AttachmentName(msg.attachment.name)
		}

		style, cursor := "", "  "
		if first+i == dialog.Selected {
			style, cursor = colors.selected, "› "
		}

		if !grid {
			styled(2, i+3, width, style, cursor+msg.date.Local().Format("02 Jan 15:04")+"  "+tui.Clip(title, width-17))

			continue
		}

		x, y := 2+i%columns*galleryCellWidth, 3+i/columns*galleryCellHeight

		key, _ := galleryThumbnail(msg.attachment)
		if image := a.inlineImages.Get(key); image != nil {
			for j, row := range image.Rows {
				fmt.Fprintf(b, "\x1b[%d;%dH%s", y+j, x, row)
			}
		}

		styled(x, y+4, galleryCellWidth-1, style, cursor+tui.Clip(title, galleryCellWidth-3))
	}

	status := dialog.Status
	switch {
	case a.pending == "download":
		status = "Opening attachment…"
	case dialog.Busy:
		status = "Loading attachments…"
	case status != "":
		status += "  R retry"
	case a.status != "":
		status = a.status
	case a.playback != nil:
		status = a.playback.hint()
	case len(hits) == 0:
		status = "No " + strings.ToLower(tab) + " in this chat"
	default:
		status = fmt.Sprintf("%d / %d", dialog.Selected+1, len(hits))
		if dialog.More {
			status += "  ·  PgDn more"
		}
	}

	hint := "Tab type  Enter open  Esc back  hjkl move  g message"
	if gallery.tab == 4 {
		hint = "Enter play  Alt+R transcript  Esc back  Tab type  hjkl move  g message"
	}

	styled(2, rows-1, width, colors.muted, tui.Clip(status, width))
	styled(2, rows, width, colors.muted, tui.Clip(hint, width))
}
