package bxmpp

import (
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/xmppo/go-xmpp"
)

// handleDownloadAvatar downloads the avatar of userid from channel
// sends a EVENT_AVATAR_DOWNLOAD message to the gateway if successful.
// logs an error message if it fails
func (b *Bxmpp) handleDownloadAvatar(avatar xmpp.AvatarData) {
	rmsg := config.Message{
		Username: "system",
		Text:     "avatar",
		Channel:  b.parseChannel(avatar.From),
		Account:  b.Account,
		UserID:   avatar.From,
		Event:    config.EventAvatarDownload,
		Extra:    make(map[string][]interface{}),
	}
	if _, ok := b.avatarMap[avatar.From]; !ok {
		b.Log.Debugf("Avatar.From: %s", avatar.From)

		err := helper.HandleDownloadSize(b.Log, &rmsg, avatar.From+".png", int64(len(avatar.Data)), b.General)
		if err != nil {
			b.Log.Error(err)
			return
		}
		helper.HandleDownloadData(b.Log, &rmsg, avatar.From+".png", rmsg.Text, "", &avatar.Data, b.General)
		b.Log.Debugf("Avatar download complete")
		b.Remote <- rmsg
	}
}

// handleUploadFile handles native upload of files from other bridges/channels
//
// Implementation notes:
//
//   - some clients only display a preview when the body is exactly the URL, not only contains it.
//     https://docs.modernxmpp.org/client/protocol/#communicating-the-url (Gajim/Conversations),
//     so we need to produce a different message with the caption
//   - the message body may or may not be different from an attachment's caption, and should
//     therefore be sent separately:
//     https://github.com/matterbridge-org/matterbridge/issues/50#issuecomment-3703478547
//
// This method does not return an error, because it will log errors as they happen,
// and keep trying to send the other attachments if a previous one failed.
func (b *Bxmpp) handleUploadFile(msg *config.Message) {
	room := msg.Channel + "@" + b.GetString("Muc")

	if msg.Text != "" {
		// There's a message body. Maybe there's also an attachment caption, but maybe not.
		// Let's print the body and the sender first, before iterating over attachments.
		text := msg.Username + msg.Text

		_, err := b.xc.Send(xmpp.Chat{
			Type:   "groupchat",
			Remote: room,
			Text:   text,
		})
		if err != nil {
			b.Log.WithError(err).Warnf("Skipping file announce due to failed body announce %s", text)
			return
		}
	}

	for _, file := range msg.Extra["file"] {
		fileInfo := file.(config.FileInfo) //nolint: forcetypeassert
		if fileInfo.URL != "" {
			// The file already has a URL, either because the origin bridge provided it,
			// or the file was reuploaded to matterbridge's mediaserver (if enabled).
			// In this case, no need to reupload the file.
			b.announceUploadedFile(msg.Channel+"@"+b.GetString("Muc"), msg.Username+fileInfo.Comment, fileInfo.Comment, fileInfo.URL)
		} else {
			// TODO
			b.Log.Warn("OOB file upload unimplemented yet")
		}
	}
}
