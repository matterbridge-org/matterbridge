//go:build e2e

// End-to-end test of the Mattermost bridge against a real Mattermost server.
//
// It feeds the bridge the messages the gateway hands over for Slack messages
// (thread replies, edits, images with a caption) and checks the resulting
// posts through the Mattermost API.
//
//	MM_E2E_SERVER=localhost:8065 MM_E2E_TEAM=test MM_E2E_CHANNEL=bridge \
//	MM_E2E_TOKEN=<bot token> [MM_E2E_WEBHOOK=<incoming webhook url>] \
//	go test -tags 'goolm e2e' -run E2E -v ./bridge/mattermost/
package bmattermost

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const e2eNick = "**alice** (slack): "

func e2eBridge(t *testing.T, extra string) *Bmattermost {
	t.Helper()

	server, team, token := os.Getenv("MM_E2E_SERVER"), os.Getenv("MM_E2E_TEAM"), os.Getenv("MM_E2E_TOKEN")
	if server == "" || team == "" || token == "" {
		t.Skip("MM_E2E_SERVER, MM_E2E_TEAM and MM_E2E_TOKEN must be set")
	}

	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	cfg := config.NewConfigFromString(logger, []byte(fmt.Sprintf(`
[mattermost.e2e]
Server=%q
Team=%q
Token=%q
NoTLS=true
SkipVersionCheck=true
PreserveThreading=true
%s
`, server, team, token, extra)))

	br := bridge.New(&config.Bridge{Account: "mattermost.e2e"})
	br.Config = cfg
	br.Log = logger.WithField("prefix", "mattermost")
	br.General = &config.Protocol{}

	b := New(&bridge.Config{Bridge: br, Remote: make(chan config.Message, 100)}).(*Bmattermost)
	require.NoError(t, b.Connect())
	require.NoError(t, b.JoinChannel(config.ChannelInfo{Name: os.Getenv("MM_E2E_CHANNEL"), ID: "e2e"}))

	return b
}

func pngData(t *testing.T) *[]byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := range 8 {
		img.Set(x, x, color.RGBA{R: 255, A: 255})
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	data := buf.Bytes()

	return &data
}

func file(t *testing.T, name, comment string) config.FileInfo {
	return config.FileInfo{Name: name, Data: pngData(t), Comment: comment, URL: "https://media.example/" + name}
}

func TestE2EMattermostAPI(t *testing.T) {
	b := e2eBridge(t, "PrefixMessagesWithNick=true")
	channel := os.Getenv("MM_E2E_CHANNEL")
	channelID := b.getChannelID(channel)
	require.NotEmpty(t, channelID)

	ctx := context.Background()
	getPost := func(id string) *model.Post {
		t.Helper()
		post, _, err := b.mc.Client.GetPost(ctx, id, "")
		require.NoError(t, err)
		return post
	}
	countPosts := func() int {
		t.Helper()
		posts, _, err := b.mc.Client.GetPostsForChannel(ctx, channelID, 0, 200, "", false, false)
		require.NoError(t, err)
		return len(posts.Order)
	}
	send := func(msg config.Message) string {
		t.Helper()
		msg.Channel = channel
		msg.Account = "slack.e2e" // set by the gateway
		if msg.Username == "" {
			msg.Username = e2eNick
		}
		id, err := b.Send(msg)
		require.NoError(t, err)
		return id
	}

	// A plain message returns the post ID, so the gateway can map threads and edits.
	rootID := send(config.Message{Text: "root message"})
	require.NotEmpty(t, rootID, "Send must return the post ID")
	assert.Equal(t, e2eNick+"root message", getPost(rootID).Message)

	t.Run("thread reply", func(t *testing.T) {
		replyID := send(config.Message{Text: "reply", ParentID: rootID})
		post := getPost(replyID)
		assert.Equal(t, rootID, post.RootId)
		assert.Equal(t, e2eNick+"reply", post.Message)

		// replying to a reply ends up in the same thread
		reply2ID := send(config.Message{Text: "reply to reply", ParentID: replyID})
		assert.Equal(t, rootID, getPost(reply2ID).RootId)
	})

	t.Run("parent not found", func(t *testing.T) {
		id := send(config.Message{Text: "orphan", ParentID: config.ParentIDNotFound})
		post := getPost(id)
		assert.Empty(t, post.RootId)
		assert.Equal(t, e2eNick+"[thread]: orphan", post.Message)
	})

	t.Run("foreign parent ID is not fatal", func(t *testing.T) {
		id := send(config.Message{Text: "foreign parent", ParentID: "1731700000.000100"})
		post := getPost(id)
		assert.Empty(t, post.RootId)
		assert.Equal(t, e2eNick+"[thread]: foreign parent", post.Message)
	})

	t.Run("edit updates in place", func(t *testing.T) {
		before := countPosts()
		id := send(config.Message{ID: rootID, Text: "root message (edited)"})
		assert.Equal(t, rootID, id)
		assert.Equal(t, e2eNick+"root message (edited)", getPost(rootID).Message)
		assert.Equal(t, before, countPosts(), "an edit must not create a new post")
	})

	var imageID string
	t.Run("image with caption (slack style)", func(t *testing.T) {
		// Slack moves the text into the comment of the first file.
		imageID = send(config.Message{Extra: map[string][]any{"file": {file(t, "pic.png", "look at this")}}})
		require.NotEmpty(t, imageID)
		post := getPost(imageID)
		assert.Equal(t, e2eNick+"look at this", post.Message)
		assert.Len(t, post.FileIds, 1)
	})

	t.Run("edit image caption", func(t *testing.T) {
		before := countPosts()
		// Slack edits of a message with a file carry the file again.
		id := send(config.Message{ID: imageID, Extra: map[string][]any{"file": {file(t, "pic.png", "look at this (edited)")}}})
		assert.Equal(t, imageID, id)
		post := getPost(imageID)
		assert.Equal(t, e2eNick+"look at this (edited)", post.Message)
		assert.Len(t, post.FileIds, 1, "files must be kept on edit")
		assert.Equal(t, before, countPosts(), "an edit must not re-upload the file as a new post")
	})

	t.Run("several images, one post", func(t *testing.T) {
		before := countPosts()
		id := send(config.Message{Extra: map[string][]any{"file": {
			file(t, "a.png", "three pictures"), file(t, "b.png", ""), file(t, "c.png", ""),
		}}})
		post := getPost(id)
		assert.Equal(t, e2eNick+"three pictures", post.Message)
		assert.Len(t, post.FileIds, 3)
		assert.Equal(t, before+1, countPosts())
	})

	t.Run("image in thread", func(t *testing.T) {
		id := send(config.Message{ParentID: rootID, Extra: map[string][]any{"file": {file(t, "t.png", "threaded pic")}}})
		post := getPost(id)
		assert.Equal(t, rootID, post.RootId)
		assert.Equal(t, e2eNick+"threaded pic", post.Message)
		assert.Len(t, post.FileIds, 1)
	})

	t.Run("text and image from mattermost style source", func(t *testing.T) {
		// Mattermost copies the text in every file comment and keeps msg.Text.
		id := send(config.Message{Text: "both", Extra: map[string][]any{"file": {file(t, "m1.png", "both"), file(t, "m2.png", "both")}}})
		post := getPost(id)
		assert.Equal(t, e2eNick+"both", post.Message)
		assert.Len(t, post.FileIds, 2)
	})

	t.Run("code block keeps rendering", func(t *testing.T) {
		id := send(config.Message{Text: "```\ncode\n```"})
		assert.Equal(t, strings.TrimRight(e2eNick, " ")+"\n```\ncode\n```", getPost(id).Message)
	})

	t.Run("delete", func(t *testing.T) {
		id := send(config.Message{Text: "to be deleted"})
		_, err := b.Send(config.Message{Channel: channel, ID: id, Event: config.EventMsgDelete})
		require.NoError(t, err)
		_, _, err = b.mc.Client.GetPost(ctx, id, "")
		assert.Error(t, err, "post should be deleted")
	})
}

// Webhook mode with a Token: posts keep the remote user's name and avatar,
// while the API is used for threads, edits and deletes.
func TestE2EMattermostWebhook(t *testing.T) {
	webhook := os.Getenv("MM_E2E_WEBHOOK")
	if webhook == "" {
		t.Skip("MM_E2E_WEBHOOK not set")
	}
	b := e2eBridge(t, fmt.Sprintf("WebhookURL=%q", webhook))
	channel := os.Getenv("MM_E2E_CHANNEL")
	channelID := b.getChannelID(channel)
	require.NotEmpty(t, channelID)

	ctx := context.Background()
	getPost := func(id string) *model.Post {
		t.Helper()
		post, _, err := b.mc.Client.GetPost(ctx, id, "")
		require.NoError(t, err)
		return post
	}
	countPosts := func() int {
		t.Helper()
		posts, _, err := b.mc.Client.GetPostsForChannel(ctx, channelID, 0, 200, "", false, false)
		require.NoError(t, err)
		return len(posts.Order)
	}
	send := func(msg config.Message) string {
		t.Helper()
		msg.Channel = channel
		msg.Account = "slack.e2e" // set by the gateway
		msg.Username = "alice"
		id, err := b.Send(msg)
		require.NoError(t, err)
		return id
	}
	assertIdentity := func(post *model.Post) {
		t.Helper()
		assert.Equal(t, "alice", post.GetProp(model.PostPropsOverrideUsername))
		assert.Equal(t, "true", post.GetProp(model.PostPropsFromWebhook))
	}

	rootID := send(config.Message{Text: "webhook root"})
	require.NotEmpty(t, rootID, "the ID of the webhook post must be found back")
	root := getPost(rootID)
	assert.Equal(t, "webhook root", root.Message)
	assertIdentity(root)

	t.Run("thread reply", func(t *testing.T) {
		replyID := send(config.Message{Text: "webhook reply", ParentID: rootID})
		require.NotEmpty(t, replyID)
		post := getPost(replyID)
		assert.Equal(t, rootID, post.RootId)
		assertIdentity(post)

		// replying to a reply ends up in the same thread
		reply2ID := send(config.Message{Text: "webhook reply 2", ParentID: replyID})
		assert.Equal(t, rootID, getPost(reply2ID).RootId)
	})

	t.Run("foreign parent ID is not fatal", func(t *testing.T) {
		id := send(config.Message{Text: "foreign parent", ParentID: "1731700000.000100"})
		post := getPost(id)
		assert.Empty(t, post.RootId)
		assert.Equal(t, "[thread]: foreign parent", post.Message)
	})

	t.Run("edit updates in place and keeps identity", func(t *testing.T) {
		before := countPosts()
		id, err := b.Send(config.Message{Channel: channel, Account: "slack.e2e", Username: "alice", ID: rootID, Text: "webhook root (edited)"})
		require.NoError(t, err)
		assert.Equal(t, rootID, id)
		post := getPost(rootID)
		assert.Equal(t, "webhook root (edited)", post.Message)
		assertIdentity(post)
		assert.Equal(t, before, countPosts(), "an edit must not create a new post")
	})

	var imageID string
	t.Run("image with caption (slack style)", func(t *testing.T) {
		imageID = send(config.Message{Extra: map[string][]any{"file": {file(t, "hook.png", "webhook caption")}}})
		require.NotEmpty(t, imageID)
		post := getPost(imageID)
		assert.Equal(t, "webhook caption https://media.example/hook.png", post.Message)
		assertIdentity(post)
	})

	t.Run("edit image caption", func(t *testing.T) {
		before := countPosts()
		_, err := b.Send(config.Message{Channel: channel, Account: "slack.e2e", Username: "alice", ID: imageID,
			Extra: map[string][]any{"file": {file(t, "hook.png", "webhook caption (edited)")}}})
		require.NoError(t, err)
		assert.Equal(t, "webhook caption (edited) https://media.example/hook.png", getPost(imageID).Message)
		assert.Equal(t, before, countPosts())
	})

	t.Run("delete", func(t *testing.T) {
		id := send(config.Message{Text: "to be deleted"})
		_, err := b.Send(config.Message{Channel: channel, ID: id, Event: config.EventMsgDelete})
		require.NoError(t, err)
		_, _, err = b.mc.Client.GetPost(ctx, id, "")
		assert.Error(t, err, "post should be deleted")
	})
}

// Events that are not messages must not be turned into edits or posts.
func TestE2EMattermostEvents(t *testing.T) {
	webhook := os.Getenv("MM_E2E_WEBHOOK")
	if webhook == "" {
		t.Skip("MM_E2E_WEBHOOK not set")
	}
	b := e2eBridge(t, fmt.Sprintf("WebhookURL=%q", webhook))
	channel := os.Getenv("MM_E2E_CHANNEL")

	for _, event := range []string{config.EventFileDelete, config.EventJoin, config.EventTopicChange} {
		id, err := b.Send(config.Message{Channel: channel, Account: "slack.e2e", Username: "alice",
			Text: "event", ID: "F0CJ7J8NZDF", Event: event})
		assert.NoError(t, err, event)
		assert.Empty(t, id, event)
	}
}

func adminClient(t *testing.T) *model.Client4 {
	t.Helper()

	token := os.Getenv("MM_E2E_ADMIN_TOKEN")
	if token == "" {
		t.Skip("MM_E2E_ADMIN_TOKEN not set")
	}
	c := model.NewAPIv4Client("http://" + os.Getenv("MM_E2E_SERVER"))
	c.SetToken(token)

	return c
}

// waitRemote returns the next message the bridge sends to the gateway
// matching match, or fails after a timeout.
func waitRemote(t *testing.T, b *Bmattermost, match func(config.Message) bool) config.Message {
	t.Helper()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-b.Remote:
			if match(msg) {
				return msg
			}
		case <-timeout:
			t.Fatal("timeout waiting for message to the gateway")
			return config.Message{}
		}
	}
}

func TestE2EMattermostReactions(t *testing.T) {
	admin := adminClient(t)
	b := e2eBridge(t, "PreserveReactions=true")
	channel := os.Getenv("MM_E2E_CHANNEL")
	channelID := b.getChannelID(channel)
	ctx := context.Background()
	me, _, err := admin.GetMe(ctx, "")
	require.NoError(t, err)

	reactions := func(postID string) []string {
		t.Helper()
		rs, _, err := admin.GetReactions(ctx, postID)
		require.NoError(t, err)
		var names []string
		for _, r := range rs {
			names = append(names, r.UserId+":"+r.EmojiName)
		}
		return names
	}
	react := func(event, postID, emoji, userID string) {
		t.Helper()
		_, err := b.Send(config.Message{Event: event, ID: postID, Text: emoji, Channel: channel,
			Account: "slack.e2e", UserID: userID, Username: userID})
		require.NoError(t, err)
	}

	post, _, err := admin.CreatePost(ctx, &model.Post{ChannelId: channelID, Message: "react to me"})
	require.NoError(t, err)
	bot := b.mc.User.Id

	t.Run("send: one reaction for several remote users", func(t *testing.T) {
		react(config.EventReactionAdd, post.Id, "+1", "U1")
		react(config.EventReactionAdd, post.Id, "+1", "U2")
		assert.Equal(t, []string{bot + ":+1"}, reactions(post.Id))

		react(config.EventReactionRemove, post.Id, "+1", "U1")
		assert.Equal(t, []string{bot + ":+1"}, reactions(post.Id), "U2 still reacts")

		react(config.EventReactionRemove, post.Id, "+1", "U2")
		assert.Empty(t, reactions(post.Id))
	})

	t.Run("send: skin tones", func(t *testing.T) {
		react(config.EventReactionAdd, post.Id, "wave::skin-tone-3", "U1")
		assert.Equal(t, []string{bot + ":wave_medium_light_skin_tone"}, reactions(post.Id))
		react(config.EventReactionRemove, post.Id, "wave::skin-tone-3", "U1")
		assert.Empty(t, reactions(post.Id))
	})

	t.Run("send: unknown emoji is an error, not a crash", func(t *testing.T) {
		_, err := b.Send(config.Message{Event: config.EventReactionAdd, ID: post.Id, Text: "not_an_emoji_xyz",
			Channel: channel, Account: "slack.e2e", UserID: "U9"})
		assert.Error(t, err)
	})

	// drain what the bridge received so far
	for len(b.Remote) > 0 {
		<-b.Remote
	}

	t.Run("receive: user reactions are relayed", func(t *testing.T) {
		_, _, err := admin.SaveReaction(ctx, &model.Reaction{UserId: me.Id, PostId: post.Id, EmojiName: "thumbsup_medium_skin_tone"})
		require.NoError(t, err)
		msg := waitRemote(t, b, func(m config.Message) bool { return m.IsReaction() })
		assert.Equal(t, config.EventReactionAdd, msg.Event)
		assert.Equal(t, post.Id, msg.ID)
		assert.Equal(t, "thumbsup::skin-tone-4", msg.Text)
		assert.Equal(t, channel, msg.Channel)
		assert.Equal(t, me.Id, msg.UserID)
		assert.Equal(t, "e2eadmin", msg.Username)

		_, err = admin.DeleteReaction(ctx, &model.Reaction{UserId: me.Id, PostId: post.Id, EmojiName: "thumbsup_medium_skin_tone"})
		require.NoError(t, err)
		msg = waitRemote(t, b, func(m config.Message) bool { return m.IsReaction() })
		assert.Equal(t, config.EventReactionRemove, msg.Event)
		assert.Equal(t, channel, msg.Channel, "channel is found on removal too")
	})

	t.Run("receive: our own reactions are not relayed back", func(t *testing.T) {
		react(config.EventReactionAdd, post.Id, "heart", "U1")
		// a user reaction after ours: it must be the first one we get
		_, _, err := admin.SaveReaction(ctx, &model.Reaction{UserId: me.Id, PostId: post.Id, EmojiName: "smile"})
		require.NoError(t, err)
		msg := waitRemote(t, b, func(m config.Message) bool { return m.IsReaction() })
		assert.Equal(t, "smile", msg.Text)
	})

	t.Run("receive: edits of posts with reactions are relayed", func(t *testing.T) {
		text := "react to me (edited)"
		_, _, err := admin.PatchPost(ctx, post.Id, &model.PostPatch{Message: &text})
		require.NoError(t, err)
		msg := waitRemote(t, b, func(m config.Message) bool { return m.ID == post.Id && !m.IsReaction() })
		assert.Equal(t, text, msg.Text)
	})
}
