package bmsteams

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/matterbridge-org/matterbridge/testdata"
	msgraph "github.com/yaegashi/msgraph.go/beta"
)

// isTestCommand returns true if the message text is exactly "@matterbridge test".
func (b *Bmsteams) isTestCommand(text string) bool {
	return strings.TrimSpace(strings.ToLower(text)) == "@matterbridge test"
}

// runTestSequence posts a series of test messages to the given channel.
// Messages are posted via the Graph API but NOT added to sentIDs/updatedIDs,
// so the poll loop picks them up and relays them to the other bridge side.
func (b *Bmsteams) runTestSequence(channelName string) {
	teamID := b.GetString("TeamID")
	channelID := decodeChannelID(channelName)

	b.Log.Infof("test: starting test sequence in channel %s", channelName)

	// Helper to post a top-level message and return its ID.
	postRoot := func(text string, contentType *msgraph.BodyType) string {
		ct := b.gc.Teams().ID(teamID).Channels().ID(channelID).Messages().Request()
		content := &msgraph.ItemBody{Content: &text}
		if contentType != nil {
			content.ContentType = contentType
		}
		res, err := ct.Add(b.ctx, &msgraph.ChatMessage{Body: content})
		if err != nil {
			b.Log.Errorf("test: post root failed: %s", err)
			return ""
		}
		// Do NOT add to sentIDs — let poll() pick it up for relay.
		return *res.ID
	}

	// Helper to post a reply and return its ID.
	postReply := func(rootID, text string, contentType *msgraph.BodyType) string {
		ct := b.gc.Teams().ID(teamID).Channels().ID(channelID).Messages().ID(rootID).Replies().Request()
		content := &msgraph.ItemBody{Content: &text}
		if contentType != nil {
			content.ContentType = contentType
		}
		res, err := ct.Add(b.ctx, &msgraph.ChatMessage{Body: content})
		if err != nil {
			b.Log.Errorf("test: post reply failed: %s", err)
			return ""
		}
		// Do NOT add to sentIDs — let poll() pick it up for relay.
		return *res.ID
	}

	// Helper to edit a reply without adding to updatedIDs.
	editReply := func(rootID, replyID, newText string) {
		type patchBody struct {
			Body struct {
				ContentType string `json:"contentType"`
				Content     string `json:"content"`
			} `json:"body"`
		}
		var patch patchBody
		patch.Body.ContentType = "text"
		patch.Body.Content = newText

		jsonData, err := json.Marshal(patch)
		if err != nil {
			b.Log.Errorf("test: marshal failed: %s", err)
			return
		}

		url := fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies/%s",
			teamID, channelID, rootID, replyID)

		token, err := b.getAccessToken()
		if err != nil {
			b.Log.Errorf("test: getAccessToken failed: %s", err)
			return
		}

		req, err := http.NewRequestWithContext(b.ctx, http.MethodPatch, url, bytes.NewReader(jsonData))
		if err != nil {
			b.Log.Errorf("test: NewRequest failed: %s", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Log.Errorf("test: PATCH failed: %s", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			b.Log.Errorf("test: edit reply failed: %d %s", resp.StatusCode, string(body))
		}
		// Do NOT add to updatedIDs — let poll() pick up the edit for relay.
	}

	// Helper to soft-delete a reply without adding to updatedIDs.
	deleteReply := func(rootID, replyID string) {
		url := fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies/%s/softDelete",
			teamID, channelID, rootID, replyID)

		token, err := b.getAccessToken()
		if err != nil {
			b.Log.Errorf("test: getAccessToken failed: %s", err)
			return
		}

		req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, url, nil)
		if err != nil {
			b.Log.Errorf("test: NewRequest failed: %s", err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Log.Errorf("test: softDelete failed: %s", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			b.Log.Errorf("test: delete reply failed: %d %s", resp.StatusCode, string(body))
		}
		// Do NOT add to updatedIDs — let poll() pick up the delete for relay.
	}

	htmlType := msgraph.BodyTypeVHTML

	// Step 1: Root message
	rootID := postRoot("🧪 <b>Matterbridge Test Sequence</b><br>This is a root message to test the bridge relay.", &htmlType)
	if rootID == "" {
		return
	}
	time.Sleep(time.Second)

	// Step 2: Thread reply
	postReply(rootID, "This is a thread reply to test threading support.", nil)
	time.Sleep(time.Second)

	// Step 3: Typo message (will be edited later)
	typoID := postReply(rootID, "this message contains a tipo", nil)
	time.Sleep(time.Second)

	// Step 4: Code block
	codeHTML := `<codeblock class="python"><code>def hello():<br>    for i in range(3):<br>        print(f"Hello from Matterbridge! ({i+1})")<br><br>hello()</code></codeblock>`
	postReply(rootID, codeHTML, &htmlType)
	time.Sleep(time.Second)

	// Step 5: Message to be deleted
	deleteID := postReply(rootID, "this message will be deleted", nil)
	time.Sleep(time.Second)

	// Step 6: Quote block
	postReply(rootID, "<blockquote>This is a quoted line.<br>Matterbridge supports quote blocks.<br>Third line of the quote.</blockquote>", &htmlType)
	time.Sleep(time.Second)

	// Step 7: Emojis
	postReply(rootID, "👍 🎉 🚀 ❤️ 👀 🇦🇹", nil)
	time.Sleep(time.Second)

	// Step 8: Edit the typo message
	if typoID != "" {
		editReply(rootID, typoID, "this message contained a typo")
	}
	time.Sleep(time.Second)

	// Step 9: Text formatting demo
	formattingHTML := `<b>This text is bold</b><br>` +
		`<i>This text is italic</i><br>` +
		`<s>This text is strikethrough</s><br>` +
		`<h3>This is a heading</h3>` +
		`<a href="https://github.com/matterbridge-org/matterbridge">This is a link</a>`
	postReply(rootID, formattingHTML, &htmlType)
	time.Sleep(time.Second)

	// Step 10: Unordered list
	postReply(rootID, "<ul><li>Item one</li><li>Item two</li><li>Item three</li></ul>", &htmlType)
	time.Sleep(time.Second)

	// Step 11: Ordered list
	postReply(rootID, "<ol><li>First point</li><li>Second point</li><li>Third point</li></ol>", &htmlType)
	time.Sleep(time.Second)

	type testImage struct {
		name        string
		contentType string
		data        []byte
	}

	// Helper to post a reply with hostedContents images.
	postReplyWithImages := func(rootID, caption string, images []testImage) {
		type hostedContent struct {
			TempID       string `json:"@microsoft.graph.temporaryId"`
			ContentBytes string `json:"contentBytes"`
			ContentType  string `json:"contentType"`
		}
		type msgBody struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		}
		type graphMessage struct {
			Body           msgBody          `json:"body"`
			HostedContents []hostedContent  `json:"hostedContents"`
		}

		bodyHTML := caption
		if bodyHTML != "" {
			bodyHTML += "<br>"
		}
		var hosted []hostedContent
		for i, img := range images {
			id := fmt.Sprintf("%d", i+1)
			bodyHTML += fmt.Sprintf(`<img src="../hostedContents/%s/$value" alt="%s" style="max-width:600px"/>`, id, img.name)
			if i < len(images)-1 {
				bodyHTML += "<br>"
			}
			hosted = append(hosted, hostedContent{
				TempID:       id,
				ContentBytes: base64.StdEncoding.EncodeToString(img.data),
				ContentType:  img.contentType,
			})
		}

		payload := graphMessage{
			Body:           msgBody{ContentType: "html", Content: bodyHTML},
			HostedContents: hosted,
		}
		jsonData, err := json.Marshal(payload)
		if err != nil {
			b.Log.Errorf("test: marshal image payload failed: %s", err)
			return
		}

		apiURL := fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies",
			teamID, channelID, rootID)
		token, err := b.getAccessToken()
		if err != nil {
			b.Log.Errorf("test: getAccessToken failed: %s", err)
			return
		}
		req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, apiURL, bytes.NewReader(jsonData))
		if err != nil {
			b.Log.Errorf("test: NewRequest failed: %s", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Log.Errorf("test: image post failed: %s", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			b.Log.Errorf("test: image reply failed: %d %s", resp.StatusCode, string(body))
		}
		// Do NOT add to sentIDs — let poll() pick it up for relay.
	}

	// Step 12: Single PNG image
	postReplyWithImages(rootID, "Image test: PNG", []testImage{
		{name: "demo.png", contentType: "image/png", data: testdata.DemoPNG},
	})
	time.Sleep(time.Second)

	// Step 13: Single GIF image
	postReplyWithImages(rootID, "Image test: GIF", []testImage{
		{name: "demo.gif", contentType: "image/gif", data: testdata.DemoGIF},
	})
	time.Sleep(time.Second)

	// Step 14: Multi-image (2x PNG in one message)
	postReplyWithImages(rootID, "Image test: multi-image (2x PNG)", []testImage{
		{name: "demo1.png", contentType: "image/png", data: testdata.DemoPNG},
		{name: "demo2.png", contentType: "image/png", data: testdata.DemoPNG},
	})
	time.Sleep(time.Second)

	// Step 15: Delete the marked message
	if deleteID != "" {
		deleteReply(rootID, deleteID)
	}

	// Step 16: Test finished
	postReply(rootID, "✅ Test finished", nil)

	b.Log.Info("test: test sequence completed")
}
