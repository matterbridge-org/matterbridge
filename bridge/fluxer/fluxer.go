package bfluxer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fluxer-flo/flo"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
)

const (
	MessageLength = 1950
)

type Bfluxer struct {
	*bridge.Config

	cache flo.Cache
	rest  flo.REST
	gway  flo.Gateway
	ready chan bool

	nick    string
	userID  flo.ID
	guildID flo.ID
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bfluxer{Config: cfg}

	b.ready = make(chan bool)

	b.cache = flo.NewCacheDefault()

	return b
}

func (b *Bfluxer) Connect() error {
	token := b.GetString("Token")
	if token == "" {
		return errors.New("No token specified!")
	}

	b.Log.Info("Connecting")

	b.rest = flo.REST{
		Auth:                   token,
		Cache:                  &b.cache,
		DefaultAllowedMentions: &flo.AllowedMentionsNone,
	}

	baseUrl, err := url.Parse(b.GetString("BaseURL"))
	if err == nil && baseUrl.String() != "" {
		b.Log.Debug("Using base url: " + baseUrl.String())
		b.rest.BaseURL = baseUrl
	}

	b.gway = flo.Gateway{
		Auth:  token,
		Cache: &b.cache,
	}

	gatewayUrl, err := url.Parse(b.GetString("GatewayURL"))
	if err == nil && gatewayUrl.String() != "" {
		b.Log.Debug("Using gateway: " + gatewayUrl.String())
		b.gway.ConnURL = gatewayUrl
	}

	err = b.gway.Start()
	if err != nil {
		return err
	}

	b.gway.ShardReady.OnceSync(func(r flo.ShardReadyEvent) {
		b.Log.Info("Logged in as " + r.User.Tag())

		b.userID = r.User.ID
		b.nick = r.User.Username
	})

	gadd := b.gway.GuildCreate.On(b.onGuildAvailable)

	// Wait for the guild to come online.
	select {
	case <-time.After(10 * time.Second):
		b.Disconnect()
		return errors.New("Timed out waiting for guild. Maybe it doesn't exist?")
	case <-b.ready:
		gadd()
	}

	b.gway.MessageCreate.On(b.messageCreate)
	b.gway.MessageUpdate.On(b.messageUpdate)
	b.gway.MessageDelete.On(b.messageDelete)
	b.gway.TypingStart.On(b.messageTyping)

	return nil
}

func (b *Bfluxer) Disconnect() error {
	return b.gway.Stop()
}

func (b *Bfluxer) JoinChannel(channel config.ChannelInfo) error {
	return nil
}

func (b *Bfluxer) Send(msg config.Message) (string, error) {
	b.Log.Debugf("=> Receiving %#v", msg)

	channelID, err := flo.ParseID(msg.Channel)
	if err != nil {
		return "", err
	}

	if msg.Event == config.EventUserTyping {
		if b.GetBool("ShowUserTyping") {
			err := b.rest.StartTyping(context.TODO(), channelID)
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

func (b *Bfluxer) onGuildAvailable(e flo.GuildAddEvent) {
	if b.guildID != 0 {
		return
	}

	serverName := strings.ReplaceAll(b.GetString("Server"), "ID:", "")

	// If itoa fails then it's likely a server string..
	gid, err := flo.ParseID(serverName)
	if err != nil && serverName != e.Name {
		return
	} else if gid != e.ID {
		return
	}

	b.guildID = e.ID

	b.ready <- true
}

// handleEventDirect handles events via the bot user
func (b *Bfluxer) handleEventBotUser(msg *config.Message, channelID flo.ID) (string, error) {
	b.Log.Debugf("Broadcasting using token (API)")

	// Delete message
	if msg.Event == config.EventMsgDelete {
		if msg.ID == "" {
			return "", nil
		}

		msgID, err := flo.ParseID(msg.ID)
		if err != nil {
			return "", err
		}

		err = b.rest.DeleteMessage(context.TODO(), channelID, msgID)

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

			msgID, err := flo.ParseID(msgIds[i])
			if err != nil {
				return "", err
			}

			_, err = b.rest.EditMessage(context.TODO(), channelID, msgID, flo.EditMessageOpts{
				Content:         &cmsg,
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

			_, err := b.rest.CreateMessage(context.TODO(), channelID, flo.CreateMessageOpts{
				Content:         rmsg.Username + rmsg.Text,
				AllowedMentions: b.getAllowedMentions(),
			})
			if err != nil {
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
		m := flo.CreateMessageOpts{
			Content:         msg.Username + msgPart,
			AllowedMentions: b.getAllowedMentions(),
		}

		if msg.ParentValid() {
			mid, err := flo.ParseID(msg.ParentID)
			if err != nil {
				return "", err
			}

			m.MessageReference = flo.MessageReferenceOpts{
				MessageID: mid,
				ChannelID: channelID,
				GuildID:   b.guildID,
			}
		}

		// Post normal message
		res, err := b.rest.CreateMessage(context.TODO(), channelID, m)
		if err != nil {
			return "", err
		}

		msgIds = append(msgIds, strconv.FormatUint(uint64(res.ID), 10))
	}

	// Exploit that a discord message ID is actually just a large number, so we encode a list of IDs by separating them with ";".
	return strings.Join(msgIds, ";"), nil
}

// handleUploadFile handles native upload of files
func (b *Bfluxer) handleUploadFile(msg *config.Message, channelID flo.ID) (string, error) {
	for _, f := range msg.Extra["file"] {
		fi := f.(config.FileInfo)
		file := flo.CreateAttachmentOpts{
			Filename: fi.Name,
			Content:  io.NopCloser(bytes.NewReader(*fi.Data)),
		}
		m := flo.CreateMessageOpts{
			Content:         msg.Username + fi.Comment,
			Attachments:     []flo.CreateAttachmentOpts{file},
			AllowedMentions: b.getAllowedMentions(),
		}

		_, err := b.rest.CreateMessage(context.TODO(), channelID, m)
		if err != nil {
			return "", fmt.Errorf("file upload failed: %s", err)
		}
	}

	return "", nil
}
