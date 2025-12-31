package bxmpp

import (
	"regexp"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/xmppo/go-xmpp"
)

var pathRegex = regexp.MustCompile("[^a-zA-Z0-9]+")

// GetAvatar constructs a URL for a given user-avatar if it is available in the cache.
func getAvatar(av map[string]string, userid string, general *config.Protocol) string {
	if hash, ok := av[userid]; ok {
		// NOTE: This does not happen in bridge/helper/helper.go but messes up XMPP
		id := pathRegex.ReplaceAllString(userid, "_")
		return general.MediaServerDownload + "/" + hash + "/" + id + ".png"
	}
	return ""
}

func (b *Bxmpp) cacheAvatar(msg *config.Message) string {
	fi := msg.Extra["file"][0].(config.FileInfo)
	/* if we have a sha we have successfully uploaded the file to the media server,
	so we can now cache the sha */
	if fi.SHA != "" {
		b.Log.Debugf("Added %s to %s in avatarMap", fi.SHA, msg.UserID)
		b.avatarMap[msg.UserID] = fi.SHA
	}
	return ""
}

// This method announces a file sharer and optional caption, then advertises the URL
// for a file attachment.
//
// The second argument contains the uploader nickname with the caption, while the third
// is the raw attachment caption.
//
// This method does not error. Errors are logged as warnings.
func (b *Bxmpp) announceUploadedFile(to string, text string, urlDesc string, urlStr string) {
	b.Log.Debugf("Announcing uploaded file to %s: text `%s` desc `%s` url `%s`", to, text, urlDesc, urlStr)

	// Send separate message with the username and optional file comment
	// because we can't have an attachment comment/description.
	_, err := b.xc.Send(xmpp.Chat{
		Type:   "groupchat",
		Remote: to,
		// This contains the uploader name, and the optional caption
		Text: text,
	})
	if err != nil {
		b.Log.WithError(err).Warnf("Skipping file announce due to failed sharer announce %s", text)
		return
	}

	_, err = b.xc.SendOOB(xmpp.Chat{
		Type:   "groupchat",
		Remote: to,
		Oob: xmpp.Oob{
			Url: urlStr,
			// This is the raw caption, if any
			Desc: urlDesc,
		},
	})
	if err != nil {
		b.Log.WithError(err).Warnf("Skipping file announce due to failed OOB announce %s", urlStr)
		return
	}
}
