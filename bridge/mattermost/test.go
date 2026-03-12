package bmattermost

import (
	"context"
	"strings"
	"time"

	"github.com/matterbridge-org/matterbridge/testdata"
	"github.com/mattermost/mattermost/server/public/model"
)

// isTestCommand returns true if the message text is exactly "@matterbridge test".
func (b *Bmattermost) isTestCommand(text string) bool {
	return strings.TrimSpace(strings.ToLower(text)) == "@matterbridge test"
}

// runTestSequence posts a series of test messages to the given channel.
// The messages are posted via the API with a special "matterbridge_test" prop
// so that skipMessage() allows them through for relay to the other bridge side.
func (b *Bmattermost) runTestSequence(channelName string) {
	channelID := b.getChannelID(channelName)
	if channelID == "" {
		b.Log.Errorf("test: could not resolve channel ID for %s", channelName)
		return
	}

	b.Log.Infof("test: starting test sequence in channel %s", channelName)

	testProps := model.StringInterface{"matterbridge_test": true}

	// Helper to post a message and return the post ID.
	post := func(message, rootID string) string {
		p := &model.Post{
			ChannelId: channelID,
			Message:   message,
			RootId:    rootID,
			Props:     testProps,
		}
		created, _, err := b.mc.Client.CreatePost(context.TODO(), p)
		if err != nil {
			b.Log.Errorf("test: CreatePost failed: %s", err)
			return ""
		}
		return created.Id
	}

	// Step 1: Root message
	rootID := post("🧪 **Matterbridge Test Sequence**\nThis is a root message to test the bridge relay.", "")
	if rootID == "" {
		return
	}
	time.Sleep(time.Second)

	// Step 2: Thread reply
	post("This is a thread reply to test threading support.", rootID)
	time.Sleep(time.Second)

	// Step 3: Typo message (will be edited later)
	typoID := post("this message contains a tipo", rootID)
	time.Sleep(time.Second)

	// Step 4: Code block
	post("```python\ndef hello():\n    for i in range(3):\n        print(f\"Hello from Matterbridge! ({i+1})\")\n\nhello()\n```", rootID)
	time.Sleep(time.Second)

	// Step 5: Message to be deleted
	deleteID := post("this message will be deleted", rootID)
	time.Sleep(time.Second)

	// Step 6: Quote block
	post("> This is a quoted line.\n> Matterbridge supports quote blocks.\n> Third line of the quote.", rootID)
	time.Sleep(time.Second)

	// Step 7: Emojis
	post(":thumbsup: :tada: :rocket: :heart: :eyes: :flag-at:", rootID)
	time.Sleep(time.Second)

	// Step 8: Edit the typo message
	if typoID != "" {
		newText := "this message contained a typo"
		_, _, err := b.mc.Client.PatchPost(context.TODO(), typoID, &model.PostPatch{Message: &newText})
		if err != nil {
			b.Log.Errorf("test: PatchPost failed: %s", err)
		}
	}
	time.Sleep(time.Second)

	// Step 9: Text formatting demo
	post("**This text is bold**\n*This text is italic*\n~~This text is strikethrough~~\n### This is a heading\n[This is a link](https://github.com/matterbridge-org/matterbridge)", rootID)
	time.Sleep(time.Second)

	// Step 10: Unordered list
	post("- Item one\n- Item two\n- Item three", rootID)
	time.Sleep(time.Second)

	// Step 11: Ordered list
	post("1. First point\n2. Second point\n3. Third point", rootID)
	time.Sleep(time.Second)

	// Step 12: Single PNG image
	if pngID, err := b.mc.UploadFile(testdata.DemoPNG, channelID, "demo.png"); err != nil {
		b.Log.Errorf("test: upload demo.png failed: %s", err)
	} else {
		p := &model.Post{ChannelId: channelID, Message: "Image test: PNG", RootId: rootID, FileIds: model.StringArray{pngID}, Props: testProps}
		if _, _, err := b.mc.Client.CreatePost(context.TODO(), p); err != nil {
			b.Log.Errorf("test: CreatePost with PNG failed: %s", err)
		}
	}
	time.Sleep(time.Second)

	// Step 13: Single GIF image
	if gifID, err := b.mc.UploadFile(testdata.DemoGIF, channelID, "demo.gif"); err != nil {
		b.Log.Errorf("test: upload demo.gif failed: %s", err)
	} else {
		p := &model.Post{ChannelId: channelID, Message: "Image test: GIF", RootId: rootID, FileIds: model.StringArray{gifID}, Props: testProps}
		if _, _, err := b.mc.Client.CreatePost(context.TODO(), p); err != nil {
			b.Log.Errorf("test: CreatePost with GIF failed: %s", err)
		}
	}
	time.Sleep(time.Second)

	// Step 14: Multi-image (2x PNG in one message)
	{
		var fileIDs model.StringArray
		for _, name := range []string{"demo1.png", "demo2.png"} {
			id, err := b.mc.UploadFile(testdata.DemoPNG, channelID, name)
			if err != nil {
				b.Log.Errorf("test: upload %s failed: %s", name, err)
				continue
			}
			fileIDs = append(fileIDs, id)
		}
		if len(fileIDs) > 0 {
			p := &model.Post{ChannelId: channelID, Message: "Image test: multi-image (2x PNG)", RootId: rootID, FileIds: fileIDs, Props: testProps}
			if _, _, err := b.mc.Client.CreatePost(context.TODO(), p); err != nil {
				b.Log.Errorf("test: CreatePost with multi-image failed: %s", err)
			}
		}
	}
	time.Sleep(time.Second)

	// Step 15: Important priority message
	// Create post first, then set priority via separate API call.
	// (Metadata in CreatePost is ignored by the server.)
	{
		p := &model.Post{
			ChannelId: channelID,
			Message:   "Priority test: important message",
			RootId:    rootID,
			Props:     testProps,
		}
		created, _, err := b.mc.Client.CreatePost(context.TODO(), p)
		if err != nil {
			b.Log.Errorf("test: CreatePost important priority failed: %s", err)
		} else {
			b.Log.Debugf("test: created important priority post %s, setting priority...", created.Id)
			prio := "important"
			b.mc.Client.SetPostPriority(context.TODO(), created.Id, &model.PostPriority{Priority: &prio})
		}
	}
	time.Sleep(time.Second)

	// Step 16: Urgent priority message
	{
		p := &model.Post{
			ChannelId: channelID,
			Message:   "Priority test: urgent message",
			RootId:    rootID,
			Props:     testProps,
		}
		created, _, err := b.mc.Client.CreatePost(context.TODO(), p)
		if err != nil {
			b.Log.Errorf("test: CreatePost urgent priority failed: %s", err)
		} else {
			b.Log.Debugf("test: created urgent priority post %s, setting priority...", created.Id)
			prio := "urgent"
			b.mc.Client.SetPostPriority(context.TODO(), created.Id, &model.PostPriority{Priority: &prio})
		}
	}
	time.Sleep(time.Second)

	// Step 17: Delete the marked message
	if deleteID != "" {
		_, err := b.mc.Client.DeletePost(context.TODO(), deleteID)
		if err != nil {
			b.Log.Errorf("test: DeletePost failed: %s", err)
		}
	}

	// Step 18: Test finished
	post("✅ Test finished", rootID)

	b.Log.Info("test: test sequence completed")
}
