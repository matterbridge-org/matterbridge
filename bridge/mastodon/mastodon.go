package mastodon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/42wim/matterbridge/bridge"
	"github.com/42wim/matterbridge/bridge/config"

	mastodon "github.com/mattn/go-mastodon"
)

type Bmastodon struct {
	*bridge.Config
	c       *mastodon.Client
	account *mastodon.Account

	ctxHome         context.Context
	ctxCancelHome   context.CancelFunc
	ctxLocal        context.Context
	ctxCancelLocal  context.CancelFunc
	ctxDirect       context.Context
	ctxCancelDirect context.CancelFunc
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bmastodon{Config: cfg}
	return b
}

func (b *Bmastodon) Connect() error {
	b.Log.Infof("Connecting %s", b.GetString("Server"))

	config := mastodon.Config{
		Server:       b.GetString("Server"),
		ClientID:     b.GetString("ClientID"),
		ClientSecret: b.GetString("ClientSecret"),
		AccessToken:  b.GetString("AccessToken"),
	}
	b.c = mastodon.NewClient(&config)
	var err error
	b.account, err =
		b.c.GetAccountCurrentUser(context.Background())
	if err != nil {
		return nil
	}

	return nil
}

func (b *Bmastodon) Disconnect() error {
	if b.ctxCancelHome != nil {
		b.ctxCancelHome()
	}
	if b.ctxCancelLocal != nil {
		b.ctxCancelLocal()
	}
	if b.ctxCancelDirect != nil {
		b.ctxCancelDirect()
	}

	return nil
}

func (b *Bmastodon) JoinChannel(channel config.ChannelInfo) error {
	var channelType string
	var ch chan mastodon.Event
	var err error
	if channel.Name == "home" {
		// You are talking to the home channel
		channelType = "home"
		b.ctxHome, b.ctxCancelHome = context.WithCancel(context.Background())
		ch, err = b.c.StreamingUser(b.ctxHome)
	} else if channel.Name == "local" {
		channelType = "local"
		// You are talking to the local channel
		b.ctxLocal, b.ctxCancelLocal = context.WithCancel(context.Background())
		ch, err = b.c.StreamingPublic(b.ctxLocal, true)
	} else if strings.HasPrefix(channel.Name, "@") {
		channelType = "direct"
		// You are talking to a private user
		if b.ctxCancelDirect == nil {
			b.ctxDirect, b.ctxCancelDirect = context.WithCancel(context.Background())
			ch, err = b.c.StreamingDirect(b.ctxDirect)
		}
	} else {
		return fmt.Errorf("invalid channel name: %s", channel.Name)
	}
	if err != nil {
		return err
	}

	go func() {
		for msg := range ch {
			switch t := msg.(type) {
			case *mastodon.UpdateEvent:
				switch channelType {
				case "local", "home":
					b.handleSendRemoteStatus(t.Status, channelType)
				}
			case *mastodon.ConversationEvent:
				b.handleSendRemoteStatus(t.Conversation.LastStatus, "@"+t.Conversation.Accounts[0].Acct)
			}

		}
	}()
	return nil
}

func (b *Bmastodon) handleSendRemoteStatus(msg *mastodon.Status, channel string) {
	remoteMessage := config.Message{
		Text:     msg.Content,
		Channel:  channel,
		Username: msg.Account.Username,
		UserID:   string(msg.Account.ID),
		Account:  msg.Account.DisplayName,
		Avatar:   msg.Account.Avatar,
		ID:       string(msg.ID),
		Extra:    map[string][]any{},
	}
	if len(msg.MediaAttachments) > 0 {
		remoteMessage.Extra["file"] = []any{}
	}
	for _, media := range msg.MediaAttachments {
		resp, err := http.Get(media.RemoteURL)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}
		remoteMessage.Extra["file"] = append(remoteMessage.Extra["file"], config.FileInfo{
			Name:   media.Description,
			Data:   &b,
			Size:   int64(len(b)),
			Avatar: false,
		})
	}
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

	for _, file := range msg.Extra["file"] {
		fileInfo, ok := file.(config.FileInfo)
		if !ok {
			continue
		}
		var r io.Reader
		var err error
		var resp *http.Response
		defer func() {
			if resp != nil {
				resp.Body.Close()
			}
		}()
		if fileInfo.URL != "" {
			resp, err = http.Get(fileInfo.URL)
			if err != nil {
				continue
			}
			if resp.StatusCode != http.StatusOK {
				continue
			}
			r = resp.Body
		} else if fileInfo.Data != nil {
			r = bytes.NewReader(*fileInfo.Data)
		}
		attachment, err := b.c.UploadMediaFromMedia(ctx, &mastodon.Media{
			File:        r,
			Description: fileInfo.Comment,
		})
		if err != nil {
			continue
		}
		toot.MediaIDs = append(toot.MediaIDs, attachment.ID)
	}

	return b.c.PostStatus(ctx, &toot)
}
