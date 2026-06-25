package birc

import (
	"crypto/tls"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lrstanley/girc"
	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	stripmd "github.com/writeas/go-strip-markdown"

	// We need to import the 'data' package as an implicit dependency.
	// See: https://godoc.org/github.com/paulrosania/go-charset/charset
	_ "github.com/paulrosania/go-charset/data"
)

type Birc struct {
	i                                         *girc.Client
	Nick                                      string
	names                                     map[string][]string
	namesMu                                   sync.RWMutex // protect the names map from concurrent write attempts
	connected                                 chan error
	Local                                     chan config.Message // local queue for flood control
	FirstConnection, authDone, botModeDone    bool
	caseMapDone, prefixDone, relayMsgDone     bool
	maxLen                                    int // MaxEventLength setting queried from girc, set after connecting
	MessageDelay, MessageQueue, MessageLength int
	MessagePrefix                             int                     // subtracted from MessageLength, set on channel join
	channels                                  map[string]bool         // list of channels to join, in case we need an invite
	channelsMu                                sync.RWMutex            // to protect the channels map
	channelsChan                              chan config.ChannelInfo // for async irc channel joins
	Casemapping                               string                  // current setting for the CASEMAPPING token in use
	RelayMsgSep                               string                  // current setting for the Relaymsg separator(s)
	CasemapFailures                           int                     // Count of casemapping errors
	RelayMsgFailures                          int                     // Count of general relaymsg errors

	*bridge.Config
}

// Work around girc using max prefix length instead of actual prefix length
// we do the check to extend b.MessageLength when the server supports it, in handleJoinPartPrefix in handlers.go
const defaultMaxPrefix = 115 // 30 + 18 + 63 + 4 from girc's event.go

func New(cfg *bridge.Config) bridge.Bridger {
	b := &Birc{}
	b.Config = cfg
	b.Nick = b.GetString("Nick")
	b.connected = make(chan error)
	b.names = make(map[string][]string)
	b.channels = make(map[string]bool)

	if b.GetInt("MessageDelay") == 0 {
		b.MessageDelay = 1300
	} else {
		b.MessageDelay = b.GetInt("MessageDelay")
	}

	if b.GetInt("MessageQueue") == 0 {
		b.MessageQueue = 30
	} else {
		b.MessageQueue = b.GetInt("MessageQueue")
	}

	if b.GetInt("MessageLength") == 0 {
		b.MessageLength = 512 // default per RFC 2812
	} else {
		b.MessageLength = b.GetInt("MessageLength")
	}

	b.CasemapFailures = 0
	b.RelayMsgFailures = 0

	if b.GetBool("UseRelayMsg") && b.GetBool("Colornicks") {
		b.Log.Warn("UseRelayMsg and Colornicks settings are not compatible with each other!")
		b.Log.Warn("Please choose one or the other for each bridge.")
		b.Log.Warnf("Overriding Colornicks and defaulting to UseRelayMsg only on: %s", b.Account)
		b.SetBool("Colornicks", false)
	}

	if !b.IsKeySet("UseRelayFallback") {
		b.SetBool("UseRelayFallback", true)
	}

	b.FirstConnection = true

	return b
}

func (b *Birc) Connect() error {
	if b.GetBool("UseSASL") && b.GetString("TLSClientCertificate") != "" {
		return errors.New("you can't enable SASL and TLSClientCertificate at the same time")
	}

	b.Local = make(chan config.Message, b.MessageQueue+10)
	b.channelsChan = make(chan config.ChannelInfo, b.MessageQueue+10)
	b.Log.Infof("Connecting %s", b.GetString("Server"))

	i, err := b.getClient()
	if err != nil {
		return err
	}

	b.authDone = false
	b.botModeDone = false
	b.prefixDone = false
	b.caseMapDone = false
	b.relayMsgDone = false

	if b.GetBool("UseSASL") {
		i.Config.SASL = &girc.SASLPlain{
			User: b.GetString("NickServNick"),
			Pass: b.GetString("NickServPassword"),
		}
	}

	// TODO: Rework these so that any of the ones which sleep() don't step on each other when backgrounded.
	i.Handlers.Add(girc.RPL_WELCOME, b.handleNewConnection)
	i.Handlers.Add(girc.RPL_ENDOFMOTD, b.handleOtherAuth)
	i.Handlers.Add(girc.ERR_NOMOTD, b.handleOtherAuth)
	i.Handlers.Add(girc.ALL_EVENTS, b.handleOther)
	b.i = i

	go b.doJoin()
	go b.doSend()
	go b.doConnect()

	go func() {
		// Block until something happens...
		<-b.connected

		b.Log.Info("Connection succeeded for bridge " + b.Account)
		b.FirstConnection = false
		if b.GetInt("DebugLevel") == 0 {
			b.i.Handlers.Clear(girc.ALL_EVENTS)
		}
	}()

	return nil
}

func (b *Birc) Disconnect() error {
	b.i.Close()
	close(b.Local)
	close(b.channelsChan)

	b.authDone = false
	b.botModeDone = false
	b.prefixDone = false
	b.caseMapDone = false
	b.relayMsgDone = false

	if b.CasemapFailures > 1 && b.Casemapping == CM_ASCII { // Maybe something got fixed, let's reset
		b.CasemapFailures = 0
	}

	if b.RelayMsgFailures > 0 {
		b.Log.Debugf("RelayMsgFailures count: %d Current separator list: %s", b.RelayMsgFailures, b.RelayMsgSep)
		b.RelayMsgFailures = 0
	}

	return nil
}

func (b *Birc) JoinChannel(channel config.ChannelInfo) error {
	b.channelsChan <- channel

	return nil
}

func (b *Birc) Send(msg config.Message) (string, error) {
	// Note: charset handling for an irc destination bridge has been moved to doSend()
	// ignore delete messages
	if msg.Event == config.EventMsgDelete {
		return "", nil
	}

	b.Log.Debugf("=> Receiving %#v", msg)

	// Don't make requests to the irc library in the main thread, as it might take out a lock for ages.
	// Instead, let's check b.authDone in doSend()
	//
	// we can be in between reconnects #385
	// if !b.i.IsConnected() {
	//	b.Log.Error("Not connected to server, dropping message")
	//	return "", nil
	// }

	if !b.prefixDone { // we haven't joined a channel yet.  drop the message
		return "", nil
	}

	// TODO: Put all of the following function calls into their own goroutines using a sync.WaitGroup
	// so that they can run concurrently, and also reduce the likelihood of out-of-order message processing
	// from happening.  More than one of the relevant code blocks has the possibility
	// of doing several writes to the b.Local queue.
	prefix := b.handlePrefix(&msg)

	// handle files, return if we're done here
	if ok := b.handleFiles(&msg); ok {
		return "", nil
	}

	if b.GetBool("StripMarkdown") {
		msg.Text = stripmd.Strip(msg.Text)
	}

	// Execute a command
	// TODO: Commands to retry obtaining a collided nick, recheck server capabilities, etc.
	// Authorization for certain commands could be handled by only allowing them to be
	// received from a particular channel, such as one that the matterbridge admin controls.
	if strings.HasPrefix(msg.Text, "!") {
		if msg.Event == config.EventNoticeIRC { // irc bots aren't supposed to respond to notices
			b.Log.Debugf("Got a NOTICE with a command in it.  Event: %#v", msg)
			return "", nil
		}

		result := b.handleCommand(&msg)
		if result != "" { // The "!users" command is all that ships with matterbridge right now, but here's a stub
			b.Log.Debugf("Command result for %s: %s", msg.Text, result)
			// Depending on what the command was, we may want to return here, instead of relaying the command
		}
	}

	// TODO: Implement ircv3 draft/multiline capabilities.
	// b.MessageLength will still correspond to the LINELEN token from RPL_ISUPPORT, which still defaults to 512,
	// even when draft/multiline is enabled.  We'll also need to handle max-bytes and max-lines values for multiline
	//
	// For now, we'll repurpose the MessageSplit setting to hand off the whole message to girc when set to false.
	if b.GetBool("MessageSplit") {
		msgLines := helper.GetSubLinesWords(msg.Text, b.MessageLength-prefix, b.GetString("MessageClipped"))
		for i := range msgLines {
			if len(b.Local) >= b.MessageQueue {
				b.Log.Debugf("flooding, dropping message (queue at %d)", len(b.Local))
				return "", nil
			}

			msg.Text = msgLines[i]

			b.Local <- msg
		}
	} else { // Not splitting messages.  Hopefully girc does it, or else the server might silently drop it
		if len(msg.Text)+prefix > (b.maxLen + defaultMaxPrefix) {
			b.Log.Warn("Warning: Large message possibly dropped instead of sent")
		}

		b.Local <- msg
	}
	// TODO: support for ircv3 msgid's
	return "", nil
}

func (b *Birc) doJoin() {
	defer b.ircHandlePanic()

	rate := time.Millisecond * time.Duration(b.MessageDelay)
	throttle := time.NewTicker(rate)
	for channel := range b.channelsChan {
		for !b.authDone { // need to check if we have nickserv auth done before joining channels
			time.Sleep(time.Second)
		}

		b.channelsMu.Lock()
		b.channels[channel.Name] = true
		b.channelsMu.Unlock()

		<-throttle.C
		if channel.Options.Key != "" {
			b.Log.Debugf("using key %s for channel %s", channel.Options.Key, channel.Name)
			b.i.Cmd.JoinKey(channel.Name, channel.Options.Key)
		} else {
			b.i.Cmd.Join(channel.Name)
		}
	}
}

func (b *Birc) doConnect() {
	for {
		// TODO: support connecting using a proxy
		// Since we're doing connections and joins asynchronously now, we can afford a generous timeout here
		err := b.i.DialerConnect(&net.Dialer{Timeout: 500 * time.Second})
		if err != nil {
			b.Log.Errorf("disconnect: error: %s", err)
			if b.FirstConnection {
				// try again
				// this will completely spam the log if the server port is closed, so sleep a bit
				time.Sleep(2 * time.Second)
				continue
			}
		} else {
			b.Log.Info("disconnect: client requested quit")
		}

		b.authDone = false
		b.botModeDone = false
		b.caseMapDone = false
		b.prefixDone = false
		b.relayMsgDone = false

		b.CasemapFailures = 0
		b.RelayMsgFailures = 0

		b.Log.Info(b.Account + " reconnecting in 60 seconds...")
		time.Sleep(60 * time.Second) // Sleep 60 seconds so as not to regress 42wim#267
		b.i.Handlers.Clear(girc.RPL_WELCOME)
		b.i.Handlers.Add(girc.RPL_WELCOME, func(client *girc.Client, event girc.Event) {
			b.Remote <- config.Message{Username: "system", Text: "rejoin", Channel: "", Account: b.Account, Event: config.EventRejoinChannels}
			// set our correct nick on reconnect if necessary
			b.Nick = event.Source.Name
		})
	}
}

func (b *Birc) doSend() {
	rate := time.Millisecond * time.Duration(b.MessageDelay)
	throttle := time.NewTicker(rate)
	for msg := range b.Local {
		if !b.authDone || !b.prefixDone {
			// If we're not logged in or joined to any channel yet, discard the message
			continue
		}

		// convert to specified charset
		err := b.handleCharset(&msg)
		if err != nil {
			b.Log.Warn("Error converting to charset")
		}

		<-throttle.C
		username := msg.Username
		// Optional support for the proposed RELAYMSG extension, described at
		// https://github.com/jlu5/ircv3-specifications/blob/master/extensions/relaymsg.md
		// nolint:nestif
		if b.GetBool("UseRelayMsg") { // Let's check this by itself first.
			// Avoid needlessly querying the irc lib on each msg, in case it takes out any locks
			if b.i.HasCapability("overdrivenetworks.com/relaymsg") || b.i.HasCapability("draft/relaymsg") {
				// nick is now sanitized in gateway.go
				text := msg.Text

				// Work around girc chomping leading commas on single word messages?
				if b.GetBool("DoubleColonPrefix") {
					if strings.HasPrefix(text, ":") && !strings.ContainsRune(text, ' ') {
						b.Log.Warn("This option may be deprecated in the future. If you are using it, please help us understand the usecase by commenting on this issue: https://github.com/matterbridge-org/matterbridge/issues/122")

						text = ":" + text
					}
				} else {
					// TODO: This is so spammy when using RelayMsg.  Remove it at first opportunity
					b.Log.Debug("Leading colon workaround has been disabled; reenable it with `DoubleColonPrefix=true`.")
				}

				if msg.Event == config.EventUserAction {
					if !b.GetBool("MessageSplit") {
						err := b.i.Cmd.SendRawf("RELAYMSG %s %s :\x01ACTION %s\x01", msg.Channel, username, text)
						if err != nil {
							b.Log.Warn("Error in SendRawf")
						}
					} else {
						cmdline := fmt.Sprintf("RELAYMSG %s %s :\x01ACTION %s\x01\r\n", msg.Channel, username, text)
						err := b.i.Cmd.SendRawNoSplit(cmdline)
						if err != nil {
							b.Log.Warn("Error in SendRawNoSplit")
						}
					}
				} else {
					b.Log.Debugf("Sending RELAYMSG to channel %s: nick=%s", msg.Channel, username)
					if !b.GetBool("MessageSplit") {
						err := b.i.Cmd.SendRawf("RELAYMSG %s %s :%s", msg.Channel, username, text)
						if err != nil {
							b.Log.Warn("Error in SendRawf")
						}
					} else {
						cmdline := fmt.Sprintf("RELAYMSG %s %s :%s\r\n", msg.Channel, username, text)
						err := b.i.Cmd.SendRawNoSplit(cmdline)
						if err != nil {
							b.Log.Warn("Error in SendRawNoSplit")
						}
					}
				}
				continue // fix for #235
			} else { // We have UseRelayMsg set but lack the capability.  Log a warning
				b.Log.Warn("WARNING!  UseRelayMsg was set, but the irc server does not support it.")
			}
		}

		var cmdline string
		switch msg.Event {
		case config.EventUserAction:
			cmdline = fmt.Sprintf("PRIVMSG %s :\x01ACTION %s\x01", msg.Channel, username+msg.Text)
			b.Log.Debugf("Sending action to channel %s", msg.Channel)
		case config.EventNoticeIRC:
			cmdline = fmt.Sprintf("NOTICE %s :%s", msg.Channel, username+msg.Text)
			b.Log.Debugf("Sending notice to channel %s", msg.Channel)
		default:
			cmdline = fmt.Sprintf("PRIVMSG %s :%s", msg.Channel, username+msg.Text)
			b.Log.Debugf("Sending to channel %s", msg.Channel)
		}

		if !b.GetBool("MessageSplit") {
			err := b.i.Cmd.SendRaw(cmdline)
			if err != nil {
				b.Log.Warn("Error in SendRaw")
			}
		} else {
			err := b.i.Cmd.SendRawNoSplit(cmdline + "\r\n")
			if err != nil {
				b.Log.Warn("Error in SendRawNoSplit")
			}
		}
	}
}

// validateInput validates the server/port/nick configuration. Returns a *girc.Client if successful
func (b *Birc) getClient() (*girc.Client, error) {
	server, portstr, err := net.SplitHostPort(b.GetString("Server"))
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portstr)
	if err != nil {
		return nil, err
	}
	user := b.GetString("UserName")
	if user == "" {
		user = b.GetString("Nick")
	}
	// fix strict user handling of girc
	for !girc.IsValidUser(user) {
		if len(user) == 1 || len(user) == 0 {
			user = "matterbridge"
			break
		}
		user = user[1:]
	}
	realName := b.GetString("RealName")
	if realName == "" {
		realName = b.GetString("Nick")
	}

	debug := io.Discard
	if b.GetInt("DebugLevel") == 2 {
		debug = b.Log.Writer()
	}

	pingDelay, err := time.ParseDuration(b.GetString("pingdelay"))
	if err != nil || pingDelay == 0 {
		pingDelay = time.Minute
	}

	b.Log.Debugf("setting pingdelay to %s", pingDelay)

	tlsConfig, err := b.getTLSConfig()
	if err != nil {
		return nil, err
	}

	i := girc.New(girc.Config{
		Server:     server,
		ServerPass: b.GetString("Password"),
		Port:       port,
		Nick:       b.GetString("Nick"),
		User:       user,
		Name:       realName,
		SSL:        b.GetBool("UseTLS"),
		Bind:       b.GetString("Bind"),
		TLSConfig:  tlsConfig,
		PingDelay:  pingDelay,
		// skip gIRC internal rate limiting, since we have our own throttling
		// but if we aren't splitting, then set false.
		// don't risk sending huge messages that girc may end up splitting too fast
		AllowFlood:    b.GetBool("MessageSplit"),
		Debug:         debug,
		SupportedCaps: map[string][]string{"overdrivenetworks.com/relaymsg": nil, "draft/relaymsg": nil},
	})

	return i, nil
}

func (b *Birc) endNames(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) < 2 {
		b.Log.Debugf("RPL_ENDOFNAMES with less than 2 params? Event: %#v", event)
		return
	}

	channel := event.Params[1]

	// Redid this to be concurrency-safe now that irc bridges are increasingly async
	b.namesMu.RLock()

	mynames := make([]string, len(b.names[channel]))
	copy(mynames, b.names[channel])

	b.namesMu.RUnlock()

	b.namesMu.Lock()
	b.names[channel] = nil
	b.namesMu.Unlock()

	b.i.Handlers.Clear(girc.RPL_NAMREPLY)
	b.i.Handlers.Clear(girc.RPL_ENDOFNAMES)

	// sort.Strings just calls slices.Sort of go 1.22
	slices.Sort(mynames)
	maxNamesPerPost := (300 / b.nicksPerRow()) * b.nicksPerRow()

	for len(mynames) > maxNamesPerPost {
		b.Remote <- config.Message{
			Username: b.Nick, Text: b.formatnicks(mynames[0:maxNamesPerPost]),
			Channel: channel, Account: b.Account,
		}

		mynames = mynames[maxNamesPerPost:]
	}
	b.Remote <- config.Message{
		Username: b.Nick, Text: b.formatnicks(mynames),
		Channel: channel, Account: b.Account,
	}
}

func (b *Birc) handleCommand(msg *config.Message) string {
	if msg.Text == "!users" { // Do not background these handlers, as they use locks now
		b.i.Handlers.Add(girc.RPL_NAMREPLY, b.storeNames)
		b.i.Handlers.Add(girc.RPL_ENDOFNAMES, b.endNames)

		err := b.i.Cmd.SendRaw("NAMES " + msg.Channel)
		if err != nil {
			b.Log.Debugf("Error in SendRaw for NAMES command: %s", err)
			b.i.Handlers.Clear(girc.RPL_NAMREPLY)
			b.i.Handlers.Clear(girc.RPL_ENDOFNAMES)
		}
	}

	return ""
}

// Count the size of the prefix for message splitting, while also applying the Colornicks setting (if set).
// Always check that b.prefixDone is true before calling this.
func (b *Birc) handlePrefix(msg *config.Message) int {
	prefix := b.MessagePrefix + len(msg.Username) + len(msg.Channel)

	// account for spaces, command names, and other padding
	// TODO: make these len()'s into constants?  But the go compiler does that anyway, so no performance loss here
	switch {
	case b.GetBool("UseRelayMsg"):
		switch msg.Event {
		case config.EventUserAction:
			prefix += len("RELAYMSG   :\x01ACTION \x01")
		default:
			prefix += len("RELAYMSG   :")
		}
	case msg.Event == config.EventUserAction:
		prefix += len("PRIVMSG  :\x01ACTION \x01")
	case msg.Event == config.EventNoticeIRC:
		prefix += len("NOTICE  :")
	default:
		prefix += len("PRIVMSG  :")
	}

	if !b.GetBool("UseRelayMsg") && b.GetBool("Colornicks") {
		// Separate colors for different fields (label, proto, nick, etc)
		userslice := strings.FieldsFunc(msg.Username, func(r rune) bool {
			return r == '\u0020' // split only on regular space; ignore NBSP, tab, newline
		})
		username := ""

		for i := range userslice {
			checksum := crc32.ChecksumIEEE([]byte(userslice[i]))
			colorCode := checksum%14 + 2 // prevent white or black color codes
			username += fmt.Sprintf("\x03%02d%s\x0F ", colorCode, userslice[i])
			prefix += 5 // we've just added four bytes and a space
		}

		msg.Username = username
	}

	return prefix
}

// TODO: Add a check for any locks still active, for debug-mode only
// We may or may not want to override the Bridge.handlePanic() method instead, TBD
func (b *Birc) ircHandlePanic() {
	rec := recover()
	if rec != nil {
		b.Log.Warnf("Recovered from panic: %#v", rec)
	}
}

func (b *Birc) skipPrivMsg(event girc.Event) bool {
	// Our nick can be changed
	b.Nick = b.i.GetNick()

	// freenode doesn't send 001 as first reply
	if event.Command == "NOTICE" && len(event.Params) != 2 {
		return true
	}
	// don't forward queries to the bot
	if event.Params[0] == b.Nick {
		return true
	}
	// don't forward message from ourself
	if event.Source != nil {
		if event.Source.Name == b.Nick {
			return true
		}
	}
	// don't forward messages we sent via RELAYMSG
	if relayedNick, ok := event.Tags.Get("draft/relaymsg"); ok && relayedNick == b.Nick {
		return true
	}
	// This is the old name of the cap sent in spoofed messages; I've kept this in
	// for compatibility reasons
	if relayedNick, ok := event.Tags.Get("relaymsg"); ok && relayedNick == b.Nick {
		return true
	}
	return false
}

func (b *Birc) nicksPerRow() int {
	return 4
}

func (b *Birc) storeNames(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) < 3 {
		b.Log.Debugf("NAMES reply with less than 3 Params? Event: %#v", event)
		return
	}

	channel := event.Params[2]

	b.namesMu.RLock()
	appended := append(b.names[channel], strings.Split(strings.TrimSpace(event.Last()), " ")...) //nolint:gocritic
	b.namesMu.RUnlock()

	b.namesMu.Lock()
	b.names[channel] = appended
	b.namesMu.Unlock()
}

func (b *Birc) formatnicks(nicks []string) string {
	return strings.Join(nicks, ", ") + " currently on IRC"
}

func (b *Birc) getTLSConfig() (*tls.Config, error) {
	server, _, _ := net.SplitHostPort(b.GetString("server"))

	tlsConfig := &tls.Config{
		InsecureSkipVerify: b.GetBool("skiptlsverify"), //nolint:gosec
		ServerName:         server,
	}

	if filename := b.GetString("TLSClientCertificate"); filename != "" {
		cert, err := tls.LoadX509KeyPair(filename, filename)
		if err != nil {
			return nil, err
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
