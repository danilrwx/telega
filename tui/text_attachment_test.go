package tui

import "testing"

func TestTextAttachmentDefaultName(t *testing.T) {
	var prompt TextAttachmentPrompt
	if action, name := prompt.Key("enter", ""); action != "edit" || name != "message.txt" {
		t.Fatalf("unexpected default file name: %q %q", action, name)
	}
}
