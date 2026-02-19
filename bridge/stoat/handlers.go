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
	b.Remote <- config.Message{
		Account:  b.Account,
		Avatar:   avatar,
		Channel:  "ID:" + m.Channel,
		ID:       m.ID,
		Text:     m.Content,
		UserID:   m.Author,
		Username: name,
	}
}
