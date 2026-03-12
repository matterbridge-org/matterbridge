package bmsteams

import (
        "bytes"
        "context"
        "encoding/base64"
        "encoding/json"
        "fmt"
        "io"
        "mime/multipart"
        "net/http"
        "net/url"
        "os"
        "path/filepath"
        "regexp"
        "strings"
        "time"

        "github.com/matterbridge-org/matterbridge/bridge"
        "github.com/matterbridge-org/matterbridge/bridge/config"
        "github.com/davecgh/go-spew/spew"
        "github.com/gomarkdown/markdown"
        mdhtml "github.com/gomarkdown/markdown/html"
        "github.com/gomarkdown/markdown/parser"
        "github.com/mattn/godown"
        msgraph "github.com/yaegashi/msgraph.go/beta"
        "github.com/yaegashi/msgraph.go/msauth"
        "golang.org/x/oauth2"
)

var (
        defaultScopes = []string{"openid", "profile", "offline_access", "Group.Read.All", "Group.ReadWrite.All"}
        attachRE      = regexp.MustCompile(`<attachment id=.*?attachment>`)
)

type Bmsteams struct {
        gc         *msgraph.GraphServiceRequestBuilder
        ctx        context.Context
        botID      string
        ts         oauth2.TokenSource // token source for fresh access tokens
        sentIDs    map[string]struct{}  // IDs of messages/replies we posted (echo prevention)
        updatedIDs map[string]time.Time // IDs of messages we PATCHed, with expiry time
        *bridge.Config
}

func New(cfg *bridge.Config) bridge.Bridger {
        return &Bmsteams{
                Config:     cfg,
                sentIDs:    make(map[string]struct{}),
                updatedIDs: make(map[string]time.Time),
        }
}

func (b *Bmsteams) Connect() error {
        tokenCachePath := b.GetString("sessionFile")
        if tokenCachePath == "" {
                tokenCachePath = "msteams_session.json"
        }

        ctx := context.Background()
        m := msauth.NewManager()
        m.LoadFile(tokenCachePath) //nolint:errcheck

        ts, err := m.DeviceAuthorizationGrant(ctx, b.GetString("TenantID"), b.GetString("ClientID"), defaultScopes, nil)
        if err != nil {
                return err
        }

        err = m.SaveFile(tokenCachePath)
        if err != nil {
                b.Log.Errorf("Couldn't save sessionfile in %s: %s", tokenCachePath, err)
        }

        err = os.Chmod(tokenCachePath, 0o600)
        if err != nil {
                b.Log.Errorf("Couldn't change permissions for %s: %s", tokenCachePath, err)
        }

        httpClient := oauth2.NewClient(ctx, ts)
        graphClient := msgraph.NewClient(httpClient)
        b.gc = graphClient
        b.ctx = ctx

        // Store the token source so we can get fresh tokens for direct HTTP calls.
        b.ts = ts

        err = b.setBotID()
        if err != nil {
                return err
        }

        b.Log.Info("Connection succeeded")
        return nil
}

func (b *Bmsteams) Disconnect() error {
        return nil
}

func (b *Bmsteams) JoinChannel(channel config.ChannelInfo) error {
        go func(name string) {
                for {
                        err := b.poll(name)
                        if err != nil {
                                b.Log.Errorf("polling failed for %s: %s. retrying in 5 seconds", name, err)
                        }
                        time.Sleep(time.Second * 2)
                }
        }(channel.Name)
        return nil
}

func (b *Bmsteams) Send(msg config.Message) (string, error) {
        b.Log.Debugf("=> Receiving %#v", msg)

        // Debug: log nick resolution for troubleshooting RemoteNickFormat.
        if nicks, ok := msg.Extra["nick"]; ok && len(nicks) > 0 {
                b.Log.Debugf("nick from Extra: %v, msg.Username: %s", nicks[0], msg.Username)
        } else {
                b.Log.Debugf("no nick in Extra, msg.Username: %s", msg.Username)
        }

        // Handle deletes from Mattermost → Teams.
        if msg.Event == config.EventMsgDelete && msg.ID != "" {
                b.Log.Debugf("delete: soft-deleting Teams message ID %s", msg.ID)
                return b.deleteMessage(msg)
        }

        // Handle edits from Mattermost → Teams.
        // The gateway sets msg.ID="" on first send, but on edits it maps the Mattermost
        // post-ID to the Teams message-ID (returned by our Send()) and passes it here.
        // So msg.ID != "" (and not a delete) means this is an edit.
        if msg.ID != "" {
                b.Log.Debugf("edit: updating Teams message ID %s", msg.ID)
                return b.updateMessage(msg)
        }

        // Handle file/image attachments.
        if msg.Extra != nil {
                for _, files := range msg.Extra["file"] {
                        fi, ok := files.(config.FileInfo)
                        if !ok {
                                continue
                        }
                        if err := b.sendFileAsMessage(msg, fi); err != nil {
                                b.Log.Errorf("sending file %s failed: %s", fi.Name, err)
                        }
                }
                if msg.Text == "" && len(msg.Extra["file"]) > 0 {
                        return "", nil
                }
        }

        if msg.ParentValid() {
                return b.sendReply(msg)
        }

        if msg.ParentNotFound() {
                msg.ParentID = ""
                // Don't add a [thread] prefix — the message is posted to the correct
                // context already and the prefix just clutters the content.
        }

        ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().Request()

        // Apply emoji mapping for any platform-specific shortcodes.
        msg.Text = mapEmojis(msg.Text)

        // Convert markdown to Teams HTML and prepend formatted username.
        htmlText := b.formatMessageHTML(msg, mdToTeamsHTML(msg.Text))
        htmlType := msgraph.BodyTypeVHTML
        content := &msgraph.ItemBody{Content: &htmlText, ContentType: &htmlType}
        rmsg := &msgraph.ChatMessage{Body: content}

        res, err := ct.Add(b.ctx, rmsg)
        if err != nil {
                return "", err
        }
        b.sentIDs[*res.ID] = struct{}{}
        return *res.ID, nil
}

// mdToTeamsHTML converts markdown text to Teams-compatible HTML.
// Handles bold, italic, strikethrough, headings, links, blockquotes,
// code fences, and line breaks using the gomarkdown library.
// Code fences are post-processed to use Teams-native <codeblock> tags.
func mdToTeamsHTML(text string) string {
        extensions := parser.HardLineBreak | parser.NoIntraEmphasis | parser.FencedCode | parser.Strikethrough
        p := parser.NewWithExtensions(extensions)
        renderer := mdhtml.NewRenderer(mdhtml.RendererOptions{Flags: 0})
        result := string(markdown.ToHTML([]byte(text), p, renderer))

        // Post-process: convert gomarkdown's <pre><code class="language-X"> to Teams <codeblock class="X"><code>.
        preCodeLangRE := regexp.MustCompile(`<pre><code class="language-(\w+)">`)
        result = preCodeLangRE.ReplaceAllString(result, `<codeblock class="$1"><code>`)
        result = strings.ReplaceAll(result, "</code></pre>", "</code></codeblock>")
        result = strings.ReplaceAll(result, "<pre><code>", "<codeblock><code>")

        // Post-process: convert gomarkdown's <del> to <s> for Teams strikethrough support.
        // Teams renders <s> but not <del>.
        result = strings.ReplaceAll(result, "<del>", "<s>")
        result = strings.ReplaceAll(result, "</del>", "</s>")

        return strings.TrimSpace(result)
}

// htmlEscape escapes HTML special characters in a string.
func htmlEscape(s string) string {
        s = strings.ReplaceAll(s, "&", "&amp;")
        s = strings.ReplaceAll(s, "<", "&lt;")
        s = strings.ReplaceAll(s, ">", "&gt;")
        s = strings.ReplaceAll(s, "\"", "&quot;")
        return s
}

// extractBridgeName returns the bridge name part from an account string like "mattermost.mybot".
func extractBridgeName(account string) string {
        parts := strings.SplitN(account, ".", 2)
        if len(parts) > 1 {
                return parts[1]
        }
        return account
}

// formatMessageHTML builds an HTML username prefix from the RemoteNickFormat template.
// It replaces {NICK} with <b>nick</b>, \n with <br>, and expands other placeholders.
func (b *Bmsteams) formatMessageHTML(msg config.Message, bodyHTML string) string {
        template := b.GetString("RemoteNickFormat")
        if template == "" {
                return bodyHTML
        }

        // Extract original nick from Extra (set by gateway).
        originalNick := ""
        if nicks, ok := msg.Extra["nick"]; ok && len(nicks) > 0 {
                if n, ok := nicks[0].(string); ok {
                        originalNick = n
                }
        }
        if originalNick == "" {
                originalNick = strings.TrimSpace(msg.Username)
        }

        // HTML-aware expansion.
        result := template
        result = strings.ReplaceAll(result, "{NICK}", "<b>"+htmlEscape(originalNick)+"</b>")
        result = strings.ReplaceAll(result, "{NOPINGNICK}", "<b>"+htmlEscape(originalNick)+"</b>")
        result = strings.ReplaceAll(result, "{PROTOCOL}", htmlEscape(msg.Protocol))
        result = strings.ReplaceAll(result, "{BRIDGE}", htmlEscape(extractBridgeName(msg.Account)))
        result = strings.ReplaceAll(result, "{GATEWAY}", htmlEscape(msg.Gateway))
        result = strings.ReplaceAll(result, "{USERID}", htmlEscape(msg.UserID))
        result = strings.ReplaceAll(result, "{CHANNEL}", htmlEscape(msg.Channel))
        result = strings.ReplaceAll(result, "\n", "<br>")

        return result + bodyHTML
}

// getAccessToken returns a fresh access token from the token source.
func (b *Bmsteams) getAccessToken() (string, error) {
        t, err := b.ts.Token()
        if err != nil {
                return "", fmt.Errorf("failed to get access token: %w", err)
        }
        return t.AccessToken, nil
}

// updateMessage patches an existing Teams message with new content.
// The Teams Graph API only allows the original sender to update via delegated perms,
// so this may fail if matterbridge is not authenticated as the message author.
func (b *Bmsteams) updateMessage(msg config.Message) (string, error) {
        // Apply emoji mapping and convert markdown to Teams HTML.
        msg.Text = mapEmojis(msg.Text)
        htmlText := b.formatMessageHTML(msg, mdToTeamsHTML(msg.Text))

        type patchBody struct {
                Body struct {
                        ContentType string `json:"contentType"`
                        Content     string `json:"content"`
                } `json:"body"`
        }

        var patch patchBody
        patch.Body.ContentType = "html"
        patch.Body.Content = htmlText

        jsonData, err := json.Marshal(patch)
        if err != nil {
                return "", err
        }

        teamID := b.GetString("TeamID")
        channelID := msg.Channel
        messageID := msg.ID

        url := fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s",
                teamID, channelID, messageID)

        token, err := b.getAccessToken()
        if err != nil {
                return "", err
        }

        req, err := http.NewRequestWithContext(b.ctx, http.MethodPatch, url, bytes.NewReader(jsonData))
        if err != nil {
                return "", err
        }
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+token)

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
                body, _ := io.ReadAll(resp.Body)
                return "", fmt.Errorf("update message failed: %d %s", resp.StatusCode, string(body))
        }

        // Suppress echo: ignore this message in the poll loop for the next 30 seconds.
        // Teams may update LastModifiedDateTime multiple times after a PATCH.
        b.updatedIDs[msg.ID] = time.Now().Add(30 * time.Second)
        return msg.ID, nil
}

// deleteMessage soft-deletes a Teams channel message or reply via the Graph API.
// For replies, msg.ParentID must be set to the top-level message ID.
func (b *Bmsteams) deleteMessage(msg config.Message) (string, error) {
        teamID := b.GetString("TeamID")
        channelID := msg.Channel
        messageID := msg.ID

        var url string
        if msg.ParentID != "" {
                // This is a reply — use the reply softDelete endpoint.
                url = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies/%s/softDelete",
                        teamID, channelID, msg.ParentID, messageID)
        } else {
                url = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/softDelete",
                        teamID, channelID, messageID)
        }

        req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, url, nil)
        if err != nil {
                return "", err
        }

        token, err := b.getAccessToken()
        if err != nil {
                return "", err
        }
        req.Header.Set("Authorization", "Bearer "+token)

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
                body, _ := io.ReadAll(resp.Body)
                return "", fmt.Errorf("delete message failed: %d %s", resp.StatusCode, string(body))
        }

        // Suppress echo for the deletion event.
        b.updatedIDs[messageID] = time.Now().Add(30 * time.Second)
        return messageID, nil
}

// uploadToMediaServer uploads file bytes to the configured MediaServerUpload endpoint.
func (b *Bmsteams) uploadToMediaServer(fi config.FileInfo) (string, error) {
        serverURL := b.GetString("MediaServerUpload")
        if serverURL == "" {
                return "", fmt.Errorf("no MediaServerUpload configured")
        }

        var buf bytes.Buffer
        writer := multipart.NewWriter(&buf)

        part, err := writer.CreateFormFile("file", fi.Name)
        if err != nil {
                return "", err
        }
        if _, err = io.Copy(part, bytes.NewReader(*fi.Data)); err != nil {
                return "", err
        }
        writer.Close()

        resp, err := http.Post(serverURL+"/"+fi.Name, writer.FormDataContentType(), &buf) //nolint:gosec
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
                return "", fmt.Errorf("media server returned %d", resp.StatusCode)
        }

        urlBytes, err := io.ReadAll(resp.Body)
        if err != nil {
                return "", err
        }
        return strings.TrimSpace(string(urlBytes)), nil
}

// mimeTypeForFile returns a MIME type for image files, or empty string otherwise.
func mimeTypeForFile(name string) string {
        switch strings.ToLower(filepath.Ext(name)) {
        case ".jpg", ".jpeg":
                return "image/jpeg"
        case ".png":
                return "image/png"
        case ".gif":
                return "image/gif"
        case ".webp":
                return "image/webp"
        case ".svg":
                return "image/svg+xml"
        case ".bmp":
                return "image/bmp"
        default:
                return ""
        }
}

func isImageFile(name string) bool {
        return mimeTypeForFile(name) != ""
}

// sendImageHostedContent sends an image as a Teams message using the hostedContents API.
// The image data is base64-encoded and embedded directly in the message, so no external
// server or public URL is required. Only works for image files.
func (b *Bmsteams) sendImageHostedContent(msg config.Message, fi config.FileInfo) error {
        mimeType := mimeTypeForFile(fi.Name)
        if mimeType == "" || fi.Data == nil {
                return fmt.Errorf("sendImageHostedContent requires image file with data")
        }

        usernameHTML := b.formatMessageHTML(msg, "")
        bodyHTML := fmt.Sprintf(
                `%s<img src="../hostedContents/1/$value" alt="%s" style="max-width:600px"/>`,
                usernameHTML, fi.Name,
        )

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

        payload := graphMessage{
                Body: msgBody{
                        ContentType: "html",
                        Content:     bodyHTML,
                },
                HostedContents: []hostedContent{
                        {
                                TempID:       "1",
                                ContentBytes: base64.StdEncoding.EncodeToString(*fi.Data),
                                ContentType:  mimeType,
                        },
                },
        }

        jsonData, err := json.Marshal(payload)
        if err != nil {
                return err
        }

        teamID := b.GetString("TeamID")
        channelID := decodeChannelID(msg.Channel)

        var apiURL string
        if msg.ParentValid() {
                apiURL = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/%s/replies",
                        teamID, channelID, msg.ParentID)
        } else {
                apiURL = fmt.Sprintf("https://graph.microsoft.com/beta/teams/%s/channels/%s/messages",
                        teamID, channelID)
        }

        token, err := b.getAccessToken()
        if err != nil {
                return err
        }

        req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, apiURL, bytes.NewReader(jsonData))
        if err != nil {
                return err
        }
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+token)

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
                return err
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
                body, _ := io.ReadAll(resp.Body)
                return fmt.Errorf("sendImageHostedContent failed: %d %s", resp.StatusCode, string(body))
        }

        return nil
}

func (b *Bmsteams) sendFileAsMessage(msg config.Message, fi config.FileInfo) error {
        isImage := isImageFile(fi.Name)

        // Prefer hostedContents for images with binary data (no external server needed).
        if isImage && fi.Data != nil {
                if err := b.sendImageHostedContent(msg, fi); err != nil {
                        b.Log.Debugf("hostedContents failed for %s, falling back: %s", fi.Name, err)
                } else {
                        return nil
                }
        }

        contentType := msgraph.BodyTypeVHTML
        var bodyText string

        fileURL := fi.URL
        if fileURL == "" && fi.Data != nil {
                uploadedURL, err := b.uploadToMediaServer(fi)
                if err != nil {
                        b.Log.Debugf("media server upload failed for %s: %s", fi.Name, err)
                } else {
                        fileURL = uploadedURL
                }
        }

        usernameHTML := b.formatMessageHTML(msg, "")

        switch {
        case fileURL != "" && isImage:
                bodyText = fmt.Sprintf(
                        `%s<img src="%s" alt="%s" style="max-width:600px"/>`,
                        usernameHTML, fileURL, fi.Name,
                )
        case fileURL != "":
                bodyText = fmt.Sprintf(
                        `%s&#128206; <a href="%s">%s</a>`,
                        usernameHTML, fileURL, fi.Name,
                )
        default:
                b.Log.Debugf("cannot send file %s to Teams: no URL and hostedContents failed", fi.Name)
                notice := fmt.Sprintf("%s[Datei: %s — Dateiübertragung nicht möglich]", usernameHTML, fi.Name)
                ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().Request()
                htmlType := msgraph.BodyTypeVHTML
                content := &msgraph.ItemBody{Content: &notice, ContentType: &htmlType}
                _, err := ct.Add(b.ctx, &msgraph.ChatMessage{Body: content})
                return err
        }

        content := &msgraph.ItemBody{
                Content:     &bodyText,
                ContentType: &contentType,
        }
        chatMsg := &msgraph.ChatMessage{Body: content}

        if msg.ParentValid() {
                ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().ID(msg.ParentID).Replies().Request()
                _, err := ct.Add(b.ctx, chatMsg)
                return err
        }

        ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().Request()
        _, err := ct.Add(b.ctx, chatMsg)
        return err
}

func (b *Bmsteams) sendReply(msg config.Message) (string, error) {
        channelID := decodeChannelID(msg.Channel)
        b.Log.Debugf("sendReply: ParentID=%s Channel=%s", msg.ParentID, channelID)
        ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(channelID).Messages().ID(msg.ParentID).Replies().Request()

        // Apply emoji mapping for any platform-specific shortcodes.
        msg.Text = mapEmojis(msg.Text)

        // Convert markdown to Teams HTML and prepend formatted username.
        htmlText := b.formatMessageHTML(msg, mdToTeamsHTML(msg.Text))
        htmlType := msgraph.BodyTypeVHTML
        content := &msgraph.ItemBody{Content: &htmlText, ContentType: &htmlType}
        rmsg := &msgraph.ChatMessage{Body: content}

        res, err := ct.Add(b.ctx, rmsg)
        if err != nil {
                b.Log.Errorf("sendReply failed: ParentID=%s err=%s", msg.ParentID, err)
                return "", err
        }
        b.sentIDs[*res.ID] = struct{}{}
        return *res.ID, nil
}

// decodeChannelID URL-decodes a channel ID if needed.
// The gateway stores channel IDs URL-encoded (e.g. 19%3A...%40thread.tacv2)
// but the Teams Graph API requires the decoded form (19:...@thread.tacv2).
func decodeChannelID(id string) string {
        decoded, err := url.PathUnescape(id)
        if err != nil {
                return id
        }
        return decoded
}

func (b *Bmsteams) getMessages(channel string) ([]msgraph.ChatMessage, error) {
        ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(channel)).Messages().Request()

        rct, err := ct.Get(b.ctx)
        if err != nil {
                return nil, err
        }

        b.Log.Debugf("got %#v messages", len(rct))
        return rct, nil
}

func (b *Bmsteams) getReplies(channel, messageID string) ([]msgraph.ChatMessage, error) {
        ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(channel)).Messages().ID(messageID).Replies().Request()
        return ct.Get(b.ctx)
}

//nolint:gocognit
func (b *Bmsteams) poll(channelName string) error {
        msgmap := make(map[string]time.Time)

        // Record start time — we will ignore any message created before this moment
        // that wasn't already captured in our initial seed.
        startTime := time.Now()

        b.Log.Debug("getting initial messages")
        res, err := b.getMessages(channelName)
        if err != nil {
                return err
        }

        // Seed with existing messages — use newest timestamp to avoid re-delivery.
        for _, msg := range res {
                if msg.LastModifiedDateTime != nil {
                        msgmap[*msg.ID] = *msg.LastModifiedDateTime
                } else {
                        msgmap[*msg.ID] = *msg.CreatedDateTime
                }
        }

        // repliesFetchedAt tracks when we last fetched replies per message.
        // We only poll replies for messages younger than 24h.
        repliesFetchedAt := make(map[string]time.Time)

        time.Sleep(time.Second * 2)
        b.Log.Debug("polling for messages")

        for {
                res, err := b.getMessages(channelName)
                if err != nil {
                        return err
                }

                now := time.Now()

                for i := len(res) - 1; i >= 0; i-- {
                        msg := res[i]

                        // --- Top-level message ---
                        isNewOrChanged := true
                        if mtime, ok := msgmap[*msg.ID]; ok {
                                if mtime == *msg.CreatedDateTime && msg.LastModifiedDateTime == nil {
                                        isNewOrChanged = false
                                } else if msg.LastModifiedDateTime != nil && mtime == *msg.LastModifiedDateTime {
                                        isNewOrChanged = false
                                }
                        } else if msg.CreatedDateTime.Before(startTime) {
                                // Message existed before we started but wasn't in our seed
                                // (older than the ~20 messages getMessages returns).
                                // Seed it silently to prevent future re-delivery.
                                if msg.LastModifiedDateTime != nil {
                                        msgmap[*msg.ID] = *msg.LastModifiedDateTime
                                } else {
                                        msgmap[*msg.ID] = *msg.CreatedDateTime
                                }
                                isNewOrChanged = false
                        }

                        if isNewOrChanged {
                                if b.GetBool("debug") {
                                        b.Log.Debug("Msg dump: ", spew.Sdump(msg))
                                }

                                if msg.From == nil || msg.From.User == nil {
                                        msgmap[*msg.ID] = *msg.CreatedDateTime
                                } else if expiry, wasUpdatedByUs := b.updatedIDs[*msg.ID]; wasUpdatedByUs && time.Now().Before(expiry) {
                                        // We PATCHed this message — suppress echo, update msgmap silently.
                                        b.Log.Debugf("skipping echo of our own edit for %s", *msg.ID)
                                        if msg.LastModifiedDateTime != nil {
                                                msgmap[*msg.ID] = *msg.LastModifiedDateTime
                                        } else {
                                                msgmap[*msg.ID] = *msg.CreatedDateTime
                                        }
                                } else if _, wasSentByUs := b.sentIDs[*msg.ID]; wasSentByUs {
                                        // We posted this message — suppress echo.
                                        b.Log.Debug("skipping own message")
                                        msgmap[*msg.ID] = *msg.CreatedDateTime
                                        delete(b.sentIDs, *msg.ID)
                                } else {
                                        // Check if this is a deletion.
                                        isDelete := msg.DeletedDateTime != nil
                                        isEdit := false
                                        if !isDelete {
                                                if _, alreadySeen := msgmap[*msg.ID]; alreadySeen {
                                                        isEdit = true
                                                }
                                        }

                                        msgmap[*msg.ID] = *msg.CreatedDateTime
                                        if msg.LastModifiedDateTime != nil {
                                                msgmap[*msg.ID] = *msg.LastModifiedDateTime
                                        }

                                        b.Log.Debugf("<= Sending message from %s on %s to gateway", *msg.From.User.DisplayName, b.Account)

                                        text := b.convertToMD(*msg.Body.Content)

                                        // Intercept test command (only for new messages, not edits/deletes).
                                        if !isDelete && !isEdit && b.isTestCommand(text) {
                                                b.Log.Info("Test command received, starting test sequence")
                                                go b.runTestSequence(channelName)
                                                // Don't relay the trigger message, but continue processing other messages.
                                                continue
                                        }

                                        // Prepend subject if present (Teams thread subjects)
                                        if msg.Subject != nil && *msg.Subject != "" {
                                                text = "**" + *msg.Subject + "**\n" + text
                                        }
                                        event := ""
                                        if isDelete {
                                                event = config.EventMsgDelete
                                                text = config.EventMsgDelete // gateway ignores empty text, use event as placeholder
                                        } else if isEdit {
                                                event = "msg_update"
                                        }
                                        rmsg := config.Message{
                                                Username: *msg.From.User.DisplayName,
                                                Text:     text,
                                                Channel:  channelName,
                                                Account:  b.Account,
                                                UserID:   *msg.From.User.ID,
                                                ID:       *msg.ID,
                                                Event:    event,
                                                Avatar:   b.GetString("IconURL"),
                                                Extra:    make(map[string][]interface{}),
                                        }
                                        b.handleAttachments(&rmsg, msg)
                                        b.Log.Debugf("<= Message is %#v", rmsg)
                                        b.Remote <- rmsg
                                }
                        }

                        // --- Replies: only for messages younger than 24h ---
                        msgAge := now.Sub(*msg.CreatedDateTime)
                        if msgAge >= 24*time.Hour {
                                continue
                        }

                        lastFetch, fetched := repliesFetchedAt[*msg.ID]
                        if fetched && now.Sub(lastFetch) < 5*time.Second {
                                continue
                        }
                        _ = lastFetch

                        replies, err := b.getReplies(channelName, *msg.ID)
                        if err != nil {
                                b.Log.Errorf("getting replies for %s failed: %s", *msg.ID, err)
                                continue
                        }
                        repliesFetchedAt[*msg.ID] = now

                        for j := len(replies) - 1; j >= 0; j-- {
                                reply := replies[j]
                                key := *msg.ID + "/" + *reply.ID

                                isReplyNewOrChanged := true
                                if mtime, ok := msgmap[key]; ok {
                                        if mtime == *reply.CreatedDateTime && reply.LastModifiedDateTime == nil {
                                                isReplyNewOrChanged = false
                                        } else if reply.LastModifiedDateTime != nil && mtime == *reply.LastModifiedDateTime {
                                                isReplyNewOrChanged = false
                                        }
                                } else if reply.CreatedDateTime.Before(startTime) {
                                        // Reply existed before startup — seed silently.
                                        if reply.LastModifiedDateTime != nil {
                                                msgmap[key] = *reply.LastModifiedDateTime
                                        } else {
                                                msgmap[key] = *reply.CreatedDateTime
                                        }
                                        isReplyNewOrChanged = false
                                }

                                if !isReplyNewOrChanged {
                                        continue
                                }

                                if b.GetBool("debug") {
                                        b.Log.Debug("Reply dump: ", spew.Sdump(reply))
                                }

                                if reply.From == nil || reply.From.User == nil {
                                        msgmap[key] = *reply.CreatedDateTime
                                        continue
                                }

                                // Check if we PATCHed this reply (echo prevention with expiry).
                                if expiry, wasUpdatedByUs := b.updatedIDs[*reply.ID]; wasUpdatedByUs && time.Now().Before(expiry) {
                                        b.Log.Debugf("skipping echo of our own reply edit for %s", *reply.ID)
                                        if reply.LastModifiedDateTime != nil {
                                                msgmap[key] = *reply.LastModifiedDateTime
                                        } else {
                                                msgmap[key] = *reply.CreatedDateTime
                                        }
                                        continue
                                }

                                if _, wasSentByUs := b.sentIDs[*reply.ID]; wasSentByUs {
                                        b.Log.Debug("skipping own reply")
                                        msgmap[key] = *reply.CreatedDateTime
                                        delete(b.sentIDs, *reply.ID)
                                        continue
                                }

                                isReplyDelete := reply.DeletedDateTime != nil
                                isReplyEdit := false
                                if !isReplyDelete {
                                        if _, alreadySeen := msgmap[key]; alreadySeen {
                                                isReplyEdit = true
                                        }
                                }

                                msgmap[key] = *reply.CreatedDateTime
                                if reply.LastModifiedDateTime != nil {
                                        msgmap[key] = *reply.LastModifiedDateTime
                                }

                                b.Log.Debugf("<= Sending reply from %s on %s to gateway", *reply.From.User.DisplayName, b.Account)

                                text := b.convertToMD(*reply.Body.Content)
                                event := ""
                                if isReplyDelete {
                                        event = config.EventMsgDelete
                                        text = config.EventMsgDelete // gateway ignores empty text, use event as placeholder
                                } else if isReplyEdit {
                                        event = "msg_update"
                                }
                                rrmsg := config.Message{
                                        Username: *reply.From.User.DisplayName,
                                        Text:     text,
                                        Channel:  channelName,
                                        Account:  b.Account,
                                        UserID:   *reply.From.User.ID,
                                        ID:       key,
                                        ParentID: *msg.ID,
                                        Event:    event,
                                        Avatar:   b.GetString("IconURL"),
                                        Extra:    make(map[string][]interface{}),
                                }
                                b.handleAttachments(&rrmsg, reply)
                                b.Log.Debugf("<= Reply message is %#v", rrmsg)
                                b.Remote <- rrmsg
                        }
                }

                time.Sleep(time.Second * 2)
        }
}

func (b *Bmsteams) setBotID() error {
        req := b.gc.Me().Request()
        r, err := req.Get(b.ctx)
        if err != nil {
                return err
        }
        b.botID = *r.ID
        return nil
}

func (b *Bmsteams) convertToMD(text string) string {
        // Pre-process Teams-specific tags that godown doesn't understand.

        // Convert <emoji alt="😠"> to just the alt text (the actual emoji character).
        emojiRE := regexp.MustCompile(`<emoji[^>]*\salt="([^"]*)"[^>]*>.*?</emoji>`)
        text = emojiRE.ReplaceAllString(text, "$1")

        // Convert <codeblock class="Lang"><code>...</code></codeblock> to markdown fenced code blocks.
        codeblockRE := regexp.MustCompile(`(?is)<codeblock[^>]*class="([^"]*)"[^>]*><code[^>]*>(.*?)</code></codeblock>`)
        if codeblockRE.MatchString(text) {
                parts := codeblockRE.FindStringSubmatch(text)
                lang := strings.ToLower(parts[1])
                code := parts[2]

                // Replace <br> with newlines first (before stripping other tags).
                code = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(code, "\n")

                // Replace block-level closing/opening tags with newlines.
                code = regexp.MustCompile(`(?i)</?(div|p)(\s[^>]*)?>`).ReplaceAllString(code, "\n")

                // Strip remaining HTML tags (syntax highlighting spans etc.)
                code = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(code, "")

                // Decode HTML entities.
                code = strings.ReplaceAll(code, "&lt;", "<")
                code = strings.ReplaceAll(code, "&gt;", ">")
                code = strings.ReplaceAll(code, "&amp;", "&")
                code = strings.ReplaceAll(code, "&nbsp;", " ")
                code = strings.ReplaceAll(code, "&#160;", " ")

                // Replace non-breaking space (U+00A0) used by Teams as line separator.
                code = strings.ReplaceAll(code, "\u00a0", "\n")

                // Collapse excessive newlines.
                code = regexp.MustCompile(`\n{3,}`).ReplaceAllString(code, "\n\n")
                code = strings.TrimSpace(code)

                replacement := "\n```" + lang + "\n" + code + "\n```\n"
                text = codeblockRE.ReplaceAllLiteralString(text, replacement)
        }

        // Strip inline <img> tags that reference hostedContents URLs — these are
        // Teams-internal image URLs that require authentication and would produce
        // broken markdown like ![image](https://graph.microsoft.com/.../hostedContents/.../$value).
        // The actual image data is handled separately via handleAttachments().
        hostedImgRE := regexp.MustCompile(`(?i)<img[^>]*src="[^"]*hostedContents[^"]*"[^>]*/?>`)
        text = hostedImgRE.ReplaceAllString(text, "")

        // Convert strikethrough HTML tags to markdown before godown (godown may not handle these).
        strikeRE := regexp.MustCompile(`(?is)<(s|del|strike)>(.*?)</(s|del|strike)>`)
        text = strikeRE.ReplaceAllString(text, "~~$2~~")

        // Strip empty paragraphs that Teams inserts around code blocks.
        emptyParaRE := regexp.MustCompile(`(?i)<p[^>]*>\s*(&nbsp;|\s)*</p>`)
        text = emptyParaRE.ReplaceAllString(text, "")

        // If no HTML tags remain, return as-is (preserves codeblock newlines).
        if !strings.ContainsAny(text, "<>") {
                return strings.TrimSpace(text)
        }

        // Convert remaining HTML to Markdown using godown.
        var sb strings.Builder
        err := godown.Convert(&sb, strings.NewReader(text), nil)
        if err != nil {
                b.Log.Errorf("Couldn't convert message to markdown: %s", err)
                return text
        }

        return strings.TrimSpace(sb.String())
}
