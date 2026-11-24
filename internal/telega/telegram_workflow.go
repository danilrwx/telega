package telega

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

func resolveAuthor(msg message, entities tg.Entities) message {
	if msg.avatarPeer.id == 0 && msg.out && !msg.service {
		for id, user := range entities.Users {
			if user != nil && user.Self {
				msg.avatarPeer = peerKey{'u', id}
				break
			}
		}
	}

	msg.avatar = entityAvatar(msg.avatarPeer, entities)

	if msg.forward != nil {
		source := *msg.forward
		if source.name == "" {
			source.name = entityName(source.author, entities)
		}

		if source.position.chat.kind == 'c' {
			if channel := entities.Channels[source.position.chat.id]; channel != nil {
				source.username = channel.Username
			}
		}

		msg.forward = &source
	}

	if msg.authorName == "" {
		if name := entityName(msg.author, entities); name != "" {
			msg.authorName, msg.authorDirectoryName = name, true
		}
	}

	return msg
}

func entityAvatar(peer peerKey, entities tg.Entities) *attachment {
	switch peer.kind {
	case 'u':
		return userAvatar(entities.Users[peer.id])
	case 'g':
		if chat := entities.Chats[peer.id]; chat != nil {
			if photo, ok := chat.Photo.(*tg.ChatPhoto); ok {
				return profileThumbnail(chat.AsInputPeer(), photo.PhotoID, photo.DCID)
			}
		}
	case 'c':
		if channel := entities.Channels[peer.id]; channel != nil {
			if photo, ok := channel.Photo.(*tg.ChatPhoto); ok {
				return profileThumbnail(channel.AsInputPeer(), photo.PhotoID, photo.DCID)
			}
		}
	}

	return nil
}

func userAvatar(user *tg.User) *attachment {
	if user != nil {
		if photo, ok := user.Photo.(*tg.UserProfilePhoto); ok {
			return profileThumbnail(user.AsInputPeer(), photo.PhotoID, photo.DCID)
		}
	}

	return nil
}

func profileThumbnail(peer tg.InputPeerClass, id int64, dc int) *attachment {
	if id == 0 || dc == 0 {
		return nil
	}

	return &attachment{dc: dc, location: &tg.InputPeerPhotoFileLocation{Peer: peer, PhotoID: id}}
}

func entityName(peer peerKey, entities tg.Entities) string {
	var name string

	switch peer.kind {
	case 'u':
		if user := entities.Users[peer.id]; user != nil {
			name = strings.TrimSpace(user.FirstName + " " + user.LastName)
			if name == "" {
				name = user.Username
			}
		}
	case 'g':
		if chat := entities.Chats[peer.id]; chat != nil {
			name = chat.Title
		}
	case 'c':
		if channel := entities.Channels[peer.id]; channel != nil {
			name = channel.Title
		}
	}

	return limitText(name, 80)
}

func lookupAuthor(ctx context.Context, api *tg.Client, req request) (string, *attachment) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserFromMessage{
		Peer: req.chat.input, MsgID: req.anchor, UserID: req.author.id,
	}})
	if err != nil {
		return "", nil
	}

	entities := tg.EntitiesFrom(users, nil)

	return entityName(req.author, entities), entityAvatar(req.author, entities)
}

func searchHistory(ctx context.Context, api *tg.Client, req request) ([]message, error) {
	query := &tg.MessagesSearchRequest{
		Peer: req.chat.input, Q: req.text, Filter: telegramSearchFilter(req.searchFilter), OffsetID: req.anchor, Limit: historyLimit,
	}
	if req.chat.threadID != 0 {
		query.SetTopMsgID(req.chat.threadID)
	}

	result, err := api.MessagesSearch(ctx, query)
	if err != nil {
		return nil, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, nil
	}

	entities := tg.EntitiesFrom(data.GetUsers(), data.GetChats())

	var out []message

	for _, item := range data.GetMessages() {
		if _, msg, ok := unwrapMessage(item); ok && (req.anchor == 0 || msg.id < req.anchor) {
			out = append(out, resolveAuthor(msg, entities))
		}
	}

	return out, nil
}

func findUnread(ctx context.Context, api *tg.Client, req request) ([]message, int, error) {
	for {
		items, err := loadHistory(ctx, api, req)
		if err != nil || len(items) == 0 {
			return nil, 0, err
		}

		for i := len(items) - 1; i >= 0; i-- {
			if items[i].id > req.anchor && !items[i].out {
				return items, items[i].id, nil
			}
		}

		if items[0].id <= req.anchor || req.chat.unread.topID != 0 && items[0].id >= req.chat.unread.topID {
			return nil, 0, nil
		}

		req.anchor = items[0].id
	}
}

func editMessage(ctx context.Context, api *tg.Client, req request) ([]message, error) {
	text, entities := codeBlocks(req.text)
	query := &tg.MessagesEditMessageRequest{Peer: req.chat.input, ID: req.anchor}
	query.SetMessage(text)
	query.SetEntities(entities)

	result, err := api.MessagesEditMessage(ctx, query)
	if err != nil {
		return nil, err
	}

	for _, msg := range messagesFromUpdates(result, req.chat.key) {
		if msg.id == req.anchor {
			return []message{msg}, nil
		}
	}

	return nil, nil
}

func messagesFromUpdates(result tg.UpdatesClass, chat peerKey) []message {
	var (
		updates  []tg.UpdateClass
		entities tg.Entities
	)

	switch update := result.(type) {
	case *tg.Updates:
		updates, entities = update.Updates, tg.EntitiesFrom(update.Users, update.Chats)
	case *tg.UpdatesCombined:
		updates, entities = update.Updates, tg.EntitiesFrom(update.Users, update.Chats)
	case *tg.UpdateShort:
		updates = []tg.UpdateClass{update.Update}
	}

	var messages []message

	for _, item := range updates {
		if update, ok := item.(interface{ GetMessage() tg.MessageClass }); ok {
			if key, msg, ok := unwrapMessage(update.GetMessage()); ok && key == chat {
				messages = append(messages, resolveAuthor(msg, entities))
			}
		}
	}

	return messages
}

func forwardMessages(ctx context.Context, api *tg.Client, req request) ([]int, []message, error) {
	var (
		sent     []int
		messages []message
	)

	for ids := range slices.Chunk(req.ids, 100) {
		random := make([]int64, len(ids))
		for i := range random {
			random[i] = randomMessageID()
		}

		result, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
			FromPeer: req.chat.input, ToPeer: req.to.input, ID: ids, RandomID: random,
		})
		if err != nil {
			return sent, messages, err
		}

		sent = append(sent, ids...)
		if req.to.key == req.chat.key {
			messages = append(messages, messagesFromUpdates(result, req.to.key)...)
		}
	}

	return sent, messages, nil
}
