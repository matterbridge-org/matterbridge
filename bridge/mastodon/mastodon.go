package mastodon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"
	"github.com/42wim/matterbridge/bridge/helper"

	mastodon "github.com/mattn/go-mastodon"
)

var (
	htmlReplacementTag = regexp.MustCompile("<[^>]*>")
	channelTypeHome    = "home"
	channelTypeLocal   = "local"
	channelTypeRemote  = "remote"
	channelTypeDirect  = "direct"
)

var errInvalidChannel = errors.New("invalid channel name")

func InvalidChannelError(name string) error {
	return fmt.Errorf("%w: %s", errInvalidChannel, name)
}

type Bmastodon struct {
	*bridge.Config

	c       *mastodon.Client
	account *mastodon.Account

	rooms   []string
	handles []context.CancelFunc
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bmastodon{Config: cfg}
	return b
}

func (b *Bmastodon) Connect() error {
	b.Log.Infof("Connecting %s", b.GetString("Server"))

	cfg := mastodon.Config{
		Server:       b.GetString("Server"),
		ClientID:     b.GetString("ClientID"),
		ClientSecret: b.GetString("ClientSecret"),
		AccessToken:  b.GetString("AccessToken"),
	}
	b.c = mastodon.NewClient(&cfg)

	var err error

	b.account, err =
		b.c.GetAccountCurrentUser(context.Background())
	if err != nil {
		return err
	}

	return nil
}

func (b *Bmastodon) Disconnect() error {
	for _, ctxCancel := range b.handles {
		ctxCancel()
	}

	return nil
}

func (b *Bmastodon) JoinChannel(channel config.ChannelInfo) error {
	var (
		channelType string
		ch          chan mastodon.Event
		err         error
	)

	ctx, ctxCancel := context.WithCancel(context.Background())

	switch channel.Name {
	case "home":
		channelType = channelTypeHome
		ch, err = b.c.StreamingUser(ctx)
	case "local":
		channelType = channelTypeLocal
		ch, err = b.c.StreamingPublic(ctx, true)
	case "remote":
		channelType = channelTypeRemote
		ch, err = b.c.StreamingPublic(ctx, false)
	default:
		if !strings.HasPrefix(channel.Name, "@") {
			ctxCancel()
			return InvalidChannelError(channel.Name)
		}

		channelType = channelTypeDirect
		ch, err = b.c.StreamingDirect(ctx)
	}

	if err != nil {
		ctxCancel()
		return err
	}

	b.rooms = append(b.rooms, channel.Name)
	b.handles = append(b.handles, ctxCancel)

	go func() {
		b.Log.Debugf("run golang channel on streaming api call, channel name: %v", channel.Name)

		for msg := range ch {
			switch t := msg.(type) {
			case *mastodon.UpdateEvent:
				switch channelType {
				case channelTypeHome, channelTypeLocal, channelTypeRemote:
					b.handleSendRemoteStatus(t.Status, channel.Name)
				default:
					b.Log.Debugf("run UpdateEvent on unsupported channelType: %s", channelType)
				}
			case *mastodon.ConversationEvent:
				switch channelType {
				case channelTypeHome, channelTypeLocal, channelTypeRemote:
					// Not a conversation
					b.Log.Debugf("run ConversationEvent on unsupported channelType: %s", channelType)
				default:
					b.handleSendRemoteStatus(t.Conversation.LastStatus, channel.Name)
				}
			}
		}
	}()

	return nil
}

func (b *Bmastodon) Send(msg config.Message) (string, error) {
	ctx := context.Background()

	// Standard Message Send
	if msg.Event == "" {
		sentMessage, err := b.handleSendingMessage(ctx, &msg)
		if err != nil {
			b.Log.Errorf("Could not send message to room %v from %v: %v", msg.Channel, msg.Username, err)

			return "", nil
		}

		return string(sentMessage.ID), nil
	}

	// Message Deletion
	if msg.Event == config.EventMsgDelete {
		if msg.UserID != string(b.account.ID) {
			b.Log.Errorf("Can not delete a status that is owned by a different account")
			return "", nil
		}

		err := b.c.DeleteStatus(context.Background(), mastodon.ID(msg.ID))

		return "", err
	}

	// Message is not a type that is currently supported
	return "", nil
}

func (b *Bmastodon) handleSendRemoteStatus(msg *mastodon.Status, channel string) {
	if msg.Account.ID == b.account.ID {
		// Ignore messages that are from the bot user
		return
	}

	remoteMessage := config.Message{
		Text:     htmlReplacementTag.ReplaceAllString(msg.Content, ""),
		Channel:  channel,
		Username: msg.Account.DisplayName,
		UserID:   string(msg.Account.ID),
		Account:  b.Account,
		Avatar:   msg.Account.Avatar,
		ID:       string(msg.ID),
		Extra:    map[string][]any{},
	}
	if len(msg.MediaAttachments) > 0 {
		remoteMessage.Extra["file"] = []any{}
	}

	for _, media := range msg.MediaAttachments {
		b, err2 := helper.DownloadFile(media.RemoteURL)
		if err2 != nil {
			// TODO: log
			continue
		}

		remoteMessage.Extra["file"] = append(remoteMessage.Extra["file"], config.FileInfo{
			Name:   media.Description,
			Data:   b,
			Size:   int64(len(*b)),
			Avatar: false,
		})
	}

	b.Log.Debugf("<= Message is %#v", remoteMessage)

	b.Remote <- remoteMessage
}

func (b *Bmastodon) handleSendingMessage(ctx context.Context, msg *config.Message) (*mastodon.Status, error) {
	toot := mastodon.Toot{
		Status:      msg.Text,
		InReplyToID: "",
		MediaIDs:    []mastodon.ID{},
		Sensitive:   false,
		SpoilerText: "",
		Visibility:  "public",
		Language:    "",
	}
	if strings.HasPrefix(msg.Channel, "#") {
		toot.Status += " " + msg.Channel
	}

	if strings.HasPrefix(msg.Channel, "@") {
		toot.Visibility = "private"
	}

	if msg.ParentID != "" {
		toot.InReplyToID = mastodon.ID(msg.ParentID)
		if toot.Visibility == "public" {
			toot.Visibility = "unlisted"
		}
	}

	for _, file := range *msg.GetFileInfos(b.Log) {
		attachment, err := b.c.UploadMediaFromMedia(ctx, &mastodon.Media{
			File:        bytes.NewReader(*file.Data),
			Description: file.Comment,
		})
		if err != nil {
			b.Log.Error(err)
			continue
		}

		toot.MediaIDs = append(toot.MediaIDs, attachment.ID)
	}

	return b.c.PostStatus(ctx, &toot)
}
