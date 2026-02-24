package bfluxer

import (
	"context"
	"strconv"
	"strings"

	"github.com/fluxer-flo/flo"
	"github.com/matterbridge-org/matterbridge/bridge/config"
)

func (b *Bfluxer) handleQuote(m *flo.Message, msg string) string {
	if b.GetBool("QuoteDisable") {
		return msg
	}

	if m.MessageReference == nil {
		return msg
	}

	refMsgRef := m.MessageReference

	refMsg, err := b.rest.GetMessage(context.TODO(), refMsgRef.ChannelID, refMsgRef.MessageID)
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

func (b *Bfluxer) messageCreate(e flo.MessageCreateEvent) { //nolint:unparam
	if *e.GuildID != b.guildID {
		b.Log.Debugf("Ignoring messageCreate because it originates from a different guild")
		return
	}

	// not relay our own messages
	if e.Author.ID == b.userID {
		return
	}

	rmsg := config.Message{
		Account: b.Account,
		Avatar:  b.getUGCUrl() + "/avatars/" + strconv.FormatUint(uint64(e.Author.ID), 10) + "/" + *e.Author.Avatar + ".jpg",
		UserID:  strconv.FormatUint(uint64(e.Author.ID), 10),
		ID:      strconv.FormatUint(uint64(e.ID), 10),
	}

	// add the url of the attachments to content
	atchmnt := e.Attachments
	if len(atchmnt) > 0 {
		for _, attach := range atchmnt {
			e.Content = e.Content + "\n" + *attach.URL
		}
	}

	b.Log.Debugf("== Receiving event %#v", e)

	rmsg.Text = e.Content

	// set channel name
	rmsg.Channel = strconv.FormatUint(uint64(e.ChannelID), 10)

	rmsg.Username = e.Author.Username
	if b.GetBool("UseDiscriminator") {
		rmsg.Username += "#" + e.Author.Discriminator
	}

	// if we have embedded content add it to text
	if b.GetBool("ShowEmbeds") && e.Embeds != nil {
		for _, embed := range e.Embeds {
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
	if ref := e.MessageReference; ref != nil && ref.ChannelID == e.ChannelID {
		rmsg.ParentID = strconv.FormatUint(uint64(ref.MessageID), 10)
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", e.Author.Username, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

func (b *Bfluxer) messageUpdate(e flo.MessageUpdateEvent) { //nolint:unparam
	if *e.GuildID != b.guildID {
		b.Log.Debugf("Ignoring messageUpdate because it originates from a different guild")
		return
	}

	if b.GetBool("EditDisable") {
		return
	}

	// not relay our own messages
	if e.Author.ID == b.userID {
		return
	}

	// only when message is actually edited
	if e.EditedAt != nil {
		rmsg := config.Message{
			Account: b.Account,
			UserID:  strconv.FormatUint(uint64(e.Author.ID), 10),
			ID:      strconv.FormatUint(uint64(e.ID), 10),
			Channel: strconv.FormatUint(uint64(e.ChannelID), 10),
		}

		rmsg.Username = e.Author.Username
		if b.GetBool("UseDiscriminator") {
			rmsg.Username += "#" + e.Author.Discriminator
		}

		b.Log.Debugf("== Receiving event %#v", e)

		b.Log.Debugf("Sending edit message")
		e.Content += b.GetString("EditSuffix")

		rmsg.Text = e.Content
		b.Remote <- rmsg
	}
}

func (b *Bfluxer) messageDelete(e flo.MessageDeleteEvent) { //nolint:unparam
	if *e.GuildID != b.guildID {
		b.Log.Debugf("Ignoring messageDelete because it originates from a different guild")
		return
	}

	rmsg := config.Message{
		Account: b.Account,
		ID:      strconv.FormatUint(uint64(e.MessageID), 10),
		Event:   config.EventMsgDelete,
		Text:    config.EventMsgDelete,
	}
	rmsg.Channel = strconv.FormatUint(uint64(e.ChannelID), 10)

	b.Log.Debugf("<= Sending message from %s to gateway", b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- rmsg
}

func (b *Bfluxer) messageTyping(e flo.TypingStartEvent) {
	if *e.GuildID != b.guildID {
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

	rmsg.Channel = strconv.FormatUint(uint64(e.ChannelID), 10)
	b.Remote <- rmsg
}

func handleEmbed(embed flo.Embed) string {
	var t []string

	var result string

	t = append(t, *embed.Title)
	t = append(t, *embed.Description)
	t = append(t, *embed.URL)

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
