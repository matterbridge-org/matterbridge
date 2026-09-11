package bstoat

import (
	"bytes"
	"errors"
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/sentinelb51/revoltgo"
)

type Bstoat struct {
	*bridge.Config

	session *revoltgo.Session
}

func New(cfg *bridge.Config) bridge.Bridger {
	return &Bstoat{Config: cfg}
}

func (b *Bstoat) Connect() error {
	b.session = revoltgo.New(b.GetString("Token"))
	revoltgo.AddHandler(b.session, b.handleMessage)
	return b.session.Open()
}

func (b *Bstoat) Disconnect() error {
	return b.session.Close()
}

func (b *Bstoat) Send(msg config.Message) (string, error) {
	if !strings.HasPrefix(msg.Channel, "ID:") {
		return "", errors.New("bad channel id")
	}
	channel := msg.Channel[3:]

	// Upload attachments
	var attachments []string
	if msg.Extra != nil {
		for _, f := range msg.Extra["file"] {
			fi := f.(config.FileInfo)
			res, err := b.session.AttachmentUpload(&revoltgo.FileParams{
				Name:   fi.Name,
				Reader: bytes.NewReader(*fi.Data),
			})
			if err != nil {
				b.Log.WithError(err).Warnf("Failed to upload attachment %s", fi.Name)
				continue
			}
			attachments = append(attachments, res.ID)
			// FIXME: handle fi.Comment
		}
	}

	// Post the message
	message, err := b.session.ChannelMessageSend(channel, revoltgo.MessageSend{
		Content:     msg.Text,
		Attachments: attachments,
		Masquerade: &revoltgo.MessageMasquerade{
			Name:   msg.Username,
			Avatar: msg.Avatar,
		},
	})
	if err != nil {
		return "", err
	}
	return message.ID, nil
}

func (b *Bstoat) JoinChannel(channel config.ChannelInfo) error {
	return nil
}
