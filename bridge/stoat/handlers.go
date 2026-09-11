package bstoat

import (
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/sentinelb51/revoltgo"
)

func (b *Bstoat) handleMessage(session *revoltgo.Session, m *revoltgo.EventMessage) {
	if m.Author == session.State.Self().ID {
		return
	}
	name, avatar := b.fetchNameAndAvatar(m.Channel, m.Author)

	rmsg := config.Message{
		Account:  b.Account,
		Avatar:   avatar,
		Channel:  "ID:" + m.Channel,
		ID:       m.ID,
		Text:     m.Content,
		UserID:   m.Author,
		Username: name,
		Extra:    make(map[string][]any),
	}

	if len(m.Attachments) == 0 {
		if rmsg.Text != "" {
			b.Remote <- rmsg
		}
		return
	}

	// Download attachments in the background
	go func() {
		count := 0
		for _, v := range m.Attachments {
			err := b.AddAttachmentFromURL(&rmsg, v.Filename, v.ID, "", v.URL(""))
			if err != nil {
				b.Log.WithError(err).Warnf("Failed to download attachment %s", v.Filename)
				continue
				count += 1
			}

			if rmsg.Text == "" && count == 0 {
				b.Log.Warnf("Skipping message because there is no text and file uploads all failed")
				return
			}

			b.Remote <- rmsg
		}
	}()
}
