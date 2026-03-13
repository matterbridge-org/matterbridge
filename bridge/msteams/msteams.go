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
                        time.Sleep(5 * time.Second)
                }
        }(channel.Name)
        return nil
}

// errDeltaTokenExpired is returned by fetchDelta when the server responds with
// HTTP 410 Gone, indicating the delta token has expired.
var errDeltaTokenExpired = fmt.Errorf("delta token expired")

// deltaResponse is the JSON structure returned by the Graph API delta endpoint.
type deltaResponse struct {
        Value     []json.RawMessage `json:"value"`
        NextLink  string            `json:"@odata.nextLink"`
        DeltaLink string            `json:"@odata.deltaLink"`
}

// deltaMessageMeta extracts the replyToId field that msgraph.ChatMessage lacks.
type deltaMessageMeta struct {
        ReplyToID *string `json:"replyToId"`
}

// fetchDelta calls the Graph API delta endpoint and paginates through all pages.
// Returns the list of messages, a map of messageID→parentID for replies, and the
// new deltaLink URL for the next incremental sync.
func (b *Bmsteams) fetchDelta(deltaURL string) (
        messages []msgraph.ChatMessage,
        replyToIDs map[string]string,
        nextDeltaLink string,
        err error,
) {
        replyToIDs = make(map[string]string)

        token, err := b.getAccessToken()
        if err != nil {
                return nil, nil, "", fmt.Errorf("getAccessToken: %w", err)
        }

        currentURL := deltaURL
        const maxPages = 10

        for page := 0; page < maxPages && currentURL != ""; page++ {
                req, reqErr := http.NewRequestWithContext(b.ctx, http.MethodGet, currentURL, nil)
                if reqErr != nil {
                        return nil, nil, "", fmt.Errorf("NewRequest: %w", reqErr)
                }
                req.Header.Set("Authorization", "Bearer "+token)

                resp, doErr := http.DefaultClient.Do(req)
                if doErr != nil {
                        return nil, nil, "", fmt.Errorf("HTTP request: %w", doErr)
                }

                body, _ := io.ReadAll(resp.Body)
                resp.Body.Close()

                if resp.StatusCode == http.StatusGone {
                        return nil, nil, "", errDeltaTokenExpired
                }
                if resp.StatusCode != http.StatusOK {
                        return nil, nil, "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
                }

                var result deltaResponse
                if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
                        return nil, nil, "", fmt.Errorf("JSON parse: %w", jsonErr)
                }

                for _, raw := range result.Value {
                        var msg msgraph.ChatMessage
                        if err := json.Unmarshal(raw, &msg); err != nil {
                                b.Log.Debugf("fetchDelta: skipping unparseable message: %s", err)
                                continue
                        }
                        if msg.ID == nil {
                                continue
                        }

                        var meta deltaMessageMeta
                        _ = json.Unmarshal(raw, &meta)

                        if meta.ReplyToID != nil && *meta.ReplyToID != "" {
                                replyToIDs[*msg.ID] = *meta.ReplyToID
                        }

                        messages = append(messages, msg)
                }

                if result.DeltaLink != "" {
                        nextDeltaLink = result.DeltaLink
                }
                currentURL = result.NextLink
        }

        // If we exhausted maxPages without getting a deltaLink, use the last nextLink
        // as a workaround (will continue pagination on next call).
        if nextDeltaLink == "" && currentURL != "" {
                nextDeltaLink = currentURL
        }

        return messages, replyToIDs, nextDeltaLink, nil
}

// deltaMessageKey returns the cache key and parentID for a delta message.
func deltaMessageKey(msg msgraph.ChatMessage, replyToIDs map[string]string) (key, parentID string) {
        if parent, isReply := replyToIDs[*msg.ID]; isReply {
                return parent + "/" + *msg.ID, parent
        }
        return *msg.ID, ""
}

// seedMsgmap populates the msgmap with timestamps from messages without relaying them.
func (b *Bmsteams) seedMsgmap(messages []msgraph.ChatMessage, replyToIDs map[string]string, msgmap map[string]time.Time, mbSrcRE *regexp.Regexp, channelName string) {
        for _, msg := range messages {
                if msg.ID == nil || msg.CreatedDateTime == nil {
                        continue
                }
                key, _ := deltaMessageKey(msg, replyToIDs)
                if msg.LastModifiedDateTime != nil {
                        msgmap[key] = *msg.LastModifiedDateTime
                } else {
                        msgmap[key] = *msg.CreatedDateTime
                }

                // Extract source ID marker from message body for persistent cache population.
                if msg.Body != nil && msg.Body.Content != nil {
                        if matches := mbSrcRE.FindStringSubmatch(*msg.Body.Content); len(matches) == 2 {
                                b.Remote <- config.Message{
                                        Event:   config.EventHistoricalMapping,
                                        Account: b.Account,
                                        Channel: channelName,
                                        ID:      *msg.ID,
                                        Extra:   map[string][]interface{}{"source_msgid": {matches[1]}},
                                }
                        }
                }
        }
}

// processReplay relays missed messages (from a delta sync after restart) to the gateway.
func (b *Bmsteams) processReplay(messages []msgraph.ChatMessage, replyToIDs map[string]string, channelName string) int {
        count := 0
        for _, msg := range messages {
                if msg.ID == nil || msg.CreatedDateTime == nil {
                        continue
                }
                if msg.From == nil || msg.From.User == nil || msg.Body == nil {
                        continue
                }
                if msg.DeletedDateTime != nil {
                        continue
                }

                key, parentID := deltaMessageKey(msg, replyToIDs)

                // Skip messages we sent.
                if _, wasSentByUs := b.sentIDs[*msg.ID]; wasSentByUs {
                        continue
                }
                // Skip if already bridged.
                if b.IsMessageBridged != nil && b.IsMessageBridged("msteams", key) {
                        continue
                }

                text := b.convertToMD(*msg.Body.Content)
                if msg.Subject != nil && *msg.Subject != "" {
                        text = "**" + *msg.Subject + "**\n" + text
                }

                createTime := *msg.CreatedDateTime
                replayPrefix := fmt.Sprintf("[Replay %s]\n", createTime.Format("2006-01-02 15:04 MST"))

                rmsg := config.Message{
                        Event:    config.EventReplayMessage,
                        Username: *msg.From.User.DisplayName,
                        Text:     replayPrefix + text,
                        Channel:  channelName,
                        Account:  b.Account,
                        UserID:   *msg.From.User.ID,
                        ID:       key,
                        ParentID: parentID,
                        Avatar:   b.GetString("IconURL"),
                        Extra:    make(map[string][]interface{}),
                }

                b.handleAttachments(&rmsg, msg)
                b.handleHostedContents(&rmsg, msg, parentID)

                // Skip empty messages (e.g. failed file download with no text content).
                hasFiles := len(rmsg.Extra["file"]) > 0
                textAfterPrefix := strings.TrimSpace(strings.TrimPrefix(rmsg.Text, replayPrefix))
                if textAfterPrefix == "" && !hasFiles {
                        continue
                }

                b.Remote <- rmsg
                count++
                time.Sleep(500 * time.Millisecond)
        }
        return count
}

// processDelta handles messages from a normal delta poll cycle (not replay).
func (b *Bmsteams) processDelta(messages []msgraph.ChatMessage, replyToIDs map[string]string, channelName string, msgmap map[string]time.Time, mbSrcRE *regexp.Regexp, startTime time.Time) {
        for _, msg := range messages {
                if msg.ID == nil || msg.CreatedDateTime == nil {
                        continue
                }

                key, parentID := deltaMessageKey(msg, replyToIDs)

                // Check if this message is new or changed.
                isNewOrChanged := true
                if mtime, ok := msgmap[key]; ok {
                        if mtime == *msg.CreatedDateTime && msg.LastModifiedDateTime == nil {
                                isNewOrChanged = false
                        } else if msg.LastModifiedDateTime != nil && mtime == *msg.LastModifiedDateTime {
                                isNewOrChanged = false
                        }
                }

                if !isNewOrChanged {
                        continue
                }

                // Guard against first-start flooding: messages created before poll
                // started that aren't in our seed (e.g. delta returning old messages
                // on first start with $deltatoken=latest) are silently seeded.
                if _, inMap := msgmap[key]; !inMap && msg.CreatedDateTime.Before(startTime) {
                        if msg.LastModifiedDateTime != nil {
                                msgmap[key] = *msg.LastModifiedDateTime
                        } else {
                                msgmap[key] = *msg.CreatedDateTime
                        }
                        continue
                }

                if b.GetBool("debug") {
                        b.Log.Debug("Msg dump: ", spew.Sdump(msg))
                }

                if msg.From == nil || msg.From.User == nil {
                        // System message or bot — update msgmap silently.
                        if msg.LastModifiedDateTime != nil {
                                msgmap[key] = *msg.LastModifiedDateTime
                        } else {
                                msgmap[key] = *msg.CreatedDateTime
                        }
                        continue
                }

                // Echo prevention: check if we PATCHed this message.
                if expiry, wasUpdatedByUs := b.updatedIDs[*msg.ID]; wasUpdatedByUs && time.Now().Before(expiry) {
                        b.Log.Debugf("skipping echo of our own edit for %s", key)
                        if msg.LastModifiedDateTime != nil {
                                msgmap[key] = *msg.LastModifiedDateTime
                        } else {
                                msgmap[key] = *msg.CreatedDateTime
                        }
                        continue
                }

                // Echo prevention: check if we posted this message.
                if _, wasSentByUs := b.sentIDs[*msg.ID]; wasSentByUs {
                        b.Log.Debug("skipping own message")
                        if msg.LastModifiedDateTime != nil {
                                msgmap[key] = *msg.LastModifiedDateTime
                        } else {
                                msgmap[key] = *msg.CreatedDateTime
                        }
                        delete(b.sentIDs, *msg.ID)
                        continue
                }

                // Determine event type: delete, edit, or new.
                isDelete := msg.DeletedDateTime != nil
                isEdit := false
                if !isDelete {
                        if _, alreadySeen := msgmap[key]; alreadySeen {
                                isEdit = true
                        }
                }

                // Update msgmap.
                if msg.LastModifiedDateTime != nil {
                        msgmap[key] = *msg.LastModifiedDateTime
                } else {
                        msgmap[key] = *msg.CreatedDateTime
                }

                // Extract source ID marker.
                if msg.Body != nil && msg.Body.Content != nil {
                        if matches := mbSrcRE.FindStringSubmatch(*msg.Body.Content); len(matches) == 2 {
                                b.Remote <- config.Message{
                                        Event:   config.EventHistoricalMapping,
                                        Account: b.Account,
                                        Channel: channelName,
                                        ID:      *msg.ID,
                                        Extra:   map[string][]interface{}{"source_msgid": {matches[1]}},
                                }
                        }
                }

                text := ""
                if msg.Body != nil && msg.Body.Content != nil {
                        text = b.convertToMD(*msg.Body.Content)
                }

                // Intercept test command (only for new root messages).
                if !isDelete && !isEdit && parentID == "" && b.isTestCommand(text) {
                        b.Log.Info("Test command received, starting test sequence")
                        go b.runTestSequence(channelName)
                        continue
                }

                // Prepend subject if present.
                if msg.Subject != nil && *msg.Subject != "" {
                        text = "**" + *msg.Subject + "**\n" + text
                }

                event := ""
                if isDelete {
                        event = config.EventMsgDelete
                        text = config.EventMsgDelete
                } else if isEdit {
                        event = "msg_update"
                }

                b.Log.Debugf("<= Sending message from %s on %s to gateway", *msg.From.User.DisplayName, b.Account)

                rmsg := config.Message{
                        Username: *msg.From.User.DisplayName,
                        Text:     text,
                        Channel:  channelName,
                        Account:  b.Account,
                        UserID:   *msg.From.User.ID,
                        ID:       key,
                        ParentID: parentID,
                        Event:    event,
                        Avatar:   b.GetString("IconURL"),
                        Extra:    make(map[string][]interface{}),
                }
                if !isEdit && !isDelete {
                        b.handleAttachments(&rmsg, msg)
                        b.handleHostedContents(&rmsg, msg, parentID)
                }
                b.Log.Debugf("<= Message is %#v", rmsg)
                b.Remote <- rmsg
        }
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

        // Prepend priority indicator emoji for Mattermost important/urgent messages.
        if msg.Extra != nil {
                if priorities, ok := msg.Extra["priority"]; ok && len(priorities) > 0 {
                        if prio, ok := priorities[0].(string); ok {
                                switch prio {
                                case "important":
                                        msg.Text = "❗ " + msg.Text
                                case "urgent":
                                        msg.Text = "🚨 " + msg.Text
                                }
                        }
                }
        }

        // Handle file/image attachments.
        if msg.Extra != nil && len(msg.Extra["file"]) > 0 {
                // Build caption from msg.Text for the first message.
                captionHTML := ""
                if msg.Text != "" {
                        captionText := mapEmojis(msg.Text)
                        captionHTML = mdToTeamsHTML(captionText)
                }

                // Classify files: supported images (hostedContents) vs others.
                var supportedImages []config.FileInfo
                var otherFiles []config.FileInfo
                for _, files := range msg.Extra["file"] {
                        fi, ok := files.(config.FileInfo)
                        if !ok {
                                continue
                        }
                        if isImageFile(fi.Name) && fi.Data != nil && isSupportedHostedContentType(fi.Name) {
                                supportedImages = append(supportedImages, fi)
                        } else {
                                otherFiles = append(otherFiles, fi)
                        }
                }

                var firstID string

                // Send all supported images in a single Teams message.
                if len(supportedImages) > 0 {
                        id, err := b.sendImageHostedContent(msg, supportedImages, captionHTML)
                        if err != nil {
                                b.Log.Warnf("sendImageHostedContent failed: %s", err)
                        } else {
                                firstID = id
                                captionHTML = "" // caption was included, don't duplicate
                        }
                }

                // Handle remaining files individually (URL-based or notification).
                for _, fi := range otherFiles {
                        id, err := b.sendFileAsMessage(msg, fi, captionHTML)
                        if err != nil {
                                b.Log.Warnf("sending file %s: %s", fi.Name, err)
                        }
                        if firstID == "" && id != "" {
                                firstID = id
                        }
                        captionHTML = "" // only include caption once
                }

                // Return the first message ID for gateway thread-reply mapping.
                return firstID, nil
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
        b.updatedIDs[*res.ID] = time.Now().Add(30 * time.Second)
        return *res.ID, nil
}

// mdToTeamsHTML converts markdown text to Teams-compatible HTML.
// Handles bold, italic, strikethrough, headings, links, blockquotes,
// code fences, and line breaks using the gomarkdown library.
// Code fences are post-processed to use Teams-native <codeblock> tags.
func mdToTeamsHTML(text string) string {
        extensions := parser.HardLineBreak | parser.NoIntraEmphasis | parser.FencedCode | parser.Strikethrough | parser.Autolink
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

        html := result + bodyHTML

        // Embed source message ID as hidden span for historical cache population.
        if srcIDs, ok := msg.Extra["source_msgid"]; ok && len(srcIDs) > 0 {
                if srcID, ok := srcIDs[0].(string); ok {
                        html += `<span data-mb-src="` + htmlEscape(srcID) + `" style="display:none"></span>`
                }
        }

        return html
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

// isSupportedHostedContentType returns true if the file type can be embedded
// via the Graph API hostedContents endpoint. Only JPG and PNG are supported.
func isSupportedHostedContentType(name string) bool {
        mime := mimeTypeForFile(name)
        return mime == "image/jpeg" || mime == "image/png"
}

// sendImageHostedContent sends one or more images as a single Teams message using
// the hostedContents API. Image data is base64-encoded and embedded directly in the
// message, so no external server or public URL is required. Only works for JPG/PNG.
// The captionHTML parameter allows including additional text in the same message.
func (b *Bmsteams) sendImageHostedContent(msg config.Message, files []config.FileInfo, captionHTML string) (string, error) {
        if len(files) == 0 {
                return "", fmt.Errorf("sendImageHostedContent requires at least one file")
        }

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

        usernameHTML := b.formatMessageHTML(msg, "")
        bodyHTML := usernameHTML
        if captionHTML != "" {
                bodyHTML += captionHTML + "<br>"
        }

        var hosted []hostedContent
        for i, fi := range files {
                if fi.Data == nil {
                        continue
                }
                id := fmt.Sprintf("%d", i+1)
                bodyHTML += fmt.Sprintf(
                        `<img src="../hostedContents/%s/$value" alt="%s" style="max-width:600px"/>`,
                        id, fi.Name,
                )
                if i < len(files)-1 {
                        bodyHTML += "<br>"
                }
                hosted = append(hosted, hostedContent{
                        TempID:       id,
                        ContentBytes: base64.StdEncoding.EncodeToString(*fi.Data),
                        ContentType:  mimeTypeForFile(fi.Name),
                })
        }

        if len(hosted) == 0 {
                return "", fmt.Errorf("no valid image data to send")
        }

        payload := graphMessage{
                Body: msgBody{
                        ContentType: "html",
                        Content:     bodyHTML,
                },
                HostedContents: hosted,
        }

        jsonData, err := json.Marshal(payload)
        if err != nil {
                return "", err
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
                return "", err
        }

        req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, apiURL, bytes.NewReader(jsonData))
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

        respBody, _ := io.ReadAll(resp.Body)

        if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
                return "", fmt.Errorf("sendImageHostedContent failed: %d %s", resp.StatusCode, string(respBody))
        }

        // Parse the response to extract the message ID for echo prevention.
        var result struct {
                ID string `json:"id"`
        }
        if err := json.Unmarshal(respBody, &result); err == nil && result.ID != "" {
                b.sentIDs[result.ID] = struct{}{}
                b.updatedIDs[result.ID] = time.Now().Add(30 * time.Second)
                return result.ID, nil
        }
        return "", nil
}

// sendFileAsMessage sends a file as a Teams message using a URL (either from the
// source bridge or uploaded to a MediaServer). For hostedContents-supported images,
// use sendImageHostedContent instead (called from Send()).
// The captionHTML parameter allows including additional text in the same message.
func (b *Bmsteams) sendFileAsMessage(msg config.Message, fi config.FileInfo, captionHTML string) (string, error) {
        isImage := isImageFile(fi.Name)

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
        captionPart := ""
        if captionHTML != "" {
                captionPart = captionHTML + "<br>"
        }

        switch {
        case fileURL != "" && isImage:
                bodyText = fmt.Sprintf(
                        `%s%s<img src="%s" alt="%s" style="max-width:600px"/>`,
                        usernameHTML, captionPart, fileURL, fi.Name,
                )
        case fileURL != "":
                bodyText = fmt.Sprintf(
                        `%s%s&#128206; <a href="%s">%s</a>`,
                        usernameHTML, captionPart, fileURL, fi.Name,
                )
        default:
                // File can't be sent: no hostedContents support and no MediaServer URL.
                // Send a notification back to the source side via b.Remote so users
                // know the file didn't arrive (instead of posting to Teams).
                b.Log.Warnf("cannot send file %s (%s) to Teams: type not supported by hostedContents and no MediaServerUpload configured",
                        fi.Name, mimeTypeForFile(fi.Name))
                // Return a fake ID so the gateway caches it as a BrMsgID for this
                // message.  The notification references it as ParentID — the gateway
                // then resolves it back to the original source post ID via the
                // downstream search in FindCanonicalMsgID + the protocol-strip fallback
                // in getDestMsgID.
                fakeID := fmt.Sprintf("unsupported-%d", time.Now().UnixNano())
                go func() {
                        b.Remote <- config.Message{
                                Text: fmt.Sprintf("⚠️ File **%s** (%s) could not be transferred to Teams"+
                                        " — format not supported, no MediaServer configured.",
                                        fi.Name, mimeTypeForFile(fi.Name)),
                                Channel:  msg.Channel,
                                Account:  b.Account,
                                Username: "matterbridge",
                                ParentID: fakeID,
                                Extra:    make(map[string][]interface{}),
                        }
                }()
                return fakeID, nil
        }

        content := &msgraph.ItemBody{
                Content:     &bodyText,
                ContentType: &contentType,
        }
        chatMsg := &msgraph.ChatMessage{Body: content}

        var res *msgraph.ChatMessage
        var err error
        if msg.ParentValid() {
                ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().ID(msg.ParentID).Replies().Request()
                res, err = ct.Add(b.ctx, chatMsg)
        } else {
                ct := b.gc.Teams().ID(b.GetString("TeamID")).Channels().ID(decodeChannelID(msg.Channel)).Messages().Request()
                res, err = ct.Add(b.ctx, chatMsg)
        }
        if err != nil {
                return "", err
        }
        if res != nil && res.ID != nil {
                b.sentIDs[*res.ID] = struct{}{}
                b.updatedIDs[*res.ID] = time.Now().Add(30 * time.Second)
                return *res.ID, nil
        }
        return "", nil
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
        b.updatedIDs[*res.ID] = time.Now().Add(30 * time.Second)
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

// poll uses Graph API delta queries to detect new/changed/deleted messages and replies
// in a single API call, replacing the previous getMessages+getReplies approach.
// On first start (no stored delta token), it initializes with $deltatoken=latest.
// On restart (stored delta token), it replays missed messages before entering the poll loop.
//
//nolint:gocognit
func (b *Bmsteams) poll(channelName string) error {
        channelKey := channelName + b.Account
        teamID := b.GetString("TeamID")
        channelID := decodeChannelID(channelName)
        mbSrcRE := regexp.MustCompile(`data-mb-src="([^"]+)"`)
        startTime := time.Now()

        // 1. Determine initial delta URL: stored token (replay) or $deltatoken=latest (first start).
        isReplay := false
        var deltaURL string
        if b.GetDeltaToken != nil {
                if token, ok := b.GetDeltaToken(channelKey); ok && token != "" {
                        deltaURL = token
                        isReplay = true
                }
        }
        if deltaURL == "" {
                deltaURL = fmt.Sprintf(
                        "https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/delta?$deltatoken=latest",
                        teamID, channelID)
                b.Log.Debugf("poll: first start for %s, using $deltatoken=latest", channelName)
        }

        // 2. Initial fetch.
        messages, replyToIDs, deltaLink, err := b.fetchDelta(deltaURL)
        if err == errDeltaTokenExpired {
                b.Log.Warn("poll: delta token expired, re-initializing")
                deltaURL = fmt.Sprintf(
                        "https://graph.microsoft.com/beta/teams/%s/channels/%s/messages/delta?$deltatoken=latest",
                        teamID, channelID)
                messages, replyToIDs, deltaLink, err = b.fetchDelta(deltaURL)
                isReplay = false
        }
        if err != nil {
                return fmt.Errorf("initial fetchDelta: %w", err)
        }

        msgmap := make(map[string]time.Time)

        if isReplay {
                count := b.processReplay(messages, replyToIDs, channelName)
                if count > 0 {
                        b.Log.Infof("poll: replayed %d missed messages from %s", count, channelName)
                }
        }
        // Seed msgmap with all messages from the initial fetch (including replayed ones).
        b.seedMsgmap(messages, replyToIDs, msgmap, mbSrcRE, channelName)

        if b.SetDeltaToken != nil && deltaLink != "" {
                b.SetDeltaToken(channelKey, deltaLink)
        }

        b.Log.Debugf("poll: entering delta poll loop for %s", channelName)

        // 3. Poll loop.
        for {
                time.Sleep(2 * time.Second)

                messages, replyToIDs, newDeltaLink, err := b.fetchDelta(deltaLink)
                if err == errDeltaTokenExpired {
                        return fmt.Errorf("delta token expired mid-poll: %w", err)
                }
                if err != nil {
                        return fmt.Errorf("fetchDelta: %w", err)
                }

                b.processDelta(messages, replyToIDs, channelName, msgmap, mbSrcRE, startTime)

                if newDeltaLink != "" {
                        deltaLink = newDeltaLink
                        if b.SetDeltaToken != nil {
                                b.SetDeltaToken(channelKey, deltaLink)
                        }
                }
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
