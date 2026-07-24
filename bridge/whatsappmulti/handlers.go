package bwhatsapp

import (
	"context"
	"fmt"
	"mime"
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// nolint:gocritic
func (b *Bwhatsapp) eventHandler(evt any) {
	switch e := evt.(type) {
	case *events.Message:
		b.handleMessage(e)
	case *events.GroupInfo:
		b.handleGroupInfo(e)
	}
}

func (b *Bwhatsapp) handleGroupInfo(event *events.GroupInfo) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	if mydebug {
		b.Log.Debugf("Receiving event %#v", event)
	}

	switch {
	case event.Join != nil:
		b.handleUserJoin(event)
	case event.Leave != nil:
		b.handleUserLeave(event)
	case event.Topic != nil:
		b.handleTopicChange(event)
	}
}

func (b *Bwhatsapp) handleUserJoin(event *events.GroupInfo) {
	for _, joinedJid := range event.Join {

		rmsg := config.Message{
			UserID:   joinedJid.String(),
			Username: b.getSenderNameFromJID(joinedJid),
			Channel:  event.JID.String(),
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventJoin,
			Text:     "joined chat",
		}

		b.Remote <- rmsg
	}
}

func (b *Bwhatsapp) handleUserLeave(event *events.GroupInfo) {
	for _, leftJid := range event.Leave {

		rmsg := config.Message{
			UserID:   leftJid.String(),
			Username: b.getSenderNameFromJID(leftJid),
			Channel:  event.JID.String(),
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventLeave,
			Text:     "left chat",
		}

		b.Remote <- rmsg
	}
}

func (b *Bwhatsapp) handleTopicChange(event *events.GroupInfo) {
	msg := event.Topic
	senderJid := msg.TopicSetBy

	text := msg.Topic
	if text == "" {
		text = "removed topic"
	}

	rmsg := config.Message{
		UserID:   senderJid.String(),
		Username: b.getSenderNameFromJID(senderJid),
		Channel:  event.JID.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Event:    config.EventTopicChange,
		Text:     "Topic changed: " + text,
	}

	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleMessage(message *events.Message) {
	defer b.waHandlePanic()

	msg := message.Message

	b.RLock()
	switch {
	case msg == nil, message.Info.IsFromMe, message.Info.Timestamp.Before(b.startedAt):
		b.RUnlock()
		return
	}

	mydebug := b.DebugMode
	b.RUnlock()

	if mydebug {
		b.Log.Debugf("Receiving message %#v", msg)
	}

	switch {
	case msg.Conversation != nil || msg.ExtendedTextMessage != nil:
		b.handleTextMessage(message, msg)
	case msg.VideoMessage != nil:
		b.handleVideoMessage(message)
	case msg.AudioMessage != nil:
		b.handleAudioMessage(message)
	case msg.DocumentMessage != nil:
		b.handleDocumentMessage(message)
	case msg.ImageMessage != nil:
		b.handleImageMessage(message)
	default:
		isProto := msg.GetProtocolMessage()
		if isProto != nil && isProto.GetType() == waE2E.ProtocolMessage_REVOKE {
			b.handleDelete(isProto)
		}
	}
}

// nolint:funlen
func (b *Bwhatsapp) handleTextMessage(message *events.Message, msg *waE2E.Message) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	senderJID := message.Info.Sender
	channel := message.Info.Chat

	if msg.GetExtendedTextMessage() == nil && msg.GetConversation() == "" {
		if mydebug {
			b.Log.Debugf("message without text content? %#v", msg)
		}

		return
	}

	var text string

	// nolint:nestif
	if msg.GetExtendedTextMessage() == nil {
		text = msg.GetConversation()
	} else if msg.GetExtendedTextMessage().GetContextInfo() == nil {
		// Handle pure text message with a link preview
		// A pure text message with a link preview acts as an extended text message but will not contain any context info
		text = msg.GetExtendedTextMessage().GetText()
	} else {
		text = msg.GetExtendedTextMessage().GetText()
		ci := msg.GetExtendedTextMessage().GetContextInfo()

		if senderJID == (types.JID{}) && ci.Participant != nil {
			senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
		}

		if ci.MentionedJID != nil {
			// handle user mentions
			for _, mentionedJID := range ci.MentionedJID {
				numberAndSuffix := strings.SplitN(mentionedJID, "@", 2)

				// mentions comes as telephone numbers and we don't want to expose it to other bridges
				// replace it with something more meaninful to others
				mention := b.getSenderNotify(types.NewJID(numberAndSuffix[0], types.DefaultUserServer))

				text = strings.Replace(text, "@"+numberAndSuffix[0], "@"+mention, 1)
			}
		}
	}

	parentID := ""
	if msg.GetExtendedTextMessage() != nil {
		ci := msg.GetExtendedTextMessage().GetContextInfo()
		parentID = getParentIdFromCtx(ci)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: b.getSenderName(message),
		Text:     text,
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]any),
		ID:       getMessageIdFormat(senderJID, message.Info.ID),
		ParentID: parentID,
	}

	b.avatarsMu.RLock()
	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.avatarsMu.RUnlock()

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}

// HandleImageMessage sent from WhatsApp, relay it to the brige
func (b *Bwhatsapp) handleImageMessage(msg *events.Message) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	imsg := msg.Message.GetImageMessage()

	senderJID := msg.Info.Sender
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: b.getSenderName(msg),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]any),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	b.avatarsMu.RLock()
	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.avatarsMu.RUnlock()

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	// rename .jfif to .jpg https://github.com/42wim/matterbridge/issues/1292
	if fileExt[0] == ".jfif" {
		fileExt[0] = ".jpg"
	}

	// rename .jpe to .jpg https://github.com/42wim/matterbridge/issues/1463
	if fileExt[0] == ".jpe" {
		fileExt[0] = ".jpg"
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[0])

	if mydebug {
		b.Log.Debugf("Trying to download %s with type %s", filename, imsg.GetMimetype())
	}

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download image failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}

// HandleVideoMessage downloads video messages
func (b *Bwhatsapp) handleVideoMessage(msg *events.Message) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	imsg := msg.Message.GetVideoMessage()

	senderJID := msg.Info.Sender
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: b.getSenderName(msg),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]any),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	b.avatarsMu.RLock()
	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.avatarsMu.RUnlock()

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	if len(fileExt) == 0 {
		fileExt = append(fileExt, ".mp4")
	}

	// Prefer .mp4 extension, otherwise fallback to first index
	fileExtIndex := 0
	for i, n := range fileExt {
		if n == ".mp4" {
			fileExtIndex = i
			break
		}
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[fileExtIndex])

	if mydebug {
		b.Log.Debugf("Trying to download %s with size %#v and type %s", filename, imsg.GetFileLength(), imsg.GetMimetype())
	}

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download video failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}

// HandleAudioMessage downloads audio messages
func (b *Bwhatsapp) handleAudioMessage(msg *events.Message) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	imsg := msg.Message.GetAudioMessage()

	senderJID := msg.Info.Sender
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}
	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: b.getSenderName(msg),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]any),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	b.avatarsMu.RLock()
	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.avatarsMu.RUnlock()

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	if len(fileExt) == 0 {
		fileExt = append(fileExt, ".ogg")
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[0])

	if mydebug {
		b.Log.Debugf("Trying to download %s with size %#v and type %s", filename, imsg.GetFileLength(), imsg.GetMimetype())
	}

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download video failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, "audio message", "", &data, b.General)

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}

// HandleDocumentMessage downloads documents
func (b *Bwhatsapp) handleDocumentMessage(msg *events.Message) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	imsg := msg.Message.GetDocumentMessage()

	senderJID := msg.Info.Sender
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: b.getSenderName(msg),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]any),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	b.avatarsMu.RLock()
	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.avatarsMu.RUnlock()

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	filename := fmt.Sprintf("%v", imsg.GetFileName())

	if mydebug {
		b.Log.Debugf("Trying to download %s with extension %s and type %s", filename, fileExt, imsg.GetMimetype())
	}

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download document message failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleDelete(messageInfo *waE2E.ProtocolMessage) {
	defer b.waHandlePanic()

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	sender, _ := types.ParseJID(*messageInfo.Key.Participant)

	rmsg := config.Message{
		Account:  b.Account,
		Protocol: b.Protocol,
		ID:       getMessageIdFormat(sender, *messageInfo.Key.ID),
		Event:    config.EventMsgDelete,
		Text:     config.EventMsgDelete,
		Channel:  *messageInfo.Key.RemoteJID,
	}

	if mydebug {
		b.Log.Debugf("<= Sending message from %s to gateway", b.Account)
		b.Log.Debugf("<= Message is %#v", rmsg)
	}

	b.Remote <- rmsg
}
