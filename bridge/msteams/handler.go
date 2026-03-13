package bmsteams

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"

	msgraph "github.com/yaegashi/msgraph.go/beta"
)

var hostedContentImgRE = regexp.MustCompile(`(?i)<img[^>]*src="[^"]*hostedContents/([^/]+)/\$value"[^>]*(?:alt="([^"]*)")?[^>]*/?>`)


func (b *Bmsteams) findFile(weburl string) (string, error) {
	itemRB, err := b.gc.GetDriveItemByURL(b.ctx, weburl)
	if err != nil {
		return "", err
	}
	itemRB.Workbook().Worksheets()
	b.gc.Workbooks()
	item, err := itemRB.Request().Get(b.ctx)
	if err != nil {
		return "", err
	}
	if url, ok := item.GetAdditionalData("@microsoft.graph.downloadUrl"); ok {
		return url.(string), nil
	}
	return "", nil
}

// handleDownloadFile handles file download with size validation.
func (b *Bmsteams) handleDownloadFile(rmsg *config.Message, filename, weburl string) error {
	realURL, err := b.findFile(weburl)
	if err != nil {
		return err
	}
	// Download the file with size limit enforcement.
	data, err := helper.DownloadFileWithSizeCheck(realURL, b.General.MediaDownloadSize)
	if err != nil {
		var tooLarge *helper.ErrFileTooLarge
		if errors.As(err, &tooLarge) {
			b.Log.Warnf("file %s too large (%d bytes, limit %d bytes)", filename, tooLarge.Size, tooLarge.MaxSize)
			b.notifyFileTooLarge(rmsg, filename, tooLarge.Size, tooLarge.MaxSize)
			return err
		}
		return fmt.Errorf("download %s failed %#v", weburl, err)
	}

	// If a comment is attached to the file(s) it is in the 'Text' field of the teams messge event
	// and should be added as comment to only one of the files. We reset the 'Text' field to ensure
	// that the comment is not duplicated.
	comment := rmsg.Text
	rmsg.Text = ""
	helper.HandleDownloadData(b.Log, rmsg, filename, comment, weburl, data, b.General)
	return nil
}

// notifyFileTooLarge posts a warning reply directly into the Teams channel
// (via Graph API) so the sender sees that their file was not transferred.
// This must NOT use b.Remote because the handler runs on the source side —
// b.Remote would route the warning to the destination bridge instead.
func (b *Bmsteams) notifyFileTooLarge(rmsg *config.Message, filename string, actualSize int64, maxSize int) {
	teamID := b.GetString("TeamID")
	channelID := decodeChannelID(rmsg.Channel)
	parentID := rmsg.ID

	text := fmt.Sprintf("⚠️ File <b>%s</b> could not be transferred — file too large (%d MB, limit: %d MB).",
		filename, actualSize/(1024*1024), maxSize/(1024*1024))
	htmlType := msgraph.BodyTypeVHTML
	content := &msgraph.ItemBody{Content: &text, ContentType: &htmlType}
	chatMsg := &msgraph.ChatMessage{Body: content}

	var res *msgraph.ChatMessage
	var err error
	if parentID != "" {
		ct := b.gc.Teams().ID(teamID).Channels().ID(channelID).Messages().ID(parentID).Replies().Request()
		res, err = ct.Add(b.ctx, chatMsg)
	} else {
		ct := b.gc.Teams().ID(teamID).Channels().ID(channelID).Messages().Request()
		res, err = ct.Add(b.ctx, chatMsg)
	}
	if err != nil {
		b.Log.Errorf("notifyFileTooLarge: failed to post warning: %s", err)
		return
	}
	if res != nil && res.ID != nil {
		b.sentIDs[*res.ID] = struct{}{}

		// Persist the warning reply and the original message in the cache so
		// replay won't re-process them after a restart (sentIDs is in-memory only).
		if b.MarkMessageBridged != nil {
			// Mark the original message as handled.
			if parentID != "" {
				b.MarkMessageBridged("msteams", parentID)
				// Mark the reply (warning) itself using the composite key.
				b.MarkMessageBridged("msteams", parentID+"/"+*res.ID)
			} else {
				b.MarkMessageBridged("msteams", *res.ID)
			}
		}
	}
}

func (b *Bmsteams) handleAttachments(rmsg *config.Message, msg msgraph.ChatMessage) {
	for _, a := range msg.Attachments {
		//remove the attachment tags from the text
		rmsg.Text = attachRE.ReplaceAllString(rmsg.Text, "")

		//handle a code snippet (code block)
		if *a.ContentType == "application/vnd.microsoft.card.codesnippet" {
			b.handleCodeSnippet(rmsg, a)
			continue
		}

		//handle the download
		err := b.handleDownloadFile(rmsg, *a.Name, *a.ContentURL)
		if err != nil {
			b.Log.Warnf("download of %s failed: %s", *a.Name, err)
		}
	}
}

type AttachContent struct {
	Language       string `json:"language"`
	CodeSnippetURL string `json:"codeSnippetUrl"`
}

func (b *Bmsteams) handleCodeSnippet(rmsg *config.Message, attach msgraph.ChatMessageAttachment) {
	var content AttachContent
	err := json.Unmarshal([]byte(*attach.Content), &content)
	if err != nil {
		b.Log.Errorf("unmarshal codesnippet failed: %s", err)
		return
	}
	s := strings.Split(content.CodeSnippetURL, "/")
	if len(s) != 13 {
		b.Log.Errorf("codesnippetUrl has unexpected size: %s", content.CodeSnippetURL)
		return
	}
	resp, err := b.gc.Teams().Request().Client().Get(content.CodeSnippetURL)
	if err != nil {
		b.Log.Errorf("retrieving snippet content failed:%s", err)
		return
	}
	defer resp.Body.Close()

	res, err := io.ReadAll(resp.Body)
	if err != nil {
		b.Log.Errorf("reading snippet data failed: %s", err)
		return
	}
	rmsg.Text = rmsg.Text + "\n```" + content.Language + "\n" + string(res) + "\n```\n"
}

// handleHostedContents downloads inline images embedded via hostedContents
// in the Teams message HTML body and adds them to rmsg.Extra["file"].
// parentMsgID should be empty for top-level messages, or the parent message ID for replies.
func (b *Bmsteams) handleHostedContents(rmsg *config.Message, msg msgraph.ChatMessage, parentMsgID string) {
	if msg.Body == nil || msg.Body.Content == nil {
		return
	}

	matches := hostedContentImgRE.FindAllStringSubmatch(*msg.Body.Content, -1)
	if len(matches) == 0 {
		return
	}

	teamID := b.GetString("TeamID")
	channelID := decodeChannelID(rmsg.Channel)
	msgID := *msg.ID

	for _, m := range matches {
		hcID := m[1]
		filename := m[2] // from alt attribute
		if filename == "" {
			filename = fmt.Sprintf("image_%s.png", hcID)
		}

		// Build the Graph API URL for the hostedContent binary.
		var apiURL string
		if parentMsgID == "" {
			apiURL = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/hostedContents/%s/$value",
				teamID, channelID, msgID, hcID)
		} else {
			apiURL = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies/%s/hostedContents/%s/$value",
				teamID, channelID, parentMsgID, msgID, hcID)
		}

		resp, err := b.gc.Teams().Request().Client().Get(apiURL)
		if err != nil {
			b.Log.Errorf("handleHostedContents: GET %s failed: %s", apiURL, err)
			continue
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			b.Log.Errorf("handleHostedContents: GET %s returned %d", apiURL, resp.StatusCode)
			continue
		}

		maxSize := b.General.MediaDownloadSize
		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSize)+1))
		resp.Body.Close()
		if err != nil {
			b.Log.Errorf("handleHostedContents: reading body for %s failed: %s", filename, err)
			continue
		}

		if len(data) > maxSize {
			b.Log.Warnf("handleHostedContents: %s too large (>%d bytes, limit %d bytes)", filename, maxSize, maxSize)
			b.notifyFileTooLarge(rmsg, filename, int64(len(data)), maxSize)
			continue
		}

		b.Log.Debugf("handleHostedContents: downloaded %s (%d bytes)", filename, len(data))
		comment := rmsg.Text
		rmsg.Text = ""
		helper.HandleDownloadData(b.Log, rmsg, filename, comment, "", &data, b.General)
	}
}
