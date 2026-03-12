package bmattermost

import (
        "context"
        "errors"
        "fmt"
        "strings"
        "sync"
        "time"

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
                // doConnectWebhookURL() already calls apiLogin() if Token or Login
                // is configured, so b.mc is available for hybrid mode.
                if b.mc != nil {
                        b.Log.Info("Hybrid mode: webhook for new messages, API for thread replies/edits/deletes")
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
        if b.GetString("WebhookURL") == "" && b.GetString("WebhookBindAddress") == "" {
                id := b.getChannelID(channel.Name)
                if id == "" {
                        return fmt.Errorf("Could not find channel ID for channel %s", channel.Name)
                }
                return b.mc.JoinChannel(id)
        }

        return nil
}

// lookupWebhookPostID searches recent channel posts to find the ID of a message
// just sent via webhook. Webhooks don't return post IDs, but the gateway needs
// them to map replies across bridges. We look for a recent post from the bot
// user that has our matterbridge uuid prop set and matches the expected text.
func (b *Bmattermost) lookupWebhookPostID(channelName, text string) string {
        if b.mc == nil {
                return ""
        }

        channelID := b.getChannelID(channelName)
        if channelID == "" {
                return ""
        }

        postList, _, err := b.mc.Client.GetPostsForChannel(context.TODO(), channelID, 0, 10, "", false, false)
        if err != nil {
                b.Log.Debugf("lookupWebhookPostID: GetPostsForChannel failed: %s", err)
                return ""
        }

        now := time.Now().UnixMilli()
        propKey := "matterbridge_" + b.uuid

        for _, id := range postList.Order {
                post := postList.Posts[id]
                if now-post.CreateAt > 5000 {
                        continue
                }
                if _, ok := post.Props[propKey]; !ok {
                        continue
                }
                if post.RootId != "" {
                        continue
                }
                if strings.Contains(post.Message, text) || post.Message == text {
                        b.Log.Debugf("lookupWebhookPostID: found post %s for webhook message", post.Id)
                        return post.Id
                }
        }

        b.Log.Debugf("lookupWebhookPostID: no matching post found")
        return ""
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

        // --- Hybrid mode: webhook for top-level, API for replies/edits/deletes ---
        if b.GetString("WebhookURL") != "" {
                isReply := msg.ParentValid()
                isEdit := msg.ID != "" && msg.Event == "msg_update"
                isDelete := msg.Event == config.EventMsgDelete

                if !isReply && !isEdit && !isDelete {
                        // Top-level new message with files → upload files via API first,
                        // then send any remaining text via webhook. Webhooks can't upload
                        // binary files, so we need the API path for actual file uploads.
                        if msg.Extra != nil && len(msg.Extra["file"]) > 0 && b.mc != nil {
                                postID, err := b.handleUploadFile(&msg)
                                if err != nil {
                                        b.Log.Errorf("handleUploadFile failed: %s", err)
                                }
                                // If there's no remaining text, return the upload post ID
                                // so the gateway can cache it for thread-reply mapping.
                                if strings.TrimSpace(msg.Text) == "" {
                                        return postID, nil
                                }
                                // Clear the files so sendWebhook doesn't append URLs again.
                                delete(msg.Extra, "file")
                        }

                        // Top-level new message → use webhook (username/avatar override).
                        // Remember the original text for post-ID lookup.
                        originalText := msg.Text

                        _, err := b.sendWebhook(msg)
                        if err != nil {
                                return "", err
                        }

                        // Look up the post ID so the gateway can cache it for reply mapping.
                        postID := b.lookupWebhookPostID(msg.Channel, originalText)
                        if postID != "" {
                                return postID, nil
                        }
                        return "", nil
                }

                // Reply, edit, or delete → need API client.
                if b.mc == nil {
                        b.Log.Warnf("Cannot send thread reply/edit/delete via API: no API connection.")
                        if isReply {
                                return b.sendWebhook(msg)
                        }
                        return "", fmt.Errorf("API client not available for edit/delete")
                }
                // Fall through to the API path below.
        }

        // Delete message
        if msg.Event == config.EventMsgDelete {
                if msg.ID == "" {
                        return "", nil
                }
                return msg.ID, b.mc.DeleteMessage(msg.ID)
        }

        // Handle prefix hint for unthreaded messages.
        if msg.ParentNotFound() {
                msg.ParentID = ""
                msg.Text = fmt.Sprintf("[thread]: %s", msg.Text)
        }

        // we only can reply to the root of the thread, not to a specific ID
        if msg.ParentID != "" {
                post, _, err := b.mc.Client.GetPost(context.TODO(), msg.ParentID, "")
                if err != nil {
                        b.Log.Errorf("getting post %s failed: %s", msg.ParentID, err)
                }
                if post != nil && post.RootId != "" {
                        msg.ParentID = post.RootId
                }
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

        // Prepend nick if configured. Bold the username and put the message
        // on the next line so it visually matches webhook post styling.
        if b.GetBool("PrefixMessagesWithNick") {
                msg.Text = "**" + strings.TrimSpace(msg.Username) + "**\n" + msg.Text
        }

        // Edit message if we have an ID
        if msg.ID != "" {
                return b.mc.EditMessage(msg.ID, msg.Text)
        }

        // Post normal message
        return b.mc.PostMessage(b.getChannelID(msg.Channel), msg.Text, msg.ParentID)
}
