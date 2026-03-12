package bmsteams

import (
	"encoding/json"
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

// handleDownloadFile handles file download
func (b *Bmsteams) handleDownloadFile(rmsg *config.Message, filename, weburl string) error {
	realURL, err := b.findFile(weburl)
	if err != nil {
		return err
	}
	// Actually download the file.
	data, err := helper.DownloadFile(realURL)
	if err != nil {
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
			b.Log.Errorf("download of %s failed: %s", *a.Name, err)
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

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			b.Log.Errorf("handleHostedContents: reading body for %s failed: %s", filename, err)
			continue
		}

		if resp.StatusCode >= 400 {
			b.Log.Errorf("handleHostedContents: GET %s returned %d", apiURL, resp.StatusCode)
			continue
		}

		b.Log.Debugf("handleHostedContents: downloaded %s (%d bytes)", filename, len(data))
		comment := rmsg.Text
		rmsg.Text = ""
		helper.HandleDownloadData(b.Log, rmsg, filename, comment, "", &data, b.General)
	}
}
