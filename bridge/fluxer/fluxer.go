package bfluxer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/fluxergo/fluxergo"
	"github.com/fluxergo/fluxergo/bot"
	"github.com/fluxergo/fluxergo/cache"
	"github.com/fluxergo/fluxergo/events"
	"github.com/fluxergo/fluxergo/fluxer"
	"github.com/fluxergo/fluxergo/gateway"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
)

const (
	MessageLength = 1950
)

type Bfluxer struct {
	*bridge.Config

	c *bot.Client
	ready chan bool

	nick    string
	guild fluxer.Guild
	userID  snowflake.ID
	guildID snowflake.ID
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bfluxer{Config: cfg}

	b.ready = make(chan bool)

	return b
}

func (b *Bfluxer) onGuildAvailable (e *events.GuildAvailable){
	if b.guildID != 0 {
		return
	}

	serverName := strings.Replace(b.GetString("Server"), "ID:", "", -1)

	// If the server name doesn't match the ID or Name, ignore it.
	if e.GuildID.String() != serverName {
		return
	}

	b.guildID = e.GuildID

	b.ready <- true
}

func (b *Bfluxer) Connect() error {
	var err error
	token := b.GetString("Token")
	b.Log.Info("Connecting")

	b.c, err = fluxergo.New(token,
				bot.WithGatewayConfigOpts(
					gateway.WithAutoReconnect(true),
				),
				bot.WithCacheConfigOpts(
					cache.WithCaches(cache.FlagChannels |
							cache.FlagMembers |
							cache.FlagGuilds),
				),
				bot.WithEventListenerFunc(b.onGuildAvailable))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 10)
	err = b.c.OpenGateway(ctx)
	if err != nil {
		cancel()
		return err
	}
	b.Log.Info("Connection succeeded")

	userinfo, serr := b.c.Caches.SelfUser()
	if serr != true {
		return errors.New("Unable to get self user")
	}

	b.nick = userinfo.Username
	b.userID = userinfo.ID

	// Wait for the guild to come online.
	select {
	case <-time.After(10 * time.Second):
		b.Disconnect()
		return errors.New("Timed out waiting for guild. Maybe it doesn't exist?")
	case <-b.ready:
	}

	b.c.AddEventListeners(bot.NewListenerFunc(b.messageCreate))
	b.c.AddEventListeners(bot.NewListenerFunc(b.messageUpdate))
	b.c.AddEventListeners(bot.NewListenerFunc(b.messageDelete))
	b.c.AddEventListeners(bot.NewListenerFunc(b.messageTyping))

	return nil
}

func (b *Bfluxer) Disconnect() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel()

	b.c.Close(ctx)
	return nil
}

func (b *Bfluxer) JoinChannel(channel config.ChannelInfo) error {
	return nil
}

func (b *Bfluxer) Send(msg config.Message) (string, error) {
	b.Log.Debugf("=> Receiving %#v", msg)

	channelID := snowflake.MustParse(msg.Channel)

	if msg.Event == config.EventUserTyping {
		if b.GetBool("ShowUserTyping") {
			err := b.c.Rest.SendTyping(channelID)
			return "", err
		}
		return "", nil
	}

	// Make a action /me of the message
	if msg.Event == config.EventUserAction {
		msg.Text = "_" + msg.Text + "_"
	}

	// Handle prefix hint for unthreaded messages.
	if msg.ParentNotFound() {
		msg.ParentID = ""
	}

	return b.handleEventBotUser(&msg, channelID)
}

// handleEventDirect handles events via the bot user
func (b *Bfluxer) handleEventBotUser(msg *config.Message, channelID snowflake.ID) (string, error) {
	b.Log.Debugf("Broadcasting using token (API)")

	// Delete message
	if msg.Event == config.EventMsgDelete {
		if msg.ID == "" {
			return "", nil
		}
		err := b.c.Rest.DeleteMessage(channelID, snowflake.MustParse(msg.ID))
		return "", err
	}

	// Edit message
	if msg.ID != "" {
		// Exploit that a discord message ID is actually just a large number, and we encode a list of IDs by separating them with ";".
		msgIds := strings.Split(msg.ID, ";")
		msgParts := helper.ClipOrSplitMessage(msg.Text, MessageLength, b.GetString("MessageClipped"), len(msgIds))
		for len(msgParts) < len(msgIds) {
			msgParts = append(msgParts, "((obsoleted by edit))")
		}
		for i := range msgParts {
			// In case of split-messages where some parts remain the same (i.e. only a typo-fix in a huge message), this causes some noop-updates.
			// TODO: Optimize away noop-updates of un-edited messages
			// TODO: Use RemoteNickFormat instead of this broken concatenation
			cmsg := msg.Username + msgParts[i]
			_, err := b.c.Rest.UpdateMessage(channelID, snowflake.MustParse(msgIds[i]),fluxer.MessageUpdate{
				Content: &cmsg,
				AllowedMentions: b.getAllowedMentions(),
			})
			if err != nil {
				return "", err
			}
		}
		return msg.ID, nil
	}

	if msg.Extra != nil {
		for _, rmsg := range helper.HandleExtra(msg, b.General) {
			// TODO: Use ClipOrSplitMessage
			rmsg.Text = helper.ClipMessage(rmsg.Text, MessageLength, b.GetString("MessageClipped"))
			if _, err := b.c.Rest.CreateMessage(channelID, fluxer.MessageCreate {
				Content: rmsg.Username+rmsg.Text,
				AllowedMentions: b.getAllowedMentions(),
			}); err != nil {
				b.Log.Errorf("Could not send message %#v: %s", rmsg, err)
			}
		}
		// File Upload
		if len(msg.Extra["file"]) > 0 {
			return b.handleUploadFile(msg, channelID)
		}
	}

	msgParts := helper.ClipOrSplitMessage(msg.Text, MessageLength, b.GetString("MessageClipped"), b.GetInt("MessageSplitMaxCount"))
	msgIds := []string{}

	for _, msgPart := range msgParts {
		m := fluxer.MessageCreate{
			Content: msg.Username + msgPart,
			AllowedMentions: b.getAllowedMentions(),
		}

		if msg.ParentValid() {
			mid := snowflake.MustParse(msg.ParentID)
			m.MessageReference = &fluxer.MessageReference{
				MessageID: &mid,
				ChannelID: &channelID,
				GuildID:   &b.guildID,
			}
		}

		// Post normal message
		res, err := b.c.Rest.CreateMessage(channelID, m)
		if err != nil {
			return "", err
		}
		msgIds = append(msgIds, res.ID.String())
	}

	// Exploit that a discord message ID is actually just a large number, so we encode a list of IDs by separating them with ";".
	return strings.Join(msgIds, ";"), nil
}

// handleUploadFile handles native upload of files
func (b *Bfluxer) handleUploadFile(msg *config.Message, channelID snowflake.ID) (string, error) {
	for _, f := range msg.Extra["file"] {
		fi := f.(config.FileInfo)
		file := fluxer.File{
			Name:        fi.Name,
			Reader:      bytes.NewReader(*fi.Data),
		}
		m := fluxer.MessageCreate{
			Content:         msg.Username + fi.Comment,
			Files:           []*fluxer.File{&file},
			AllowedMentions: b.getAllowedMentions(),
		}
		_, err := b.c.Rest.CreateMessage(channelID, m)
		if err != nil {
			return "", fmt.Errorf("file upload failed: %s", err)
		}
	}

	return "", nil
}
