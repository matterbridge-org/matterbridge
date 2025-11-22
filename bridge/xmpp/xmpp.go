package bxmpp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jpillora/backoff"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/rs/xid"
	"github.com/xmppo/go-xmpp"
)

type Bxmpp struct {
	*bridge.Config

	startTime time.Time
	xc        *xmpp.Client
	xmppMap   map[string]string
	connected bool
	sync.RWMutex

	avatarAvailability map[string]bool
	avatarMap          map[string]string

	httpUploadComponent string
	httpUploadMaxSize   int64
	httpUploadBuffer    map[string]*config.FileInfo
}

func New(cfg *bridge.Config) bridge.Bridger {
	return &Bxmpp{
		Config:             cfg,
		xmppMap:            make(map[string]string),
		avatarAvailability: make(map[string]bool),
		avatarMap:          make(map[string]string),
		httpUploadBuffer:   make(map[string]*config.FileInfo),
	}
}

func (b *Bxmpp) Connect() error {
	b.Log.Infof("Connecting %s", b.GetString("Server"))
	if err := b.createXMPP(); err != nil {
		b.Log.Debugf("%#v", err)
		return err
	}

	b.Log.Info("Connection succeeded")
	go b.manageConnection()
	return nil
}

func (b *Bxmpp) Disconnect() error {
	return nil
}

func (b *Bxmpp) JoinChannel(channel config.ChannelInfo) error {
	if channel.Options.Key != "" {
		b.Log.Debugf("using key %s for channel %s", channel.Options.Key, channel.Name)
		b.xc.JoinProtectedMUC(channel.Name+"@"+b.GetString("Muc"), b.GetString("Nick"), channel.Options.Key, xmpp.NoHistory, 0, nil)
	} else {
		b.xc.JoinMUCNoHistory(channel.Name+"@"+b.GetString("Muc"), b.GetString("Nick"))
	}
	return nil
}

func (b *Bxmpp) Send(msg config.Message) (string, error) {
	// should be fixed by using a cache instead of dropping
	if !b.Connected() {
		return "", fmt.Errorf("bridge %s not connected, dropping message %#v to bridge", b.Account, msg)
	}
	// ignore delete messages
	if msg.Event == config.EventMsgDelete {
		return "", nil
	}

	b.Log.Debugf("=> Receiving %#v", msg)

	if msg.Event == config.EventAvatarDownload {
		return b.cacheAvatar(&msg), nil
	}

	// Make a action /me of the message, prepend the username with it.
	// https://xmpp.org/extensions/xep-0245.html
	if msg.Event == config.EventUserAction {
		msg.Username = "/me " + msg.Username
	}

	// Upload a file (in XMPP case send the upload URL because XMPP has no native upload support).
	var err error
	if msg.Extra != nil {
		for _, rmsg := range helper.HandleExtra(&msg, b.General) {
			b.Log.Debugf("=> Sending attachement message %#v", rmsg)
			if b.GetString("WebhookURL") != "" {
				err = b.postSlackCompatibleWebhook(msg)
			} else {
				_, err = b.xc.Send(xmpp.Chat{
					Type:   "groupchat",
					Remote: rmsg.Channel + "@" + b.GetString("Muc"),
					Text:   rmsg.Username + rmsg.Text,
				})
			}

			if err != nil {
				b.Log.WithError(err).Error("Unable to send message with share URL.")
			}
		}
		if len(msg.Extra["file"]) > 0 {
			return "", b.handleUploadFile(&msg)
		}
	}

	if b.GetString("WebhookURL") != "" {
		b.Log.Debugf("Sending message using Webhook")
		err := b.postSlackCompatibleWebhook(msg)
		if err != nil {
			b.Log.Errorf("Failed to send message using webhook: %s", err)
			return "", err
		}

		return "", nil
	}

	// Post normal message.
	b.Log.Debugf("=> Sending message %#v", msg)
	if _, err := b.xc.Send(xmpp.Chat{
		Type:   "groupchat",
		Remote: msg.Channel + "@" + b.GetString("Muc"),
		Text:   msg.Username + msg.Text,
	}); err != nil {
		return "", err
	}

	// Generate a dummy ID because to avoid collision with other internal messages
	// However this does not provide proper Edits/Replies integration on XMPP side.
	msgID := xid.New().String()
	return msgID, nil
}

func (b *Bxmpp) postSlackCompatibleWebhook(msg config.Message) error {
	type XMPPWebhook struct {
		Username string `json:"username"`
		Text     string `json:"text"`
	}
	webhookBody, err := json.Marshal(XMPPWebhook{
		Username: msg.Username,
		Text:     msg.Text,
	})
	if err != nil {
		b.Log.Errorf("Failed to marshal webhook: %s", err)
		return err
	}

	resp, err := http.Post(b.GetString("WebhookURL")+"/"+url.QueryEscape(msg.Channel), "application/json", bytes.NewReader(webhookBody))
	if err != nil {
		b.Log.Errorf("Failed to POST webhook: %s", err)
		return err
	}

	resp.Body.Close()
	return nil
}

func (b *Bxmpp) createXMPP() error {
	var serverName string
	switch {
	case !b.GetBool("Anonymous"):
		if !strings.Contains(b.GetString("Jid"), "@") {
			return fmt.Errorf("the Jid %s doesn't contain an @", b.GetString("Jid"))
		}
		serverName = strings.Split(b.GetString("Jid"), "@")[1]
	case !strings.Contains(b.GetString("Server"), ":"):
		serverName = strings.Split(b.GetString("Server"), ":")[0]
	default:
		serverName = b.GetString("Server")
	}

	tc := &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: b.GetBool("SkipTLSVerify"), // nolint: gosec
	}

	options := xmpp.Options{
		Host:                         b.GetString("Server"),
		User:                         b.GetString("Jid"),
		Password:                     b.GetString("Password"),
		NoTLS:                        true,
		StartTLS:                     !b.GetBool("NoTLS"),
		TLSConfig:                    tc,
		Debug:                        b.GetBool("debug"),
		Session:                      true,
		Status:                       "",
		StatusMessage:                "",
		Resource:                     "",
		InsecureAllowUnencryptedAuth: b.GetBool("NoTLS"),
		DebugWriter:                  b.Log.Writer(),
	}
	var err error
	b.xc, err = options.NewClient()
	return err
}

func (b *Bxmpp) manageConnection() {
	b.setConnected(true)
	initial := true
	bf := &backoff.Backoff{
		Min:    time.Second,
		Max:    5 * time.Minute,
		Jitter: true,
	}

	// Main connection loop. Each iteration corresponds to a successful
	// connection attempt and the subsequent handling of the connection.
	for {
		if initial {
			initial = false
		} else {
			b.Remote <- config.Message{
				Username: "system",
				Text:     "rejoin",
				Channel:  "",
				Account:  b.Account,
				Event:    config.EventRejoinChannels,
			}
		}

		if err := b.handleXMPP(); err != nil {
			b.Log.WithError(err).Error("Disconnected.")
			b.setConnected(false)
		}

		// Reconnection loop using an exponential back-off strategy. We
		// only break out of the loop if we have successfully reconnected.
		for {
			d := bf.Duration()
			b.Log.Infof("Reconnecting in %s.", d)
			time.Sleep(d)

			b.Log.Infof("Reconnecting now.")
			if err := b.createXMPP(); err == nil {
				b.setConnected(true)
				bf.Reset()
				break
			}
			b.Log.Warn("Failed to reconnect.")
		}
	}
}

func (b *Bxmpp) xmppKeepAlive() chan bool {
	done := make(chan bool)
	go func() {
		ticker := time.NewTicker(90 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.Log.Debugf("PING")
				if err := b.xc.PingC2S("", ""); err != nil {
					b.Log.Debugf("PING failed %#v", err)
				}
			case <-done:
				return
			}
		}
	}()
	return done
}

func (b *Bxmpp) handleXMPP() error {
	b.startTime = time.Now()

	done := b.xmppKeepAlive()
	defer close(done)

	for {
		m, err := b.xc.Recv()
		if err != nil {
			// An error together with AvatarData is non-fatal
			switch m.(type) {
			case xmpp.AvatarData:
				continue
			default:
				return err
			}
		}

		switch v := m.(type) {
		case xmpp.Chat:
			if v.Type == "groupchat" {
				b.Log.Debugf("== Receiving %#v", v)

				// Skip invalid messages.
				if b.skipMessage(v) {
					continue
				}

				var event string
				if strings.Contains(v.Text, "has set the subject to:") {
					event = config.EventTopicChange
				}

				available, sok := b.avatarAvailability[v.Remote]
				avatar := ""
				if !sok {
					b.Log.Debugf("Requesting avatar data")
					b.avatarAvailability[v.Remote] = false
					b.xc.AvatarRequestData(v.Remote)
				} else if available {
					avatar = getAvatar(b.avatarMap, v.Remote, b.General)
				}

				rmsg := config.Message{
					Username: b.parseNick(v.Remote),
					Text:     v.Text,
					Channel:  b.parseChannel(v.Remote),
					Account:  b.Account,
					Avatar:   avatar,
					UserID:   v.Remote,
					// Here the stanza-id has been set by the server and can be used to provide replies
					// as explained in XEP-0461 https://xmpp.org/extensions/xep-0461.html#business-id
					ID:    v.StanzaID.ID,
					Event: event,
					Extra: make(map[string][]any),
				}

				// Check if we have an action event.
				var ok bool
				rmsg.Text, ok = b.replaceAction(rmsg.Text)
				if ok {
					rmsg.Event = config.EventUserAction
				}

				if b.handleDownloadFile(&rmsg, &v) {
					continue
				}
				b.Log.Debugf("<= Sending message from %s on %s to gateway", rmsg.Username, b.Account)
				b.Log.Debugf("<= Message is %#v", rmsg)
				b.Remote <- rmsg
			}
		case xmpp.AvatarData:
			b.handleDownloadAvatar(v)
			b.avatarAvailability[v.From] = true
			b.Log.Debugf("Avatar for %s is now available", v.From)
		case xmpp.Presence:
			// Do nothing.
		case xmpp.DiscoItems:
			// Received a list of items, most likely from trying to find the HTTP upload server
			// Send a disco info query to all items to find out which is which
			for _, item := range v.Items {
				_, err := b.xc.DiscoverInfo(item.Jid)
				if err != nil {
					b.Log.WithError(err).Warnf("Failed to disco info from %s", item.Jid)
				}
			}
		case xmpp.DiscoResult:
			for _, identity := range v.Identities {
				if identity.Type != "file" || identity.Category != "store" {
					continue
				}

				foundSize := b.extractMaxSizeFromX(&v.X)

				b.Log.Debugf("Found HTTP file upload component %s (maximum size: %d)", v.From, foundSize)
				b.Lock()
				b.httpUploadComponent = v.From
				b.httpUploadMaxSize = foundSize
				b.Unlock()
			}
		case xmpp.Slot:
			b.Log.Debugf("Received upload slot ID %s", v.ID)
			b.Lock()
			entry, ok := b.httpUploadBuffer[v.ID]
			b.Unlock()

			if !ok {
				b.Log.Warnf("Received upload slot ID %s doesn't match a known file", v.ID)
				continue
			}

			b.Log.Debugf("Preparing to upload file %s to %s", entry.Name, v.Put.Url)
			// TODO: upload file to the upload slot, then share it in the chat
		}
	}
}

func (b *Bxmpp) replaceAction(text string) (string, bool) {
	if strings.HasPrefix(text, "/me ") {
		return strings.ReplaceAll(text, "/me ", ""), true
	}
	return text, false
}

// handleUploadFile handles native upload of files
// IMPORTANT NOTES:
//
// Some clients only display a preview when the body is exactly the URL, not only contains it.
// https://docs.modernxmpp.org/client/protocol/#communicating-the-url
//
// This is the case with Gajim/Conversations for example.
//
// This means we cannot have an actual description of the uploaded file, nor can we add
// information about who posted it... at least in the same message.
func (b *Bxmpp) handleUploadFile(msg *config.Message) error {
	var urlDesc string

	for _, file := range msg.Extra["file"] {
		fileInfo := file.(config.FileInfo)
		if fileInfo.Comment != "" {
			msg.Text += fileInfo.Comment + ": "
		}
		if fileInfo.URL != "" {
			msg.Text = fileInfo.URL
			if fileInfo.Comment != "" {
				msg.Text = fileInfo.Comment + ": " + fileInfo.URL
				urlDesc = fileInfo.Comment
			}
		}
		if _, err := b.xc.Send(xmpp.Chat{
			Type:   "groupchat",
			Remote: msg.Channel + "@" + b.GetString("Muc"),
			Text:   msg.Username + msg.Text,
		}); err != nil {
			return err
		}

		if fileInfo.URL != "" {
			// The file has a URL because it was uploaded to matterbridge's mediaserver

			// Send separate message with the username and optional file comment
			// because we can't have an attachment comment/description.
			_, err := b.xc.Send(xmpp.Chat{
				Type:   "groupchat",
				Remote: msg.Channel + "@" + b.GetString("Muc"),
				Text:   msg.Username + fileInfo.Comment,
			})
			if err != nil {
				b.Log.WithError(err).Warn("Failed to announce file sharer, not sharing file.")
				continue
			}

			if _, err := b.xc.SendOOB(xmpp.Chat{
				Type:    "groupchat",
				Remote:  msg.Channel + "@" + b.GetString("Muc"),
				Ooburl:  fileInfo.URL,
				Oobdesc: urlDesc,
			}); err != nil {
				b.Log.WithError(err).Warn("Failed to send share URL.")
				continue
			}
		} else {
			// The file received from other bridges is just a bunch of bytes in fileInfo.Data
			// We need to upload it to the XMPP server's HTTP upload component.
			// This is defined in XEP-0363: https://xmpp.org/extensions/xep-0363.html
			//
			// The steps are:
			//
			// 1. Find the server's attached upload XMPP component (done on login)
			// 2. Request an "upload slot" from the upload component (we are here)
			// 3. Send a PUT request with the data to the remote HTTP "upload slot" (when receiving the slot)
			fileId := xid.New().String()
			b.requestUploadSlot(fileId, &fileInfo)
		}
	}
	return nil
}

func (b *Bxmpp) parseNick(remote string) string {
	s := strings.Split(remote, "@")
	if len(s) > 1 {
		s = strings.Split(s[1], "/")
		if len(s) == 2 {
			return s[1] // nick
		}
	}
	return ""
}

func (b *Bxmpp) parseChannel(remote string) string {
	s := strings.Split(remote, "@")
	if len(s) >= 2 {
		return s[0] // channel
	}
	return ""
}

// skipMessage skips messages that need to be skipped
func (b *Bxmpp) skipMessage(message xmpp.Chat) bool {
	// skip messages from ourselves
	if b.parseNick(message.Remote) == b.GetString("Nick") {
		return true
	}

	// skip empty messages
	if message.Text == "" {
		return true
	}

	// skip subject messages
	if strings.Contains(message.Text, "</subject>") {
		return true
	}

	// do not show subjects on connect #732
	if strings.Contains(message.Text, "has set the subject to:") && time.Since(b.startTime) < time.Second*5 {
		return true
	}

	// skip delayed messages
	return !message.Stamp.IsZero() && time.Since(message.Stamp).Minutes() > 5
}

func (b *Bxmpp) setConnected(state bool) {
	b.Lock()
	b.connected = state
	b.Unlock()

	// We are now (re)connected, send a disco query to find out HTTP upload server
	// Ignore any errors encountered
	_, err := b.xc.DiscoverServerItems()
	if err != nil {
		b.Log.WithError(err).Warn("Failed to discover server items")
	}
}

func (b *Bxmpp) Connected() bool {
	b.RLock()
	defer b.RUnlock()
	return b.connected
}

// handleDownloadFile processes file downloads in the background.
//
// Returns true if the message was handled, false otherwise.
//
// This implements XEP-0066 https://xmpp.org/extensions/xep-0066.html
func (b *Bxmpp) handleDownloadFile(rmsg *config.Message, v *xmpp.Chat) bool {
	// Do we have an OOB attachment URL?
	if v.Oob.Url != "" {
		go func() {
			b.handleDownloadFileInner(rmsg, v)
		}()

		return true
	}

	return false
}

// handleDownloadFileInner is a helper to actually download a remote attachment
// and announce it to other bridges.
//
// It runs in the foreground, and should only be called in a background context
// to avoid stalling in the main thread.
//
// If it encounters any error, it will log the error and skip the message.
func (b *Bxmpp) handleDownloadFileInner(rmsg *config.Message, v *xmpp.Chat) {
	parsed_url, err := url.Parse(v.Oob.Url)
	if err != nil {
		b.Log.WithError(err).Warn("Failed to parse OOB URL")
		return
	}
	// We use the last part of the URL's path as filename. This prevents
	// errors from extra slashes, but might not make sense if for example
	// the URL is `/download?id=FOO`.
	// TODO: investigate popular URL naming schemes in XMPP world, or
	// consider naming the files after their own checksum.
	fileName := path.Base(parsed_url.Path)

	err = b.AddAttachmentFromURL(rmsg, fileName, "", "", v.Oob.Url)
	if err != nil {
		b.Log.WithError(err).Warn("Failed to download remote XMPP OOB attachment")
		return
	}

	b.Log.Debugf("<= Sending message from %s on %s to gateway", rmsg.Username, b.Account)
	b.Log.Debugf("<= Message is %#v", rmsg)

	b.Remote <- *rmsg
}

func (b *Bxmpp) extractMaxSizeFromX(disco_x *[]xmpp.DiscoX) int64 {
	for _, x := range *disco_x {
		for i, field := range x.Field {
			if field.Var == "max-file-size" {
				if i > 0 {
					if x.Field[i-1].Value[0] == "urn:xmpp:http:upload:0" {
						return b.extractMaxSizeFromXFieldValue(field.Value[0])
					}
				}
			}
		}
	}

	b.Log.Debug("No HTTP max upload size found")

	return 0
}

func (b *Bxmpp) extractMaxSizeFromXFieldValue(value string) int64 {
	maxFileSize, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		// If the max-file-size can't be parsed, assume it's 0
		// and log the error.
		b.Log.Errorf("Failed to parse HTTP max upload size: %s", value)
		return 0
	}

	return maxFileSize
}

func (b *Bxmpp) requestUploadSlot(fileId string, fileInfo *config.FileInfo) {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\+\-\_\.]+`)
	fileNameEscaped := reg.ReplaceAllString(fileInfo.Name, "_")

	// Guess the mime-type
	mimeType := mime.TypeByExtension(path.Ext(fileInfo.Name))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	b.Log.Debugf("Requesting upload slot ID %s for %s (escaped) with mime-type %s", fileId, fileNameEscaped, mimeType)

	request := fmt.Sprintf("<request xmlns='urn:xmpp:http:upload:0' filename='%s' size='%d' content-type='%s' />", fileNameEscaped, fileInfo.Size, mimeType)

	b.Lock()
	httpUploadComponent := b.httpUploadComponent
	b.Unlock()

	_, err := b.xc.RawInformation(b.xc.JID(), httpUploadComponent, fileId, "get", request)
	if err != nil {
		b.Log.WithError(err).Error("Failed to request upload slot")
		return
	}

	// Save the FileInfo in the buffer to actually upload it later
	// when we receive the upload slot.
	b.Lock()
	b.httpUploadBuffer[fileId] = fileInfo
	b.Unlock()
}
