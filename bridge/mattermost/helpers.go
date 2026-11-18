package bmattermost

import (
	"context"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/matterbridge-org/matterbridge/matterhook"
	"github.com/matterbridge/matterclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/rs/xid"
)

const systemUsername = "system"

func (b *Bmattermost) doConnectWebhookBind() error {
	switch {
	case b.GetString("WebhookURL") != "":
		b.Log.Info("Connecting using webhookurl (sending) and webhookbindaddress (receiving)")
		b.mh = matterhook.New(b.GetString("WebhookURL"),
			matterhook.Config{
				InsecureSkipVerify: b.GetBool("SkipTLSVerify"),
				BindAddress:        b.GetString("WebhookBindAddress"),
			})
	case b.GetString("Token") != "":
		b.Log.Info("Connecting using token (sending)")
		err := b.apiLogin()
		if err != nil {
			return err
		}
	case b.GetString("Login") != "":
		b.Log.Info("Connecting using login/password (sending)")
		err := b.apiLogin()
		if err != nil {
			return err
		}
	default:
		b.Log.Info("Connecting using webhookbindaddress (receiving)")
		b.mh = matterhook.New(b.GetString("WebhookURL"),
			matterhook.Config{
				InsecureSkipVerify: b.GetBool("SkipTLSVerify"),
				BindAddress:        b.GetString("WebhookBindAddress"),
			})
	}
	return nil
}

func (b *Bmattermost) doConnectWebhookURL() error {
	b.Log.Info("Connecting using webhookurl (sending)")
	b.mh = matterhook.New(b.GetString("WebhookURL"),
		matterhook.Config{
			InsecureSkipVerify: b.GetBool("SkipTLSVerify"),
			DisableServer:      true,
		})
	if b.GetString("Token") != "" {
		b.Log.Info("Connecting using token (receiving)")
		err := b.apiLogin()
		if err != nil {
			return err
		}
	} else if b.GetString("Login") != "" {
		b.Log.Info("Connecting using login/password (receiving)")
		err := b.apiLogin()
		if err != nil {
			return err
		}
	}

	if b.mc != nil {
		b.Log.Info("Using the API next to the webhook for threads, edits and deletes")
		b.checkWebhookOwner()
	}
	return nil
}

// checkWebhookOwner warns when the posts made through WebhookURL can't be
// edited or deleted by our user: webhook posts belong to the webhook creator.
func (b *Bmattermost) checkWebhookOwner() {
	if b.mc.User == nil {
		return
	}

	hookID := path.Base(strings.TrimRight(b.GetString("WebhookURL"), "/"))

	hook, _, err := b.mc.Client.GetIncomingWebhook(context.TODO(), hookID, "")
	if err != nil {
		b.Log.Warnf("Could not check the owner of WebhookURL (%s). Edits and deletes of bridged messages "+
			"only work if %s created the webhook or may edit/delete others' posts.", err, b.mc.User.Username)

		return
	}

	if hook.UserId != b.mc.User.Id {
		b.Log.Warnf("WebhookURL was not created by %s: edits and deletes of bridged messages will fail "+
			"unless %s may edit/delete others' posts. Recreate the webhook with this user to fix this.",
			b.mc.User.Username, b.mc.User.Username)

		return
	}

	b.Log.Infof("WebhookURL is owned by %s: edits and deletes of bridged messages are supported", b.mc.User.Username)
}

//nolint:wrapcheck
func (b *Bmattermost) apiLogin() error {
	password := b.GetString("Password")
	if b.GetString("Token") != "" {
		password = "token=" + b.GetString("Token")
	}

	b.mc = matterclient.New(b.GetString("Login"), password, b.GetString("Team"), b.GetString("Server"), "")
	if b.GetBool("debug") {
		b.mc.SetLogLevel("debug")
	}
	b.mc.SkipTLSVerify = b.GetBool("SkipTLSVerify")
	b.mc.SkipVersionCheck = b.GetBool("SkipVersionCheck")
	b.mc.NoTLS = b.GetBool("NoTLS")
	b.Log.Infof("Connecting %s (team: %s) on %s", b.GetString("Login"), b.GetString("Team"), b.GetString("Server"))

	if err := b.mc.Login(); err != nil {
		return err
	}

	if b.mc.User != nil {
		b.Log.Infof("Connection succeeded as user %s (bot: %t)", b.mc.User.Username, b.mc.User.IsBot)
	} else {
		b.Log.Info("Connection succeeded")
	}
	b.TeamID = b.mc.GetTeamID()
	return nil
}

// replaceAction replace the message with the correct action (/me) code
func (b *Bmattermost) replaceAction(text string) (string, bool) {
	if strings.HasPrefix(text, "*") && strings.HasSuffix(text, "*") {
		return strings.ReplaceAll(text, "*", ""), true
	}
	return text, false
}

func (b *Bmattermost) cacheAvatar(msg *config.Message) (string, error) {
	fi := msg.Extra["file"][0].(config.FileInfo)
	/* if we have a sha we have successfully uploaded the file to the media server,
	so we can now cache the sha */
	if fi.SHA != "" {
		b.Log.Debugf("Added %s to %s in avatarMap", fi.SHA, msg.UserID)
		b.avatarMap[msg.UserID] = fi.SHA
	}
	return "", nil
}

// webhookRefProp is the post prop used to find back the posts we send through
// the webhook: Mattermost incoming webhooks don't return the ID of the post.
const webhookRefProp = "matterbridge_ref"

// webhookText returns the text of msg as it is posted through the webhook.
func (b *Bmattermost) webhookText(msg *config.Message) string {
	text := msg.Text

	if len(msg.Extra["file"]) > 0 {
		// Some bridges (e.g. Slack) move the text of a message with
		// attachments into the comment of the file: don't drop it.
		if text == "" {
			text = fileComments(msg)
		}

		// webhook doesn't support file uploads, so we add the url manually
		parts := []string{text}

		for _, f := range msg.Extra["file"] {
			fi, ok := f.(config.FileInfo)
			if ok && fi.URL != "" {
				parts = append(parts, fi.URL)
			}
		}

		text = strings.Join(parts, " ")
		text = strings.TrimSpace(text)
	}

	return b.prefixNick(msg.Username, text)
}

// sendWebhook uses the configured WebhookURL to send the message.
//
// When the API is available too (Token or Login configured), the message is
// posted in its thread (msg.ParentID must be the root post) and the ID of the
// created post is returned, so that the gateway can map later replies and
// edits to it.
func (b *Bmattermost) sendWebhook(msg config.Message) (string, error) {
	// skip events, /me actions are normal messages
	if msg.Event != "" && msg.Event != config.EventUserAction {
		return "", nil
	}

	b.sendWebhookExtra(&msg)

	text := b.webhookText(&msg)
	iconURL := config.GetIconURL(&msg, b.GetString("iconurl"))
	matterMessage := matterhook.OMessage{
		IconURL:  iconURL,
		Channel:  msg.Channel,
		UserName: msg.Username,
		Text:     text,
		RootID:   msg.ParentID,
		Props:    make(map[string]any),
	}
	if msg.Avatar != "" {
		matterMessage.IconURL = msg.Avatar
	}
	matterMessage.Props["matterbridge_"+b.uuid] = true

	var ref string
	if b.mc != nil {
		ref = xid.New().String()
		matterMessage.Props[webhookRefProp] = ref
	}

	since := time.Now().Add(-time.Minute).UnixMilli()

	err := b.mh.Send(matterMessage)
	if err != nil && matterMessage.RootID != "" {
		// e.g. the thread was deleted meanwhile: don't lose the message.
		b.Log.Errorf("sending thread reply through webhook failed, sending unthreaded: %s", err)

		matterMessage.RootID = ""
		matterMessage.Text = "[thread]: " + text
		err = b.mh.Send(matterMessage)
	}
	if err != nil {
		b.Log.Info(err)
		return "", err
	}

	if ref == "" {
		return "", nil
	}

	return b.findWebhookPost(msg.Channel, ref, since), nil
}

// sendWebhookExtra sends the notices about files that were too big to download.
func (b *Bmattermost) sendWebhookExtra(msg *config.Message) {
	if msg.Extra == nil {
		return
	}

	for _, rmsg := range helper.HandleExtra(msg, b.General) {
		iconURL := config.GetIconURL(&rmsg, b.GetString("iconurl"))
		matterMessage := matterhook.OMessage{
			IconURL:  iconURL,
			Channel:  rmsg.Channel,
			UserName: rmsg.Username,
			Text:     rmsg.Text,
			RootID:   msg.ParentID,
			Props:    make(map[string]any),
		}
		matterMessage.Props["matterbridge_"+b.uuid] = true

		err := b.mh.Send(matterMessage)
		if err != nil {
			b.Log.Errorf("sendWebhook failed: %s ", err)
		}
	}
}

// findWebhookPost returns the ID of the post we sent through the webhook,
// tagged with ref, in channel (or "" if it can't be found).
func (b *Bmattermost) findWebhookPost(channel, ref string, since int64) string {
	channelID := b.getChannelID(channel)
	if channelID == "" {
		b.Log.Warnf("Could not find channel ID for channel %s: the bridged message can't be threaded or edited", channel)
		return ""
	}

	const attempts = 3
	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}

		posts, _, err := b.mc.Client.GetPostsSince(context.TODO(), channelID, since, false)
		if err != nil {
			b.Log.Errorf("getting posts of channel %s failed: %s", channel, err)
			continue
		}

		for _, post := range posts.Posts {
			if r, ok := post.GetProp(webhookRefProp).(string); ok && r == ref {
				return post.Id
			}
		}
	}

	b.Log.Warnf("Could not find the post sent through the webhook: it can't be threaded or edited")

	return ""
}

// editPost replaces the message of a post. PatchPost only changes the
// message and keeps the props of the post (e.g. the username and icon
// overrides of posts made through the webhook).
func (b *Bmattermost) editPost(id, text string) (string, error) {
	post, _, err := b.mc.Client.PatchPost(context.TODO(), id, &model.PostPatch{Message: &text})
	if err != nil {
		return "", err
	}

	return post.Id, nil
}

// fileComments returns the text attached to the files of a message. Some
// bridges (e.g. Slack) move the text of a message to the comment of its first
// file, others (e.g. Mattermost) copy it to the comment of every file, so
// duplicates are only kept once.
func fileComments(msg *config.Message) string {
	var comments []string

	seen := make(map[string]bool)

	for _, f := range msg.Extra["file"] {
		fi, ok := f.(config.FileInfo)
		if !ok || fi.Comment == "" || seen[fi.Comment] {
			continue
		}

		seen[fi.Comment] = true
		comments = append(comments, fi.Comment)
	}

	return strings.Join(comments, "\n")
}

// markdownBlockStart matches text starting with a block-level markdown element
// (code fence, quote, heading, list item, table) which only renders at the
// beginning of a line.
var markdownBlockStart = regexp.MustCompile("^(```|~~~|>|#{1,6} |[-*+] |[0-9]+[.)] |\\|)")

// prefixNick prepends the nick to the text when PrefixMessagesWithNick is set.
func (b *Bmattermost) prefixNick(nick, text string) string {
	if !b.GetBool("PrefixMessagesWithNick") {
		return text
	}

	return prefixNick(nick, text)
}

// prefixNick prepends nick to text. If the text starts with a block-level
// markdown element, the text is moved to its own line so that it still renders
// (e.g. "nick: ```code```" would break the code fence).
func prefixNick(nick, text string) string {
	if nick == "" {
		return text
	}

	if markdownBlockStart.MatchString(text) {
		return strings.TrimRight(nick, " ") + "\n" + text
	}

	return nick + text
}

// skipMessages returns true if this message should not be handled
//
//nolint:gocyclo,cyclop
func (b *Bmattermost) skipMessage(message *matterclient.Message) bool {
	// Handle join/leave
	skipJoinMessageTypes := map[string]struct{}{
		"system_add_to_channel":      {},
		"system_remove_from_channel": {},
		"system_add_to_team":         {},
		"system_remove_from_team":    {},
	}

	// dirty hack to efficiently check if this element is in the map without writing a contains func
	// can be replaced with native slice.contains with go 1.21
	if _, ok := skipJoinMessageTypes[message.Type]; ok {
		if b.GetBool("nosendjoinpart") {
			return true
		}

		channelName := b.getChannelName(message.Post.ChannelId)
		if channelName == "" {
			channelName = message.Channel
		}

		switch message.Type {
		case "system_add_to_channel":
			b.Log.Debugf("Sending JOIN event from %s to gateway", b.Account)

			b.Remote <- config.Message{
				Username: systemUsername,
				Text:     message.Text,
				Channel:  channelName,
				Account:  b.Account,
				Event:    config.EventJoin,
			}
		case "system_remove_from_channel":
			b.Log.Debugf("Sending LEAVE event from %s to gateway", b.Account)

			b.Remote <- config.Message{
				Username: systemUsername,
				Text:     message.Text,
				Channel:  channelName,
				Account:  b.Account,
				Event:    config.EventLeave,
			}
		}
		return true
	}

	// Handle edited messages
	if (message.Raw.EventType() == model.WebsocketEventPostEdited) && b.GetBool("EditDisable") {
		return true
	}

	// Ignore non-post messages
	if message.Post == nil {
		b.Log.Debugf("ignoring nil message.Post: %#v", message)
		return true
	}

	// Ignore messages sent from matterbridge
	if message.Post.Props != nil {
		if _, ok := message.Post.Props["matterbridge_"+b.uuid].(bool); ok {
			b.Log.Debug("sent by matterbridge, ignoring")
			return true
		}
	}

	// Ignore messages sent from a user logged in as the bot
	if b.mc.User.Username == message.Username {
		b.Log.Debug("message from same user as bot, ignoring")
		return true
	}

	// Reactions are received as reaction_added/reaction_removed events. Some
	// Mattermost versions also sent post_edited when a reaction was added:
	// ignore post_edited events of posts that were never edited. (Skipping all
	// posts having reactions dropped the real edits and deletes of those.)
	if message.Raw.EventType() == model.WebsocketEventPostEdited && message.Post.EditAt == 0 {
		return true
	}

	// ignore messages from other teams than ours
	if message.Raw.GetData()["team_id"].(string) != b.TeamID {
		b.Log.Debug("message from other team, ignoring")
		return true
	}

	// only handle posted, edited or deleted events
	if !(message.Raw.EventType() == "posted" || message.Raw.EventType() == model.WebsocketEventPostEdited ||
		message.Raw.EventType() == model.WebsocketEventPostDeleted) {
		return true
	}
	return false
}

func (b *Bmattermost) getVersion() string {
	proto := "https"

	if b.GetBool("notls") {
		proto = "http"
	}

	resp, err := http.Get(proto + "://" + b.GetString("server"))
	if err != nil {
		b.Log.Error("failed getting version")
		return ""
	}

	defer resp.Body.Close()

	return resp.Header.Get("X-Version-Id")
}

func (b *Bmattermost) getChannelID(name string) string {
	idcheck := strings.Split(name, "ID:")
	if len(idcheck) > 1 {
		return idcheck[1]
	}

	return b.mc.GetChannelID(name, b.TeamID)
}

func (b *Bmattermost) getChannelName(id string) string {
	b.channelsMutex.RLock()
	defer b.channelsMutex.RUnlock()

	for _, c := range b.channelInfoMap {
		if c.Name == "ID:"+id {
			// if we have ID: specified in our gateway configuration return this
			return c.Name
		}
	}

	return ""
}
