package bxmpp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

// UploadBufferEntry is data stored between requesting an upload,
// and actually performing the upload.
type UploadBufferEntry struct {
	FileInfo    *config.FileInfo // Data received from other bridges
	Mime        string           // Mimetype for the file upload
	Description string           // Raw comment without authorship
	Text        string           // Computed comment (including authorship) for the upload
	To          string           // Room to send the upload announcement once completed
}

type Bxmpp struct {
	*bridge.Config

	startTime time.Time
	xc        *xmpp.Client
	xmppMap   map[string]string
	connected bool
	sync.RWMutex

	avatarAvailability map[string]bool
	avatarMap          map[string]string

	// The account's HTTP [upload component](https://xmpp.org/extensions/xep-0363.html#disco)
	// is discovered in steps commented HTTP_UPLOAD_DISCO.
	httpUploadComponent string
	// The max attachment size is discovered in the last step of HTTP_UPLOAD_DISCO.
	httpUploadMaxSize int64
	// Files are stored in this buffer so we can perform the uploads asynchronously
	// without blocking the main thread:
	//
	// - request an upload slot and store the file in the buffer (HTTP_UPLOAD_SLOT step 1)
	// - (matterbridge processes other messages)
	// - receive the upload slot and perform the HTTP upload (HTTP_UPLOAD_SLOT step 2)
	// - (matterbridge processes other messages)
	// - receive upload confirmation and post the OOB URL (HTTP_UPLOAD_SLOT step 3)
	//
	// Note that in most cases, remote bridges will provide an attachment URL, no file
	// will actually be uploaded on XMPP side, and this buffer will be untouched.
	httpUploadBuffer map[string]*UploadBufferEntry
}

func New(cfg *bridge.Config) bridge.Bridger {
	return &Bxmpp{
		Config:             cfg,
		xmppMap:            make(map[string]string),
		avatarAvailability: make(map[string]bool),
		avatarMap:          make(map[string]string),
		httpUploadBuffer:   make(map[string]*UploadBufferEntry),
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
		// HandleExtra will produce error messages to be printed in the chat
		// when the attachments are too big.
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
			b.handleUploadFile(&msg)
			return "", nil
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
	// TODO: remove in release after first community fork release (N+2)
	if b.GetBool("NoTLS") {
		b.Log.Fatalf("NoTLS setting has been deprecated. If you'd like to disable StartTLS and start a plaintext connection, use NoStartTLS instead.")
	}

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
		NoTLS:                        !b.GetBool("UseDirectTLS"),
		StartTLS:                     !b.GetBool("NoStartTLS"),
		TLSConfig:                    tc,
		Debug:                        b.GetBool("debug"),
		Session:                      true,
		Status:                       "",
		StatusMessage:                "",
		Resource:                     "",
		InsecureAllowUnencryptedAuth: !b.GetBool("UseDirectTLS") && b.GetBool("NoStartTLS"),
		DebugWriter:                  b.Log.Writer(),
		Mechanism:                    b.GetString("Mechanism"),
		NoPLAIN:                      b.GetBool("NoPLAIN"),
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
			//
			// HTTP_UPLOAD_DISCO step 2
			for _, item := range v.Items {
				_, err := b.xc.DiscoverInfo(item.Jid)
				if err != nil {
					b.Log.WithError(err).Warnf("Failed to disco info from %s", item.Jid)
				}
			}
		case xmpp.DiscoResult:
			// Received disco info about a specific item, most likely from trying
			// to find the HTTP upload server.
			for _, identity := range v.Identities {
				if identity.Type != "file" || identity.Category != "store" {
					// Filter out disco info about everything else.
					continue
				}

				// HTTP_UPLOAD_DISCO step 3
				foundSize := b.extractMaxSizeFromX(&v.X)

				b.Log.Debugf("Found HTTP file upload component %s (maximum size: %d)", v.From, foundSize)
				b.Lock()
				b.httpUploadComponent = v.From
				b.httpUploadMaxSize = foundSize
				b.Unlock()
			}
		case xmpp.Slot:
			// HTTP_UPLOAD_SLOT step 2
			b.Log.Debugf("Received upload slot ID %s", v.ID)
			b.Lock()
			entry, ok := b.httpUploadBuffer[v.ID]
			b.Unlock()

			if !ok {
				b.Log.Warnf("Received upload slot ID %s doesn't match a known file", v.ID)
				continue
			}

			b.Log.Debugf("Preparing to upload file %s to %s", entry.FileInfo.Name, v.Put.Url)

			go func() {
				headers := make(map[string]string)
				headers["Content-Type"] = entry.Mime

				for _, h := range v.Put.Headers {
					switch h.Name {
					case "Authorization", "Cookie", "Expires":
						b.Log.Debugf("Setting header %s to %s", h.Name, h.Value)
						headers[h.Name] = h.Value
					default:
						b.Log.Warnf("Unknown header from HTTP upload component: %s: %s", h.Name, h.Value)
					}
				}

				err := b.HttpUpload(http.MethodPut, v.Put.Url, headers, entry.FileInfo.Data, []int{http.StatusOK, http.StatusCreated})
				if err != nil {
					b.Log.WithError(err).Warnf("Failed to upload file %s", entry.FileInfo.Name)
				}

				// Actually perform the chat announcement
				// HTTP_UPLOAD_SLOT step 3
				b.announceUploadedFile(entry.To, entry.Text, entry.Description, v.Get.Url)
			}()
		}
	}
}

func (b *Bxmpp) replaceAction(text string) (string, bool) {
	if strings.HasPrefix(text, "/me ") {
		return strings.ReplaceAll(text, "/me ", ""), true
	}
	return text, false
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
	//
	// HTTP_UPLOAD_DISCO step 1
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
