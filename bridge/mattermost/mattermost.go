package bmattermost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/matterbridge-org/matterbridge/matterhook"
	"github.com/matterbridge/matterclient"
	"github.com/rs/xid"
)

type Bmattermost struct {
	mh     *matterhook.Client
	mc     *matterclient.Client
	v6     bool
	uuid   string
	TeamID string
	*bridge.Config
	avatarMap      map[string]string
	channelsMutex  sync.RWMutex
	channelInfoMap map[string]*config.ChannelInfo
}

const mattermostPlugin = "mattermost.plugin"

var errChannelNotFound = errors.New("could not find channel ID for channel")

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bmattermost{
		Config:         cfg,
		avatarMap:      make(map[string]string),
		channelInfoMap: make(map[string]*config.ChannelInfo),
	}

	b.v6 = b.GetBool("v6")
	b.uuid = xid.New().String()

	return b
}

func (b *Bmattermost) Command(cmd string) string {
	return ""
}

func (b *Bmattermost) Connect() error {
	if b.Account == mattermostPlugin {
		return nil
	}

	if strings.HasPrefix(b.getVersion(), "6.") || strings.HasPrefix(b.getVersion(), "7.") {
		if !b.v6 {
			b.v6 = true
		}
	}

	if b.GetString("WebhookBindAddress") != "" {
		if err := b.doConnectWebhookBind(); err != nil {
			return err
		}
		go b.handleMatter()
		return nil
	}
	switch {
	case b.GetString("WebhookURL") != "":
		if err := b.doConnectWebhookURL(); err != nil {
			return err
		}
		go b.handleMatter()
		return nil
	case b.GetString("Token") != "":
		b.Log.Info("Connecting using token (sending and receiving)")
		err := b.apiLogin()
		if err != nil {
			return err
		}
		go b.handleMatter()
	case b.GetString("Login") != "":
		b.Log.Info("Connecting using login/password (sending and receiving)")
		b.Log.Infof("Using mattermost v6 methods: %t", b.v6)
		err := b.apiLogin()
		if err != nil {
			return err
		}
		go b.handleMatter()
	}
	if b.GetString("WebhookBindAddress") == "" && b.GetString("WebhookURL") == "" &&
		b.GetString("Login") == "" && b.GetString("Token") == "" {
		return errors.New("no connection method found. See that you have WebhookBindAddress, WebhookURL or Token/Login/Password/Server/Team configured")
	}
	return nil
}

func (b *Bmattermost) Disconnect() error {
	return nil
}

func (b *Bmattermost) JoinChannel(channel config.ChannelInfo) error {
	if b.Account == mattermostPlugin {
		return nil
	}

	b.channelsMutex.Lock()
	b.channelInfoMap[channel.ID] = &channel
	b.channelsMutex.Unlock()

	// we can only join channels using the API
	if b.mc != nil {
		err := b.joinChannelAPI(channel.Name)
		if err != nil && b.GetString("WebhookURL") != "" {
			// We can still post through the webhook: don't fail.
			b.Log.Warnf("%s: threads, edits and deletes will not work in this channel", err)
			return nil
		}

		return err
	}

	return nil
}

func (b *Bmattermost) Send(msg config.Message) (string, error) {
	if b.Account == mattermostPlugin {
		return "", nil
	}
	b.Log.Debugf("=> Receiving %#v", msg)

	// Make a action /me of the message
	if msg.Event == config.EventUserAction {
		msg.Text = "*" + msg.Text + "*"
	}

	// map the file SHA to our user (caches the avatar)
	if msg.Event == config.EventAvatarDownload {
		return b.cacheAvatar(&msg)
	}

	useWebhook := b.GetString("WebhookURL") != ""

	// Webhook only (no Token/Login): we can't thread, edit or delete.
	if useWebhook && b.mc == nil {
		msg.ParentID = ""
		if msg.ID != "" {
			b.Log.Debugf("Can't edit a message using only a webhook, configure a Token to enable editing")
			return "", nil
		}
		return b.sendWebhook(msg)
	}

	// Delete message
	if msg.Event == config.EventMsgDelete {
		if msg.ID == "" {
			return "", nil
		}

		return msg.ID, b.mc.DeleteMessage(msg.ID)
	}

	// File deletions carry the native file ID of the source bridge, not a
	// post ID: they must not be handled as edits.
	if msg.Event == config.EventFileDelete {
		return "", nil
	}

	// The webhook never relayed events (join/leave, topic changes, ...).
	if useWebhook && msg.Event != "" && msg.Event != config.EventUserAction {
		return "", nil
	}

	// Handle prefix hint for unthreaded messages.
	if msg.ParentNotFound() {
		msg.ParentID = ""
		msg.Text = fmt.Sprintf("[thread]: %s", msg.Text)
	}

	// we only can reply to the root of the thread, not to a specific ID (like discord for example does)
	if msg.ParentID != "" {
		post, _, err := b.mc.Client.GetPost(context.TODO(), msg.ParentID, "")
		switch {
		case err != nil || post == nil:
			// The parent is unknown on this server: posting with an invalid
			// root_id would be rejected and the message lost, so post it
			// unthreaded with the usual hint instead.
			b.Log.Errorf("getting parent post %s failed, sending unthreaded: %s", msg.ParentID, err)
			msg.ParentID = ""
			msg.Text = "[thread]: " + msg.Text
		case post.RootId != "":
			msg.ParentID = post.RootId
		}
	}

	// Edit message if we have an ID. This must be handled before files:
	// edits of messages carrying attachments (e.g. a Slack image whose caption
	// is edited) arrive with the files again, and re-uploading them would
	// create a new post instead of editing the existing one.
	if msg.ID != "" {
		if useWebhook {
			// keep the text as it was posted through the webhook
			return b.editPost(msg.ID, b.webhookText(&msg))
		}

		text := msg.Text
		if text == "" {
			text = fileComments(&msg)
		}

		return b.editPost(msg.ID, b.prefixNick(msg.Username, text))
	}

	// Post through the webhook (keeps the remote user's name and avatar); the
	// API is used to thread, and to find the ID of the post for later edits.
	if useWebhook {
		return b.sendWebhook(msg)
	}

	// Upload a file if it exists
	if msg.Extra != nil {
		for _, rmsg := range helper.HandleExtra(&msg, b.General) {
			if _, err := b.mc.PostMessage(b.getChannelID(rmsg.Channel), rmsg.Username+rmsg.Text, msg.ParentID); err != nil {
				b.Log.Errorf("PostMessage failed: %s", err)
			}
		}
		if len(msg.Extra["file"]) > 0 {
			return b.handleUploadFile(&msg)
		}
	}

	// Post normal message
	return b.mc.PostMessage(b.getChannelID(msg.Channel), b.prefixNick(msg.Username, msg.Text), msg.ParentID)
}

func (b *Bmattermost) joinChannelAPI(name string) error {
	id := b.getChannelID(name)
	if id == "" {
		return fmt.Errorf("%w %s", errChannelNotFound, name)
	}

	return b.mc.JoinChannel(id)
}
