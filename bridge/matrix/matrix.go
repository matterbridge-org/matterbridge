package bmatrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	// Initialize specific format decoders,
	// see https://pkg.go.dev/image
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"

	mautrix "maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	htmlTag            = regexp.MustCompile("</.*?>")
	htmlReplacementTag = regexp.MustCompile("<[^>]*>")
)

type NicknameCacheEntry struct {
	displayName string
	lastUpdated time.Time
}

type Bmatrix struct {
	mc          *mautrix.Client
	UserID      id.UserID
	AccessToken string
	NicknameMap map[string]NicknameCacheEntry
	RoomMap     map[id.RoomID]string
	rateMutex   sync.RWMutex
	sync.RWMutex
	*bridge.Config
}

type httpError struct {
	Errcode      string `json:"errcode"`
	Err          string `json:"error"`
	RetryAfterMs int    `json:"retry_after_ms"`
}

type matrixUsername struct {
	plain     string
	formatted string
}

// SubTextMessage represents the new content of the message in edit messages.
type SubTextMessage struct {
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	FormattedBody string `json:"formatted_body,omitempty"`
	Format        string `json:"format,omitempty"`
}

// MessageRelation explains how the current message relates to a previous message.
// Notably used for message edits.
type MessageRelation struct {
	EventID string            `json:"event_id"`
	Type    event.MessageType `json:"rel_type"`
}

type EditedMessage struct {
	event.MessageEventContent

	NewContent SubTextMessage  `json:"m.new_content"`
	RelatedTo  MessageRelation `json:"m.relates_to"`
}

type InReplyToRelationContent struct {
	EventID string `json:"event_id"`
}

type InReplyToRelation struct {
	InReplyTo InReplyToRelationContent `json:"m.in_reply_to"`
}

type ReplyMessage struct {
	event.MessageEventContent

	RelatedTo InReplyToRelation `json:"m.relates_to"`
}

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Bmatrix{Config: cfg}
	b.RoomMap = make(map[id.RoomID]string)
	b.NicknameMap = make(map[string]NicknameCacheEntry)
	return b
}

func (b *Bmatrix) Connect() error {
	var err error
	b.Log.Infof("Connecting %s", b.GetString("Server"))

	if b.GetString("MxID") != "" && b.GetString("Token") != "" && b.GetString("DeviceID") != "" {
		userID := id.UserID(b.GetString("MxID"))

		b.mc, err = mautrix.NewClient(
			b.GetString("Server"), userID, b.GetString("Token"),
		)
		if err != nil {
			return err
		}

		b.UserID = userID
		b.AccessToken = b.GetString("Token")
		b.Log.Info("Using existing Matrix credentials")
	} else {
		b.mc, err = mautrix.NewClient(b.GetString("Server"), "", "")
		if err != nil {
			return err
		}

		resp, err2 := b.mc.Login(
			context.TODO(),
			&mautrix.ReqLogin{
				Type:             mautrix.AuthTypePassword,
				Identifier:       mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: b.GetString("Login")},
				Password:         b.GetString("Password"),
				StoreCredentials: true,
			},
		)
		if err2 != nil {
			return err2
		}
		b.UserID = resp.UserID
		b.AccessToken = resp.AccessToken
	}
	/**
	// BEGIN CACHED MESSAGES FIX
	**/

	accountStore := mautrix.NewAccountDataStore("org.example.mybot.synctoken", b.mc)
	b.mc.Store = accountStore

	b.Log.Info("Connection succeeded")

	initialFilter := mautrix.Filter{
		Room: &mautrix.RoomFilter{
			Timeline: &mautrix.FilterPart{
				Limit: 0, // Request zero history messages
			},
		},
	}

	// Upload the filter using client.CreateFilter()
	filterResponse, err := b.mc.CreateFilter(context.TODO(), &initialFilter)
	if err != nil {
		b.Log.Fatalf("Failed to create filter: %v", err)
	}

	filterID := filterResponse.FilterID

	err = b.mc.Store.SaveFilterID(context.Background(), b.UserID, filterID)
	if err != nil {
		b.Log.Fatalf("Failed to save filter ID to store: %v", err)
	}

	err = b.mc.Store.SaveNextBatch(context.TODO(), b.UserID, "")
	if err != nil {
		b.Log.Fatalf("Failed to save initial sync token: %v", err)
	}
	/**
	// END CACHED MESSAGES FIX
	**/

	go b.handlematrix()
	return nil
}

func (b *Bmatrix) Disconnect() error {
	return nil
}

func (b *Bmatrix) JoinChannel(channel config.ChannelInfo) error {
	return b.retry(func() error {
		resp, err := b.mc.JoinRoom(context.TODO(), channel.Name, nil)
		if err != nil {
			return err
		}

		b.Lock()
		b.RoomMap[resp.RoomID] = channel.Name
		b.Unlock()

		return nil
	})
}

// Incoming messages from other bridges
func (b *Bmatrix) Send(msg config.Message) (string, error) {
	b.Log.Debugf("=> Receiving %#v", msg)

	roomID := b.getRoomID(msg.Channel)
	b.Log.Debugf("Channel %s maps to channel id %s", msg.Channel, roomID.String())

	username := newMatrixUsername(msg.Username)

	body := username.plain + msg.Text
	formattedBody := username.formatted + helper.ParseMarkdown(msg.Text)

	if b.GetBool("SpoofUsername") {
		// https://spec.matrix.org/v1.3/client-server-api/#mroommember
		type stateMember struct {
			AvatarURL   string           `json:"avatar_url,omitempty"`
			DisplayName string           `json:"displayname"`
			Membership  event.Membership `json:"membership"`
		}

		// TODO: reset username afterwards with DisplayName: null ?
		content := stateMember{
			AvatarURL:   "",
			DisplayName: username.plain,
			Membership:  event.MembershipJoin,
		}

		_, err := b.mc.SendStateEvent(context.TODO(), roomID, event.StateMember, b.UserID.String(), content)
		if err == nil {
			body = msg.Text
			formattedBody = helper.ParseMarkdown(msg.Text)
		}
	}

	// Make a action /me of the message
	if msg.Event == config.EventUserAction {
		content := event.MessageEventContent{
			MsgType:       event.MsgEmote,
			Body:          body,
			FormattedBody: formattedBody,
			Format:        event.FormatHTML,
		}

		if b.GetBool("HTMLDisable") {
			content.Format = ""
			content.FormattedBody = ""
		}

		var msgID id.EventID

		err := b.retry(func() error {
			resp, err := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)
			if err != nil {
				return err
			}

			msgID = resp.EventID

			return err
		})

		return msgID.String(), err
	}

	// Delete message
	if msg.Event == config.EventMsgDelete {
		if msg.ID == "" {
			return "", nil
		}

		var msgID id.EventID

		err := b.retry(func() error {
			resp, err := b.mc.RedactEvent(context.TODO(), roomID, id.EventID(msg.ID), mautrix.ReqRedact{})
			if err != nil {
				return err
			}

			msgID = resp.EventID

			return err
		})

		return msgID.String(), err
	}

	// Upload a file if it exists
	if msg.Extra != nil {
		for _, rmsg := range helper.HandleExtra(&msg, b.General) {

			err := b.retry(func() error {
				_, err := b.mc.SendText(context.TODO(), roomID, rmsg.Username+rmsg.Text)

				return err
			})
			if err != nil {
				b.Log.Errorf("sendText failed: %s", err)
			}
		}
		// check if we have files to upload (from slack, telegram or mattermost)
		if len(msg.Extra["file"]) > 0 {
			return b.handleUploadFiles(&msg, roomID)
		}
	}

	// Edit message if we have an ID
	if msg.ID != "" {
		content := event.MessageEventContent{
			Body:          body,
			FormattedBody: formattedBody,
			MsgType:       event.MsgText,
			Format:        event.FormatHTML,
			NewContent: &event.MessageEventContent{
				Body:          body,
				FormattedBody: formattedBody,
				Format:        event.FormatHTML,
				MsgType:       event.MsgText,
			},
			RelatesTo: &event.RelatesTo{
				EventID: id.EventID(msg.ID),
				Type:    event.RelReplace,
			},
		}

		if b.GetBool("HTMLDisable") {
			content.Format = ""
			content.FormattedBody = ""
			content.NewContent.Format = ""
			content.NewContent.FormattedBody = ""
		}

		err := b.retry(func() error {
			_, err := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

			return err
		})
		if err != nil {
			return "", err
		}

		return msg.ID, nil
	}

	// Use notices to send join/leave events
	if msg.Event == config.EventJoinLeave {
		content := event.MessageEventContent{
			MsgType:       event.MsgNotice,
			Body:          body,
			FormattedBody: formattedBody,
			Format:        event.FormatHTML,
		}

		if b.GetBool("HTMLDisable") {
			content.Format = ""
			content.FormattedBody = ""
		}

		var (
			resp *mautrix.RespSendEvent
			err  error
		)

		err = b.retry(func() error {
			resp, err = b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

			return err
		})
		if err != nil {
			return "", err
		}

		return resp.EventID.String(), err
	}

	// Reply to parent if message has a parent id
	if msg.ParentValid() {
		content := event.MessageEventContent{
			MsgType:       event.MsgText,
			Body:          body,
			FormattedBody: formattedBody,
			Format:        event.FormatHTML,
			RelatesTo: &event.RelatesTo{
				Type: "m.reply",
				InReplyTo: &event.InReplyTo{
					EventID: id.EventID(msg.ParentID),
				},
			},
		}

		if b.GetBool("HTMLDisable") {
			content.Format = ""
			content.FormattedBody = ""
		}

		var (
			resp *mautrix.RespSendEvent
			err  error
		)

		err = b.retry(func() error {
			resp, err = b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

			return err
		})
		if err != nil {
			return "", err
		}

		return resp.EventID.String(), err
	}

	// Send a plain text message if html is disabled
	if b.GetBool("HTMLDisable") {
		var (
			resp *mautrix.RespSendEvent
			err  error
		)

		err = b.retry(func() error {
			resp, err = b.mc.SendText(context.TODO(), roomID, body)

			return err
		})
		if err != nil {
			return "", err
		}

		return resp.EventID.String(), err
	}

	// Post normal message with HTML support (eg riot.im)
	var (
		resp *mautrix.RespSendEvent
		err  error
	)

	err = b.retry(func() error {
		content := event.MessageEventContent{
			MsgType:       event.MsgText,
			Body:          body,
			FormattedBody: formattedBody,
			Format:        event.FormatHTML,
		}

		resp, err = b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

		return err
	})
	if err != nil {
		return "", err
	}

	return resp.EventID.String(), err
}

func (b *Bmatrix) NewHttpRequest(method, uri string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, uri, body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+b.mc.AccessToken)

	return req, nil
}

func (b *Bmatrix) handlematrix() {
	syncer := b.mc.Syncer.(*mautrix.DefaultSyncer) //nolint:forcetypeassert // We're only using DefaultSyncer
	syncer.OnEventType(event.EventRedaction, b.handleRedactionEvent)
	syncer.OnEventType(event.EventMessage, b.handleMessageEvent)
	syncer.OnEventType(event.StateMember, b.handleMemberChange)
	go func() {
		for {
			if b == nil {
				return
			}
			// Call SyncWithContext() with *only* the context.
			// It will use the FilterID and empty NextBatch token saved in the store.
			syncErr := b.mc.SyncWithContext(context.TODO())
			if syncErr != nil {
				b.Log.Debugf("Sync() returned %v, retrying in 5 seconds...\n", syncErr)
				time.Sleep(time.Second * 5)

				continue
			}
		}
	}()
}

func (b *Bmatrix) handleEdit(ev *event.Event, rmsg config.Message) bool {
	relation := ev.Content.AsMessage().OptionalGetRelatesTo()

	if relation == nil {
		return false
	}

	if ev.Content.AsMessage().NewContent == nil {
		return false
	}

	newContent := ev.Content.AsMessage().NewContent

	if relation.Type != event.RelReplace {
		return false
	}

	rmsg.ID = relation.EventID.String()
	rmsg.Text = newContent.Body
	b.Remote <- rmsg

	return true
}

func (b *Bmatrix) handleReply(ev *event.Event, rmsg config.Message) bool {
	relation := ev.Content.AsMessage().OptionalGetRelatesTo()

	if relation == nil {
		return false
	}

	body := rmsg.Text

	if !b.GetBool("keepquotedreply") {
		for strings.HasPrefix(body, "> ") {
			lineIdx := strings.IndexRune(body, '\n')
			if lineIdx == -1 {
				body = ""
			} else {
				body = body[(lineIdx + 1):]
			}
		}
	}

	rmsg.Text = body

	rmsg.ParentID = relation.InReplyTo.EventID.String()
	b.Remote <- rmsg

	return true
}

func (b *Bmatrix) handleAttachment(ev *event.Event, rmsg config.Message) bool {
	if !b.containsAttachment(ev.Content) {
		return false
	}

	go func() {
		// File download is processed in the background to avoid stalling
		err := b.handleDownloadFile(&rmsg, ev.Content)
		if err != nil {
			b.Log.Errorf("%#v", err)
			return
		}

		b.Remote <- rmsg
	}()

	return true
}

func (b *Bmatrix) handleMemberChange(ctx context.Context, ev *event.Event) {
	b.Log.Debugf("== Receiving member change event: %#v", ev)
	// Update the displayname on join messages, according to https://matrix.org/docs/spec/client_server/r0.6.1#events-on-change-of-profile-information
	content := ev.Content.AsMember()

	if content.Membership == event.MembershipJoin {
		if content.Displayname != "" {
			b.cacheDisplayName(ev.Sender, ev.Content.AsMember().Displayname)
		}
	}
}

//nolint:funlen // This function is necessarily long because it is an event handler
func (b *Bmatrix) handleRedactionEvent(ctx context.Context, ev *event.Event) {
	b.Log.Debugf("== Receiving redaction event: %#v", ev)

	if ev.Sender == b.UserID {
		return
	}

	b.RLock()
	channel, ok := b.RoomMap[ev.RoomID]
	b.RUnlock()

	if !ok {
		b.Log.Debugf("Unknown room %s", ev.RoomID)
		return
	}

	// Create our message
	rmsg := config.Message{
		Username: b.getDisplayName(ctx, ev.Sender),
		Channel:  channel,
		Account:  b.Account,
		UserID:   ev.Sender.String(),
		ID:       ev.ID.String(),
		Avatar:   b.getAvatarURL(ctx, ev.Sender),
	}

	// Remove homeserver suffix if configured
	if b.GetBool("NoHomeServerSuffix") {
		re := regexp.MustCompile(`\s+\(@.*`)
		rmsg.Username = re.ReplaceAllString(rmsg.Username, `$1`)
	}

	// Delete event
	if ev.Type == event.EventRedaction {
		rmsg.Event = config.EventMsgDelete
		rmsg.ID = ev.Redacts.String()

		rmsg.Text = config.EventMsgDelete
		b.Remote <- rmsg

		return
	}

	// Text must be a string
	if rmsg.Text, ok = ev.Content.GetRaw()["body"].(string); !ok {
		contentBytes, err := json.Marshal(ev)
		if err != nil {
			b.Log.Errorf("Error marshalling event content to JSON: %v", err)
			return
		}

		eventString := string(contentBytes)

		b.Log.Errorf("Content[body] is not a string: %T\n%#v", ev.Content.GetRaw()["body"], eventString)

		return
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", ev.Sender, b.Account)

	b.Remote <- rmsg

	// not crucial, so no ratelimit check here
	err := b.mc.MarkRead(ctx, ev.RoomID, ev.ID)
	if err != nil {
		b.Log.Errorf("couldn't mark message as read %s", err.Error())
	}
}

// Outgoing messages to other bridges
//
//nolint:funlen // This function is necessarily long because it is an event handler
func (b *Bmatrix) handleMessageEvent(ctx context.Context, ev *event.Event) {
	b.Log.Debugf("== Receiving message event: %#v", ev)

	if ev.Sender == b.UserID {
		return
	}

	b.RLock()
	channel, ok := b.RoomMap[ev.RoomID]
	b.RUnlock()

	if !ok {
		b.Log.Debugf("Unknown room %s", ev.RoomID)
		return
	}

	// Create our message
	rmsg := config.Message{
		Username: b.getDisplayName(ctx, ev.Sender),
		Channel:  channel,
		Account:  b.Account,
		UserID:   ev.Sender.String(),
		ID:       ev.ID.String(),
		Avatar:   b.getAvatarURL(ctx, ev.Sender),
	}

	// Remove homeserver suffix if configured
	if b.GetBool("NoHomeServerSuffix") {
		re := regexp.MustCompile(`\s+\(@.*`)
		rmsg.Username = re.ReplaceAllString(rmsg.Username, `$1`)
	}

	// Delete event as a relation
	if ev.Unsigned.RedactedBecause != nil {
		rmsg.Event = config.EventMsgDelete
		rmsg.ID = ev.Unsigned.RedactedBecause.Redacts.String()

		rmsg.Text = config.EventMsgDelete
		b.Remote <- rmsg

		return
	}

	// Text must be a string
	if rmsg.Text, ok = ev.Content.GetRaw()["body"].(string); !ok {
		contentBytes, err := json.Marshal(ev)
		if err != nil {
			b.Log.Errorf("Error marshalling event content to JSON: %v", err)
			return
		}

		eventString := string(contentBytes)

		b.Log.Errorf("Content[body] is not a string: %T\n%#v", ev.Content.GetRaw()["body"], eventString)

		return
	}

	// Do we have a /me action
	if ev.Content.AsMessage().MsgType == event.MsgEmote {
		rmsg.Event = config.EventUserAction
	}

	// Is it an edit?
	if b.handleEdit(ev, rmsg) {
		return
	}

	// Is it a reply?
	if b.handleReply(ev, rmsg) {
		return
	}

	// Do we have an attachment
	// TODO: does matrix support multiple attachments?
	if b.handleAttachment(ev, rmsg) {
		return
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", ev.Sender, b.Account)

	b.Remote <- rmsg

	// not crucial, so no ratelimit check here
	var err = b.mc.MarkRead(ctx, ev.RoomID, ev.ID)
	if err != nil {
		b.Log.Errorf("couldn't mark message as read %s", err.Error())
	}
}

// handleDownloadFile handles file download
func (b *Bmatrix) handleDownloadFile(rmsg *config.Message, content event.Content) error {
	var (
		ok                        bool
		url, name, msgtype, mtype string
		info                      map[string]interface{}
		size                      float64
	)

	rmsg.Extra = make(map[string][]interface{})

	if url, ok = content.Raw["url"].(string); !ok {
		return fmt.Errorf("url isn't a %T", url)
	}
	// Matrix downloads now have to be authenticated with an access token
	// See https://github.com/matrix-org/matrix-spec-proposals/blob/main/proposals/3916-authentication-for-media.md
	// Also see: https://github.com/matterbridge-org/matterbridge/issues/36
	url = strings.ReplaceAll(url, "mxc://", b.GetString("Server")+"/_matrix/client/v1/media/download/")

	if info, ok = content.Raw["info"].(map[string]any); !ok {
		return fmt.Errorf("info isn't a %T", info)
	}

	if size, ok = info["size"].(float64); !ok {
		return fmt.Errorf("size isn't a %T", size)
	}

	if name, ok = content.Raw["body"].(string); !ok {
		return fmt.Errorf("name isn't a %T", name)
	}

	if msgtype, ok = content.Raw["msgtype"].(string); !ok {
		return fmt.Errorf("msgtype isn't a %T", msgtype)
	}

	if mtype, ok = info["mimetype"].(string); !ok {
		return fmt.Errorf("mtype isn't a %T", mtype)
	}

	// check if we have an image uploaded without extension
	if !strings.Contains(name, ".") {
		if msgtype == "m.image" {
			mext, _ := mime.ExtensionsByType(mtype)
			if len(mext) > 0 {
				name += mext[0]
			}
		} else {
			// just a default .png extension if we don't have mime info
			name += ".png"
		}
	}

	// TODO: add attachment ID?
	err := b.AddAttachmentFromURL(rmsg, name, "", "", url)
	if err != nil {
		return err
	}
	return nil
}

// handleUploadFiles handles native upload of files.
func (b *Bmatrix) handleUploadFiles(msg *config.Message, roomID id.RoomID) (string, error) {
	for _, f := range msg.Extra["file"] {
		if fi, ok := f.(config.FileInfo); ok {
			b.handleUploadFile(msg, roomID, &fi)
		}
	}
	return "", nil
}

// handleUploadFile handles native upload of a file.
//
//nolint:funlen // This function is necessarily long because it is an event handler
func (b *Bmatrix) handleUploadFile(msg *config.Message, roomID id.RoomID, fi *config.FileInfo) {
	username := newMatrixUsername(msg.Username)
	content := bytes.NewReader(*fi.Data)
	sp := strings.Split(fi.Name, ".")
	mtype := mime.TypeByExtension("." + sp[len(sp)-1])
	// image and video uploads send no username, we have to do this ourself here #715
	err := b.retry(func() error {
		content := event.MessageEventContent{
			MsgType:       event.MsgText,
			Body:          username.plain + fi.Comment,
			FormattedBody: username.formatted + fi.Comment,
			Format:        event.FormatHTML,
		}

		_, err2 := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

		return err2
	})
	if err != nil {
		b.Log.Errorf("file comment failed: %#v", err)
	}

	b.Log.Debugf("uploading file: %s %s", fi.Name, mtype)

	var res *mautrix.RespMediaUpload

	err = b.retry(func() error {
		media := mautrix.ReqUploadMedia{
			Content:       content,
			ContentType:   mtype,
			ContentLength: int64(len(*fi.Data)),
		}

		var err2 error

		res, err2 = b.mc.UploadMedia(context.TODO(), media)

		return err2
	})

	if err != nil {
		b.Log.Errorf("file upload failed: %#v", err)
		return
	}

	switch {
	case strings.Contains(mtype, "video"):
		b.Log.Debugf("sendVideo %s", res.ContentURI)
		err = b.retry(func() error {
			content := event.MessageEventContent{
				MsgType:  event.MsgVideo,
				FileName: fi.Name,
				URL:      id.ContentURIString(res.ContentURI.String()),
			}

			_, err2 := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

			return err2
		})
		if err != nil {
			b.Log.Errorf("sendVideo failed: %#v", err)
		}
	case strings.Contains(mtype, "image"):
		b.Log.Debugf("sendImage %s", res.ContentURI)

		cfg, format, err2 := image.DecodeConfig(bytes.NewReader(*fi.Data))
		if err2 != nil {
			b.Log.WithError(err2).Errorf("Failed to decode image %s", fi.Name)
			return
		}

		b.Log.Debugf("Image format detected: %s (%dx%d)", format, cfg.Width, cfg.Height)

		img := event.MessageEventContent{
			MsgType: event.MsgImage,
			Body:    fi.Name,
			URL:     id.ContentURIString(res.ContentURI.String()),
			Info: &event.FileInfo{
				MimeType: mtype,
				Size:     len(*fi.Data),
				Width:    cfg.Width,  // #nosec G115 -- go std will not returned negative size
				Height:   cfg.Height, // #nosec G115 -- go std will not returned negative size
			},
		}

		err = b.retry(func() error {
			_, err = b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, img)
			return err
		})
		if err != nil {
			b.Log.Errorf("sendImage failed: %#v", err)
		}
//	case strings.Contains(mtype, "audio"):
//		b.Log.Debugf("sendAudio %s", res.ContentURI)
//		err = b.retry(func() error {
//			content := event.MessageEventContent{
//				MsgType:  event.MsgAudio,
//				FileName: fi.Name,
//				URL:      id.ContentURIString(res.ContentURI.String()),
//				Info: &event.FileInfo{
//					MimeType: mtype,
//					Size:     len(*fi.Data),
//				},
//			}
//
//			_, err2 := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)
//
//			return err2
//		})
//		if err != nil {
//			b.Log.Errorf("sendAudio failed: %#v", err)
//		}
	default:
		b.Log.Debugf("sendFile %s", res.ContentURI)
		err = b.retry(func() error {
			content := event.MessageEventContent{
				MsgType:  event.MsgFile,
				FileName: fi.Name,
				URL:      id.ContentURIString(res.ContentURI.String()),
				Info: &event.FileInfo{
					MimeType: mtype,
					Size:     len(*fi.Data),
				},
			}

			_, err2 := b.mc.SendMessageEvent(context.TODO(), roomID, event.EventMessage, content)

			return err2
		})
		if err != nil {
			b.Log.Errorf("sendFile failed: %#v", err)
		}
	}
	b.Log.Debugf("result: %#v", res)
}
