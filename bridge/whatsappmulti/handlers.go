package bwhatsapp

import (
	"context"
	"fmt"
	"mime"
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"

	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// nolint:gocritic
func (b *Bwhatsapp) eventHandler(evt interface{}) {
	switch e := evt.(type) {
	case *events.Message:
		b.handleMessage(e)
	case *events.GroupInfo:
		b.handleGroupInfo(e)
	case *events.NewsletterJoin:
		b.handleNewsletterJoin(e)
	case *events.NewsletterLeave:
		b.handleNewsletterLeave(e)
	case *events.JoinedGroup:
		b.handleJoinedGroup(e)
	}
}

func (b *Bwhatsapp) handleJoinedGroup(event *events.JoinedGroup) {
	b.Log.Infof("Joined group: %s (%s)", event.JID, event.GroupName.Name)
}

func (b *Bwhatsapp) handleGroupInfo(event *events.GroupInfo) {
	b.Log.Debugf("Receiving event %#v", event)

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
		senderName := b.getSenderNameFromJID(joinedJid)

		rmsg := config.Message{
			UserID:   joinedJid.String(),
			Username: senderName,
			Channel:  event.JID.String(),
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventJoinLeave,
			Text:     "joined chat",
		}

		b.Remote <- rmsg
	}
}

func (b *Bwhatsapp) handleUserLeave(event *events.GroupInfo) {
	for _, leftJid := range event.Leave {
		senderName := b.getSenderNameFromJID(leftJid)

		rmsg := config.Message{
			UserID:   leftJid.String(),
			Username: senderName,
			Channel:  event.JID.String(),
			Account:  b.Account,
			Protocol: b.Protocol,
			Event:    config.EventJoinLeave,
			Text:     "left chat",
		}

		b.Remote <- rmsg
	}
}

func (b *Bwhatsapp) handleTopicChange(event *events.GroupInfo) {
	msg := event.Topic
	senderJid := msg.TopicSetBy
	senderName := b.getSenderNameFromJID(senderJid)

	text := msg.Topic
	if text == "" {
		text = "removed topic"
	}

	rmsg := config.Message{
		UserID:   senderJid.String(),
		Username: senderName,
		Channel:  event.JID.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Event:    config.EventTopicChange,
		Text:     "Topic changed: " + text,
	}

	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleMessage(message *events.Message) {
	msg := message.Message
	switch {
	case msg == nil, message.Info.IsFromMe, message.Info.Timestamp.Before(b.startedAt):
		return
	}

	b.Log.Debugf("Receiving message %#v", msg)

	// Route newsletter (channel) messages to a dedicated handler
	if message.NewsletterMeta != nil {
		b.handleNewsletterMessage(message)
		return
	}

	switch {
	case msg.Conversation != nil || msg.ExtendedTextMessage != nil:
		b.handleTextMessage(message.Info, msg)
	case msg.VideoMessage != nil:
		b.handleVideoMessage(message)
	case msg.AudioMessage != nil:
		b.handleAudioMessage(message)
	case msg.DocumentMessage != nil:
		b.handleDocumentMessage(message)
	case msg.ImageMessage != nil:
		b.handleImageMessage(message)
	case msg.ProtocolMessage != nil && *msg.ProtocolMessage.Type == proto.ProtocolMessage_REVOKE:
		b.handleDelete(msg.ProtocolMessage)
	}
}

// nolint:funlen
func (b *Bwhatsapp) handleTextMessage(messageInfo types.MessageInfo, msg *proto.Message) {
	senderJID := messageInfo.Sender
	channel := messageInfo.Chat

	senderName := b.getSenderName(messageInfo)

	if msg.GetExtendedTextMessage() == nil && msg.GetConversation() == "" {
		b.Log.Debugf("message without text content? %#v", msg)
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
		Username: senderName,
		Text:     text,
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, messageInfo.ID),
		ParentID: parentID,
	}

	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

// HandleImageMessage sent from WhatsApp, relay it to the brige
func (b *Bwhatsapp) handleImageMessage(msg *events.Message) {
	imsg := msg.Message.GetImageMessage()

	senderJID := msg.Info.Sender
	senderName := b.getSenderName(msg.Info)
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: senderName,
		Text:     imsg.GetCaption(),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

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

	b.Log.Debugf("Trying to download %s with type %s", filename, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download image failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

// HandleVideoMessage downloads video messages
func (b *Bwhatsapp) handleVideoMessage(msg *events.Message) {
	imsg := msg.Message.GetVideoMessage()

	senderJID := msg.Info.Sender
	senderName := b.getSenderName(msg.Info)
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: senderName,
		Text:     imsg.GetCaption(),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

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
		if ".mp4" == n {
			fileExtIndex = i
			break
		}
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[fileExtIndex])

	b.Log.Debugf("Trying to download %s with size %#v and type %s", filename, imsg.GetFileLength(), imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download video failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

// HandleAudioMessage downloads audio messages
func (b *Bwhatsapp) handleAudioMessage(msg *events.Message) {
	imsg := msg.Message.GetAudioMessage()

	senderJID := msg.Info.Sender
	senderName := b.getSenderName(msg.Info)
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}
	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: senderName,
		Text:     "audio message",
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	if len(fileExt) == 0 {
		fileExt = append(fileExt, ".ogg")
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[0])

	b.Log.Debugf("Trying to download %s with size %#v and type %s", filename, imsg.GetFileLength(), imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download video failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, "audio message", "", &data, b.General)

	b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

// HandleDocumentMessage downloads documents
func (b *Bwhatsapp) handleDocumentMessage(msg *events.Message) {
	imsg := msg.Message.GetDocumentMessage()

	senderJID := msg.Info.Sender
	senderName := b.getSenderName(msg.Info)
	ci := imsg.GetContextInfo()

	if senderJID == (types.JID{}) && ci.Participant != nil {
		senderJID = types.NewJID(ci.GetParticipant(), types.DefaultUserServer)
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: senderName,
		Text:     imsg.GetCaption(),
		Channel:  msg.Info.Chat.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
		ParentID: getParentIdFromCtx(ci),
	}

	if avatarURL, exists := b.userAvatars[senderJID.String()]; exists {
		rmsg.Avatar = avatarURL
	}

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)

		return
	}

	filename := fmt.Sprintf("%v", imsg.GetFileName())

	b.Log.Debugf("Trying to download %s with extension %s and type %s", filename, fileExt, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download document message failed: %s", err)

		return
	}

	// Move file to bridge storage
	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending message from %s on %s to gateway", senderJID, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleDelete(messageInfo *proto.ProtocolMessage) {
	var sender types.JID
	if messageInfo.Key.Participant != nil {
		sender, _ = types.ParseJID(*messageInfo.Key.Participant)
	} else if messageInfo.Key.RemoteJID != nil {
		sender, _ = types.ParseJID(*messageInfo.Key.RemoteJID)
	}

	rmsg := config.Message{
		Account:  b.Account,
		Protocol: b.Protocol,
		ID:       getMessageIdFormat(sender, *messageInfo.Key.ID),
		Event:    config.EventMsgDelete,
		Text:     config.EventMsgDelete,
		Channel:  *messageInfo.Key.RemoteJID,
	}

	b.Log.Debugf("<= Sending message from %s to gateway", b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)
	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterMessage(message *events.Message) {
	msg := message.Message
	channel := message.Info.Chat
	newsletterName := b.getNewsletterName(channel)
	senderJID := channel

	b.Log.Debugf("Receiving newsletter message from %s: %#v", channel, msg)

	switch {
	case msg.Conversation != nil || msg.ExtendedTextMessage != nil:
		b.handleNewsletterTextMessage(senderJID, channel, newsletterName, message)
	case msg.ImageMessage != nil:
		b.handleNewsletterImageMessage(senderJID, channel, newsletterName, message)
	case msg.VideoMessage != nil:
		b.handleNewsletterVideoMessage(senderJID, channel, newsletterName, message)
	case msg.AudioMessage != nil:
		b.handleNewsletterAudioMessage(senderJID, channel, newsletterName, message)
	case msg.DocumentMessage != nil:
		b.handleNewsletterDocumentMessage(senderJID, channel, newsletterName, message)
	case msg.ProtocolMessage != nil && *msg.ProtocolMessage.Type == proto.ProtocolMessage_REVOKE:
		b.handleDelete(msg.ProtocolMessage)
	default:
		b.Log.Debugf("Unhandled newsletter message type: %#v", msg)
	}
}

func (b *Bwhatsapp) handleNewsletterTextMessage(senderJID, channel types.JID, newsletterName string, message *events.Message) {
	var text string
	msg := message.Message

	if msg.GetExtendedTextMessage() == nil {
		text = msg.GetConversation()
	} else {
		text = msg.GetExtendedTextMessage().GetText()
	}

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: newsletterName,
		Text:     text,
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, message.Info.ID),
	}

	b.Log.Debugf("<= Sending newsletter message from %s on %s to gateway", newsletterName, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterImageMessage(senderJID, channel types.JID, newsletterName string, msg *events.Message) {
	imsg := msg.Message.GetImageMessage()

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: newsletterName,
		Text:     imsg.GetCaption(),
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
	}

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)
		return
	}

	if fileExt[0] == ".jfif" {
		fileExt[0] = ".jpg"
	}
	if fileExt[0] == ".jpe" {
		fileExt[0] = ".jpg"
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[0])

	b.Log.Debugf("Trying to download newsletter image %s with type %s", filename, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download newsletter image failed: %s", err)
		return
	}

	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending newsletter message from %s on %s to gateway", newsletterName, b.Account)
	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterVideoMessage(senderJID, channel types.JID, newsletterName string, msg *events.Message) {
	imsg := msg.Message.GetVideoMessage()

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: newsletterName,
		Text:     imsg.GetCaption(),
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
	}

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)
		return
	}

	if len(fileExt) == 0 {
		fileExt = append(fileExt, ".mp4")
	}

	fileExtIndex := 0
	for i, n := range fileExt {
		if ".mp4" == n {
			fileExtIndex = i
			break
		}
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[fileExtIndex])

	b.Log.Debugf("Trying to download newsletter video %s with type %s", filename, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download newsletter video failed: %s", err)
		return
	}

	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending newsletter message from %s on %s to gateway", newsletterName, b.Account)
	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterAudioMessage(senderJID, channel types.JID, newsletterName string, msg *events.Message) {
	imsg := msg.Message.GetAudioMessage()

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: newsletterName,
		Text:     "audio message",
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
	}

	fileExt, err := mime.ExtensionsByType(imsg.GetMimetype())
	if err != nil {
		b.Log.Errorf("Mimetype detection error: %s", err)
		return
	}

	if len(fileExt) == 0 {
		fileExt = append(fileExt, ".ogg")
	}

	filename := fmt.Sprintf("%v%v", msg.Info.ID, fileExt[0])

	b.Log.Debugf("Trying to download newsletter audio %s with type %s", filename, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download newsletter audio failed: %s", err)
		return
	}

	helper.HandleDownloadData(b.Log, &rmsg, filename, "audio message", "", &data, b.General)

	b.Log.Debugf("<= Sending newsletter message from %s on %s to gateway", newsletterName, b.Account)
	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterDocumentMessage(senderJID, channel types.JID, newsletterName string, msg *events.Message) {
	imsg := msg.Message.GetDocumentMessage()

	rmsg := config.Message{
		UserID:   senderJID.String(),
		Username: newsletterName,
		Text:     imsg.GetCaption(),
		Channel:  channel.String(),
		Account:  b.Account,
		Protocol: b.Protocol,
		Extra:    make(map[string][]interface{}),
		ID:       getMessageIdFormat(senderJID, msg.Info.ID),
	}

	filename := imsg.GetFileName()

	b.Log.Debugf("Trying to download newsletter document %s with type %s", filename, imsg.GetMimetype())

	data, err := b.wc.Download(context.Background(), imsg)
	if err != nil {
		b.Log.Errorf("Download newsletter document failed: %s", err)
		return
	}

	helper.HandleDownloadData(b.Log, &rmsg, filename, imsg.GetCaption(), "", &data, b.General)

	b.Log.Debugf("<= Sending newsletter message from %s on %s to gateway", newsletterName, b.Account)
	b.Remote <- rmsg
}

func (b *Bwhatsapp) handleNewsletterJoin(event *events.NewsletterJoin) {
	b.Log.Debugf("Joined newsletter: %#v", event)

	name := event.ThreadMeta.Name.Text
	if name == "" {
		name = event.ID.String()
	}

	b.Lock()
	for i, nl := range b.subscribedNewsletters {
		if nl.ID == event.ID {
			b.subscribedNewsletters[i] = &event.NewsletterMetadata
			b.newsletterNames[event.ID.String()] = name
			b.Unlock()
			b.Log.Infof("Subscribed to newsletter: %s (%s)", name, event.ID.String())
			return
		}
	}
	b.subscribedNewsletters = append(b.subscribedNewsletters, &event.NewsletterMetadata)
	b.newsletterNames[event.ID.String()] = name
	b.Unlock()

	b.Log.Infof("Subscribed to newsletter: %s (%s)", name, event.ID.String())
}

func (b *Bwhatsapp) handleNewsletterLeave(event *events.NewsletterLeave) {
	b.Log.Debugf("Left newsletter: %#v", event)

	b.Lock()
	for i, nl := range b.subscribedNewsletters {
		if nl.ID == event.ID {
			b.subscribedNewsletters = append(b.subscribedNewsletters[:i], b.subscribedNewsletters[i+1:]...)
			delete(b.newsletterNames, event.ID.String())
			b.Unlock()
			b.Log.Infof("Unsubscribed from newsletter: %s", event.ID.String())
			return
		}
	}
	b.Unlock()
}
