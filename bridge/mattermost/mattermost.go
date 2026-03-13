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
        "github.com/mattermost/mattermost/server/public/model"
        "github.com/rs/xid"
)

type Bmattermost struct {
        mh     *matterhook.Client
        mc     *matterclient.Client
        v6     bool
        uuid   string
        TeamID string
        *bridge.Config
        avatarMap        map[string]string
        displayNameCache map[string]string
        channelsMutex    sync.RWMutex
        channelInfoMap   map[string]*config.ChannelInfo
}

const mattermostPlugin = "mattermost.plugin"

func New(cfg *bridge.Config) bridge.Bridger {
        b := &Bmattermost{
                Config:           cfg,
                avatarMap:        make(map[string]string),
                displayNameCache: make(map[string]string),
                channelInfoMap:   make(map[string]*config.ChannelInfo),
        }

        b.v6 = b.GetBool("v6")
        b.uuid = xid.New().String()

        return b
}

// getDisplayName returns the full display name (FirstName + LastName) for a
// Mattermost user, using a cache to avoid redundant API calls. Returns "" if
// the user has no first/last name set.
func (b *Bmattermost) getDisplayName(userID string) string {
        if dn, ok := b.displayNameCache[userID]; ok {
                return dn
        }
        if b.mc == nil {
                return ""
        }
        user, _, err := b.mc.Client.GetUser(context.TODO(), userID, "")
        if err != nil || user == nil {
                b.displayNameCache[userID] = ""
                return ""
        }
        dn := strings.TrimSpace(user.FirstName + " " + user.LastName)
        b.displayNameCache[userID] = dn
        return dn
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
                if err := b.mc.JoinChannel(id); err != nil {
                        return err
                }
        }

        // Scan recent messages for historical source-ID markers, then replay missed messages.
        go func() {
                b.scanHistoricalMappings(channel)
                b.replayMissedMessages(channel)
        }()

        return nil
}

// scanHistoricalMappings scans recent channel messages for matterbridge_srcid
// props and sends EventHistoricalMapping events to the gateway for persistent
// cache population.
func (b *Bmattermost) scanHistoricalMappings(channel config.ChannelInfo) {
        if b.mc == nil {
                return
        }
        channelID := b.getChannelID(channel.Name)
        if channelID == "" {
                return
        }

        postList, _, err := b.mc.Client.GetPostsForChannel(context.TODO(), channelID, 0, 200, "", false, false)
        if err != nil {
                b.Log.Debugf("scanHistoricalMappings: GetPostsForChannel %s: %s", channel.Name, err)
                return
        }

        count := 0
        for _, id := range postList.Order {
                post := postList.Posts[id]
                srcID, ok := post.Props["matterbridge_srcid"].(string)
                if !ok || srcID == "" {
                        continue
                }
                b.Remote <- config.Message{
                        Event:   config.EventHistoricalMapping,
                        Account: b.Account,
                        Channel: channel.Name,
                        ID:      post.Id,
                        Extra:   map[string][]interface{}{"source_msgid": {srcID}},
                }
                count++
        }
        if count > 0 {
                b.Log.Infof("scanHistoricalMappings: found %d mappings in %s", count, channel.Name)
        }
}

// replayMissedMessages fetches recent messages from the channel and replays any
// that were not yet bridged. This catches up on messages missed during downtime.
func (b *Bmattermost) replayMissedMessages(channel config.ChannelInfo) {
        if b.mc == nil || b.IsMessageBridged == nil || b.GetLastSeen == nil {
                return
        }

        channelID := b.getChannelID(channel.Name)
        if channelID == "" {
                return
        }

        channelKey := channel.Name + b.Account
        lastSeen, ok := b.GetLastSeen(channelKey)
        if !ok {
                // First start: no replay, let the cache initialize through normal operation.
                b.Log.Debugf("replayMissedMessages: no lastSeen for %s, skipping (first start)", channelKey)
                return
        }
        cutoff := lastSeen

        sinceMillis := cutoff.UnixMilli()
        postList, _, err := b.mc.Client.GetPostsSince(context.TODO(), channelID, sinceMillis, false)
        if err != nil {
                b.Log.Errorf("replayMissedMessages: GetPostsSince failed: %s", err)
                return
        }

        // Collect and sort posts by CreateAt ascending (oldest first).
        type postEntry struct {
                id   string
                post *model.Post
        }
        var posts []postEntry
        for _, id := range postList.Order {
                post := postList.Posts[id]
                if post.CreateAt < sinceMillis {
                        continue
                }
                posts = append(posts, postEntry{id, post})
        }
        // Sort oldest first.
        for i := 0; i < len(posts); i++ {
                for j := i + 1; j < len(posts); j++ {
                        if posts[j].post.CreateAt < posts[i].post.CreateAt {
                                posts[i], posts[j] = posts[j], posts[i]
                        }
                }
        }

        count := 0
        propKey := "matterbridge_" + b.uuid
        for _, pe := range posts {
                post := pe.post

                // Skip messages sent by matterbridge itself.
                if post.Props != nil {
                        if _, ok := post.Props[propKey].(bool); ok {
                                continue
                        }
                        // Also skip test messages.
                        if _, ok := post.Props["matterbridge_test"]; ok {
                                continue
                        }
                }

                // Skip system messages.
                if post.Type != "" && strings.HasPrefix(post.Type, "system_") {
                        continue
                }

                // Skip if already bridged.
                if b.IsMessageBridged("mattermost", post.Id) {
                        continue
                }

                // Resolve username for the post author.
                username := ""
                if post.Props != nil {
                        if override, ok := post.Props["override_username"].(string); ok && override != "" {
                                username = override
                        }
                }
                var displayName string
                if username == "" {
                        user, _, userErr := b.mc.Client.GetUser(context.TODO(), post.UserId, "")
                        if userErr == nil && user != nil {
                                if !b.GetBool("useusername") && user.Nickname != "" {
                                        username = user.Nickname
                                } else {
                                        username = user.Username
                                }
                                dn := strings.TrimSpace(user.FirstName + " " + user.LastName)
                                b.displayNameCache[post.UserId] = dn
                                displayName = dn
                        } else {
                                username = "unknown"
                        }
                }

                // Format replay prefix with original timestamp.
                createTime := time.UnixMilli(post.CreateAt)
                replayPrefix := fmt.Sprintf("[Replay %s]\n", createTime.Format("2006-01-02 15:04 MST"))

                rmsg := config.Message{
                        Event:    config.EventReplayMessage,
                        Account:  b.Account,
                        Channel:  channel.Name,
                        Username: username,
                        UserID:   post.UserId,
                        Text:     replayPrefix + post.Message,
                        ID:       post.Id,
                        ParentID: post.RootId,
                        Extra:    make(map[string][]interface{}),
                }
                if displayName != "" {
                        rmsg.Extra["displayname"] = []interface{}{displayName}
                }

                // Handle file attachments.
                for _, fileID := range post.FileIds {
                        if dlErr := b.handleDownloadFile(&rmsg, fileID); dlErr != nil {
                                b.Log.Errorf("replay: download failed for %s: %s", fileID, dlErr)
                        }
                }

                b.Remote <- rmsg
                count++
                time.Sleep(500 * time.Millisecond)
        }

        if count > 0 {
                b.Log.Infof("replayMissedMessages: replayed %d messages from %s", count, channel.Name)
        }
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
                        extraPost := &model.Post{
                                ChannelId: b.getChannelID(rmsg.Channel),
                                Message:   rmsg.Text,
                                RootId:    msg.ParentID,
                                Props: model.StringInterface{
                                        "from_webhook":           "true",
                                        "override_username":      strings.TrimSpace(rmsg.Username),
                                        "matterbridge_" + b.uuid: true,
                                },
                        }
                        if rmsg.Avatar != "" {
                                extraPost.Props["override_icon_url"] = rmsg.Avatar
                        }
                        if _, _, err := b.mc.Client.CreatePost(context.TODO(), extraPost); err != nil {
                                b.Log.Errorf("PostMessage failed: %s", err)
                        }
                }
                if len(msg.Extra["file"]) > 0 {
                        return b.handleUploadFile(&msg)
                }
        }

        // Edit message if we have an ID — use PatchPost to preserve override props.
        if msg.ID != "" {
                props := model.StringInterface{
                        "from_webhook":           "true",
                        "override_username":      strings.TrimSpace(msg.Username),
                        "matterbridge_" + b.uuid: true,
                }
                if msg.Avatar != "" {
                        props["override_icon_url"] = msg.Avatar
                }
                _, _, err := b.mc.Client.PatchPost(context.TODO(), msg.ID, &model.PostPatch{
                        Message: &msg.Text,
                        Props:   &props,
                })
                if err != nil {
                        return "", err
                }
                return msg.ID, nil
        }

        // Post normal message with override_username/icon so it appears as the
        // bridged user (same as handleUploadFile does for file posts).
        post := &model.Post{
                ChannelId: b.getChannelID(msg.Channel),
                Message:   msg.Text,
                RootId:    msg.ParentID,
                Props: model.StringInterface{
                        "from_webhook":           "true",
                        "override_username":      strings.TrimSpace(msg.Username),
                        "matterbridge_" + b.uuid: true,
                },
        }
        if msg.Avatar != "" {
                post.Props["override_icon_url"] = msg.Avatar
        }
        if msg.Extra != nil {
                if srcIDs, ok := msg.Extra["source_msgid"]; ok && len(srcIDs) > 0 {
                        if srcID, ok := srcIDs[0].(string); ok {
                                post.Props["matterbridge_srcid"] = srcID
                        }
                }
        }
        created, _, err := b.mc.Client.CreatePost(context.TODO(), post)
        if err != nil {
                return "", err
        }
        return created.Id, nil
}
