package bfluxer

import (
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/fluxergo/fluxergo/events"
	"github.com/fluxergo/fluxergo/fluxer"
)

func (b *Bfluxer) handleQuote(m *fluxer.Message, msg string) string {
	if b.GetBool("QuoteDisable") {
		return msg
	}
	if m.MessageReference == nil {
		return msg
	}
	refMsgRef := m.MessageReference
	refMsg, err := b.c.Rest.GetMessage(*refMsgRef.ChannelID, *refMsgRef.MessageID)
	if err != nil {
		b.Log.Errorf("Error getting quoted message %s:%s: %s", refMsgRef.ChannelID, refMsgRef.MessageID, err)
		return msg
	}

	quoteMessage := refMsg.Content
	quoteNick := refMsg.Author.Username
	if b.GetBool("UseDiscriminator") {
		quoteNick += "#" + refMsg.Author.Discriminator
	}

	format := b.GetString("quoteformat")
	if format == "" {
		format = "{MESSAGE} (re @{QUOTENICK}: {QUOTEMESSAGE})"
	}
	quoteMessagelength := len([]rune(quoteMessage))
	if b.GetInt("QuoteLengthLimit") != 0 && quoteMessagelength >= b.GetInt("QuoteLengthLimit") {
		runes := []rune(quoteMessage)
		quoteMessage = string(runes[0:b.GetInt("QuoteLengthLimit")])
		if quoteMessagelength > b.GetInt("QuoteLengthLimit") {
			quoteMessage += "..."
		}
	}
	format = strings.ReplaceAll(format, "{MESSAGE}", m.Content)
	format = strings.ReplaceAll(format, "{QUOTENICK}", quoteNick)
	format = strings.ReplaceAll(format, "{QUOTEMESSAGE}", quoteMessage)
	return format
}

func (b *Bfluxer) messageCreate(e *events.MessageCreate) { //nolint:unparam
	if e.GuildID.String() != b.guildID.String() {
		b.Log.Debugf("Ignoring messageCreate because it originates from a different guild")
		return
	}

	// not relay our own messages
	if e.Message.Author.ID == b.userID {
		return
	}

	rmsg := config.Message{
		Account: b.Account,
		Avatar: "https://fluxerusercontent.com/avatars/" +
		e.Message.Author.ID.String() + "/" + *e.Message.Author.Avatar + ".jpg",
		UserID: e.Message.Author.ID.String(),
		ID: e.Message.ID.String(),
	}

	// add the url of the attachments to content
	atchmnt := e.Message.Attachments
	if len(atchmnt) > 0 {
		for _, attach := range atchmnt {
			e.Message.Content = e.Message.Content + "\n" + attach.URL
		}
	}

	b.Log.Debugf("== Receiving event %#v", e.GenericEvent)

	rmsg.Text = e.Message.Content

	// set channel name
	rmsg.Channel = e.ChannelID.String()

	rmsg.Username = e.Message.Author.Username
	if b.GetBool("UseDiscriminator") {
		rmsg.Username += "#" + e.Message.Author.Discriminator
	}

	// if we have embedded content add it to text
	if b.GetBool("ShowEmbeds") && e.Message.Embeds != nil {
		for _, embed := range e.Message.Embeds {
			rmsg.Text += handleEmbed(embed)
		}
	}

	// no empty messages
	if rmsg.Text == "" && len(rmsg.Extra["file"]) == 0 {
		return
	}

	// do we have a /me action
	var ok bool
	rmsg.Text, ok = b.replaceAction(rmsg.Text)
	if ok {
		rmsg.Event = config.EventUserAction
	}

	// Replace emotes
	rmsg.Text = replaceEmotes(rmsg.Text)

	// Handle Reply thread
	rmsg.Text = b.handleQuote(&e.Message, rmsg.Text)

	// Add our parent id if it exists, and if it's not referring to a message in another channel
	if ref := e.Message.MessageReference; ref != nil && ref.ChannelID.String() == e.ChannelID.String() {
		rmsg.ParentID = ref.MessageID.String()
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", e.Message.Author.Username, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)
	b.Remote <- rmsg
}

func (b *Bfluxer) messageUpdate(e *events.MessageUpdate) { //nolint:unparam
	if e.GuildID.String() != b.guildID.String() {
		b.Log.Debugf("Ignoring messageUpdate because it originates from a different guild")
		return
	}
	if b.GetBool("EditDisable") {
		return
	}

	// not relay our own messages
	if e.Message.Author.ID == b.userID {
		return
	}

	// only when message is actually edited
	if e.Message.EditedAt != nil {
		rmsg := config.Message{
			Account: b.Account,
			UserID: e.Message.Author.ID.String(),
			ID: e.MessageID.String(),
			Channel: e.ChannelID.String(),
		}

		rmsg.Username = e.Message.Author.Username
		if b.GetBool("UseDiscriminator") {
			rmsg.Username += "#" + e.Message.Author.Discriminator
		}

		b.Log.Debugf("== Receiving event %#v", e.GenericEvent)

		b.Log.Debugf("Sending edit message")
		e.Message.Content += b.GetString("EditSuffix")
		rmsg.Text = e.Message.Content
		b.Remote <- rmsg
	}
}

func (b *Bfluxer) messageDelete(e *events.MessageDelete) { //nolint:unparam
	if e.GuildID.String() != b.guildID.String() {
		b.Log.Debugf("Ignoring messageDelete because it originates from a different guild")
		return
	}
	rmsg := config.Message{
		Account: b.Account,
		ID: e.MessageID.String(),
		Event: config.EventMsgDelete,
		Text: config.EventMsgDelete,
	}
	rmsg.Channel = e.ChannelID.String()

	b.Log.Debugf("<= Sending message from %s to gateway", b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)
	b.Remote <- rmsg
}

func (b *Bfluxer) messageTyping(e *events.UserTypingStart) {
	if e.GuildID.String() != b.guildID.String() {
		b.Log.Debugf("Ignoring messageTyping because it originates from a different guild")
		return
	}
	if !b.GetBool("ShowUserTyping") {
		return
	}

	// Ignore our own typing messages
	if e.UserID == b.userID {
		return
	}

	rmsg := config.Message{Account: b.Account, Event: config.EventUserTyping}
	rmsg.Channel = e.ChannelID.String()
	b.Remote <- rmsg
}

func handleEmbed(embed fluxer.Embed) string {
	var t []string
	var result string

	t = append(t, embed.Title)
	t = append(t, embed.Description)
	t = append(t, embed.URL)

	i := 0
	for _, e := range t {
		if e == "" {
			continue
		}

		i++
		if i == 1 {
			result += " embed: " + e
			continue
		}

		result += " - " + e
	}

	if result != "" {
		result += "\n"
	}

	return result
}
