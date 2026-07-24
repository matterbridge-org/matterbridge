package birc

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lrstanley/girc"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/matterbridge-org/matterbridge/bridge/helper"
	"github.com/paulrosania/go-charset/charset"
	"github.com/saintfish/chardet"

	// We need to import the 'data' package as an implicit dependency.
	// See: https://godoc.org/github.com/paulrosania/go-charset/charset
	_ "github.com/paulrosania/go-charset/data"
)

const (
	utf8charset    = "utf-8"
	errInvalidNick = "INVALID_NICK"
	cmdRelayMsg    = "RELAYMSG"
)

// this handler actually gets called when sending out to an IRC bridge, not when receiving a privmsg via the irc library.
// refer to handlePrivMsg() for the latter (including attempted autodetection when no charset is specified)
//
// If we received the message from another IRC bridge, the text should have already been converted to UTF-8.
// If from any other bridge type, no conversion is needed, as all other supported bridge types use UTF-8 exclusively.
//
// TODO: rework this using x/text/transform and x/text/encoding packages instead of go-charset
func (b *Birc) handleCharset(msg *config.Message) error {
	if b.GetString("Charset") != "autodetect" {
		switch b.GetString("Charset") {
		case "utf8", utf8charset:
			break
		default:
			buf := new(bytes.Buffer)
			w, err := charset.NewWriter(b.GetString("Charset"), buf)
			if err != nil {
				b.Log.Errorf("utf-8 to charset conversion failed: %s", err)
				return err
			}
			fmt.Fprint(w, msg.Text)
			w.Close()

			msg.Text = buf.String()
		}
	}

	return nil
}

// handleFiles returns true if we have handled the files, otherwise return false
func (b *Birc) handleFiles(msg *config.Message) bool {
	if msg.Extra == nil {
		return false
	}

	for _, rmsg := range helper.HandleExtra(msg, b.General) {
		b.Local <- rmsg
	}

	if len(msg.Extra["file"]) == 0 {
		return false
	}

	// We have some attachments, which may or may not have a caption
	// First, let's print the message body, if any
	if msg.Text != "" {
		b.Local <- config.Message{Text: msg.Text, Username: msg.Username, Channel: msg.Channel, Event: msg.Event}
	}

	for _, f := range msg.Extra["file"] {
		fi := f.(config.FileInfo)

		if fi.URL == "" {
			// IRC does not support raw bytes upload.
			//
			// Here we just produce an error to be announced and logged, hoping the
			// matterbridge operator will finally enable the media server.
			msg.Text = fmt.Sprintf("Could not share file %s (no mediaserver configured)", fi.Name)
			b.Local <- config.Message{Text: msg.Text, Username: "<matterbridge>", Channel: msg.Channel, Event: msg.Event}

			b.Log.Error(msg.Text)

			continue
		}

		// File has a public URL, either because it's provided by the remote bridge,
		// or because the media server is enabled. Share it alongside the
		// attachment caption, if any.
		if fi.Comment == "" {
			msg.Text = fi.URL
		} else {
			msg.Text = fi.Comment + " : " + fi.URL
		}

		b.Local <- config.Message{Text: msg.Text, Username: msg.Username, Channel: msg.Channel, Event: msg.Event}
	}

	return true
}

func (b *Birc) handleInvite(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) != 2 {
		return
	}

	channel := event.Params[1]

	b.Log.Debugf("got invite for %s", channel)
	b.channelsMu.RLock()
	_, ok := b.channels[channel]
	b.channelsMu.RUnlock()

	if ok {
		b.i.Cmd.Join(channel)
	}
}

func (b *Birc) handleJoinPartKICK(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) == 0 {
		b.Log.Debugf("handleJoinPartKICK: empty Params? %#v", event)
		return
	}

	channel := strings.ToLower(event.Params[0])

	b.RLock()
	mynick := b.Nick
	b.RUnlock()

	if event.Params[1] == mynick {
		b.Log.Infof("Got kicked from %s by %s", channel, event.Source.Name)
		// TODO: Do this another way, without sleeping
		time.Sleep(time.Duration(b.GetInt("RejoinDelay")) * time.Second)

		b.Remote <- config.Message{Username: "system", Text: "rejoin", Channel: channel, Account: b.Account, Event: config.EventRejoinChannels}
	}
}

func (b *Birc) handleJoinPartQUIT(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) == 0 {
		b.Log.Debugf("handleJoinPartQUIT: empty Params? %#v", event)
		return
	}

	channel := strings.ToLower(event.Params[0])

	b.RLock()
	mynick := b.Nick
	b.RUnlock()

	if event.Source.Name == mynick && strings.Contains(event.Last(), "Ping timeout") {
		b.Log.Infof("%s reconnecting ..", b.Account)

		b.Lock()
		b.authDone = false
		b.botModeDone = false
		b.prefixDone = false
		b.caseMapDone = false
		b.relayMsgDone = false
		b.CasemapFailures = 0
		b.RelayMsgFailures = 0
		b.utf8OnlyDone = false
		b.Unlock()

		b.Remote <- config.Message{Username: "system", Text: "reconnect", Channel: channel, Account: b.Account, Event: config.EventFailure}
	}
}

// Unless we generate another event first, channel join is the first chance to find our current prefix
func (b *Birc) handleJoinPartPrefix(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	b.RLock()

	if b.prefixDone {
		b.RUnlock()

		return
	}

	mynick := b.Nick
	b.RUnlock()

	if event.Source.Name != mynick {
		return
	}

	if len(event.Params) == 0 {
		b.Log.Debugf("handleJoinPartPrefix: empty Params? %#v", event)
		return
	}

	channel := strings.ToLower(event.Params[0])
	user := event.Source.Ident
	host := event.Source.Host

	if host == "" {
		b.Log.Debugf("empty host after %s joined %s?", mynick, channel)
		return
	}

	myprefix := len(mynick) + len(user) + len(host) + 6 // 6 bytes for ':', '!', '@', ' ' and a trailing CRLF

	b.SetInt("MessagePrefix", myprefix)

	b.RLock()
	mymaxlen := b.maxLen        // Server-specified max line length
	mymsglen := b.MessageLength // Our current setting, default of 512
	b.RUnlock()

	// Work around girc using max prefix length instead of actual prefix length
	if (defaultMaxPrefix + mymaxlen - myprefix) > (mymsglen - myprefix) {
		mymsglen = mymaxlen + defaultMaxPrefix // Server supports extended lines
		b.SetInt("MessageLength", mymsglen)

		b.Lock()
		b.MessageLength = mymsglen
		b.Unlock()
	}

	b.Lock()
	b.MessagePrefix = myprefix
	b.prefixDone = true
	b.Unlock()
}

func (b *Birc) handleJoinPart(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) == 0 {
		b.Log.Debugf("handleJoinPart: empty Params? %#v", event)
		return
	}

	channel := strings.ToLower(event.Params[0])

	b.RLock()
	mynick := b.Nick
	mydebug := b.DebugMode
	b.RUnlock()

	if event.Source.Name != mynick {
		if b.GetBool("nosendjoinpart") {
			return
		}

		text := formatJoinLeaveText(event, b.GetBool("verbosejoinpart"))
		msg := config.Message{
			Username: event.Source.Name,
			Text:     text,
			Channel:  channel,
			Account:  b.Account,
			Event:    config.EventJoinLeave,
		}

		switch event.Command {
		case girc.JOIN:
			msg.Event = config.EventJoin
		case girc.PART:
			msg.Event = config.EventLeave
		}

		if mydebug {
			b.Log.Debugf("<= Sending JOIN/LEAVE event from %s to gateway", b.Account)
			b.Log.Debugf("<= Message is %#v", msg)
		}
		b.Remote <- msg

		return
	}

	if mydebug {
		b.Log.Debugf("handle %#v", event)
	}
}

func (b *Birc) handleCapRelay(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if !b.GetBool("UseRelayMsg") {
		return // nothing to do
	}

	if len(event.Params) >= 3 && event.Params[1] == girc.CAP_LS || event.Params[1] == girc.CAP_NEW {
		b.RLock()
		mydebug := b.DebugMode

		if b.relayMsgDone {
			b.RUnlock()

			if event.Params[1] != girc.CAP_NEW {
				return // We're done here, unless the server has rehashed with a new separator
			}
		} else {
			b.RUnlock()
		}
		caps := strings.Split(event.Last(), " ")

		for cap := range caps {
			for strings.Contains(caps[cap], "relaymsg") { // "draft/relaymsg" now, but for compatibility's sake...
				sep := strings.Index(caps[cap], "=")
				if sep < 0 || sep == (len(caps[cap])-1) { // make sure there is actually a separator specified
					b.Log.Errorf("Relaymsg capability advertised but no separator specified: %s", caps[cap])

					return
				} else {
					mysep := strings.TrimSpace(caps[cap][sep+1:])

					if mydebug {
						b.Log.Debugf("RelayMsgSep value for %s set to %s", b.Account, mysep)
					}

					b.SetString("RelayMsgSep", mysep)

					b.Lock()
					b.RelayMsgSep = mysep
					b.relayMsgDone = true
					b.Unlock()

					return
				}
			}
		}
	}
}

func (b *Birc) handleErrorCM(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) < 2 || event.Params[0] != cmdRelayMsg || event.Params[1] != errInvalidNick {
		return
	}

	text := event.Last()

	if !strings.Contains(text, "Invalid nickname") || strings.Contains(text, "Relayed nicknames MUST contain") {
		return // another handler will log the error
	}

	b.RLock()

	if b.CasemapFailures > 1 && b.caseMapDone {
		b.RUnlock()

		// This may be a case of an incorrect RelayMsgSep instead, let the other handlers deal with it
		return
	}

	mymap := b.Casemapping
	mydebug := b.DebugMode
	b.RUnlock()

	switch mymap {
	case CM_PERMISSIVE: // Ergo sends "ascii" for precis, permissive, or ascii options
		b.SetString("Casemapping", CM_ASCII)

		if mydebug {
			b.Log.Debugf("Got RELAYMSG failure with permissive, falling back to ASCII on %s", b.Account)
		}

		b.Lock()
		b.Casemapping = CM_ASCII
		b.caseMapDone = false
	case CM_ASCII:
		b.Log.Info("RELAYMSG failure with ASCII setting on " + b.Account)
		b.Log.Info("Next RELAYMSG nick failure will be assumed to be a missing separator")
		b.Lock()
		b.caseMapDone = true
	default:
		b.SetString("Casemapping", CM_ASCII)

		if mydebug {
			b.Log.Debugf("RELAYMSG failure with %s setting, falling back to ASCII on %s", mymap, b.Account)
		}

		b.Lock()
		b.Casemapping = CM_ASCII
		b.caseMapDone = false
	}

	b.CasemapFailures += 1
	b.RelayMsgFailures += 1
	b.Unlock()

	b.Log.Warnf("Got a RELAYMSG failure: %s", event.Last())

	if mydebug {
		b.RLock()
		mycmfails := b.CasemapFailures
		myrelfails := b.RelayMsgFailures
		b.RUnlock()
		b.Log.Debugf("<= Message is %#v", event)
		b.Log.Debugf("Error count is %d (casemap failures) %d (relaymsg failures)", mycmfails, myrelfails)
	}
}

func (b *Birc) handleErrorSEP(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) < 2 && event.Params[0] != cmdRelayMsg || event.Params[1] != errInvalidNick {
		return
	}

	text := event.Last()

	b.RLock()
	mydebug := b.DebugMode
	cmdone := b.caseMapDone
	cmfail := b.CasemapFailures
	mysep := b.RelayMsgSep
	b.RUnlock()

	switch { // Ergo might return either one of these strings for a missing separator
	case strings.Contains(text, "Relayed nicknames MUST contain"):
		break
	case strings.Contains(text, "Invalid nickname"):
		if !cmdone || cmfail <= 1 {
			return // It might be a wrong casemapping setting
		}

		// We've the wrong separator but nothing to do about it?  Try the CAP handler again
		b.Lock()
		b.relayMsgDone = false
		b.Unlock()

		if mydebug {
			b.Log.Debugf("Sending a CAP LS 302 to check for RelayMsgSep on %s", b.Account)
		}
		client.Send(&girc.Event{Command: "CAP", Params: []string{"LS", "302"}})

		return
	default:
		if mydebug {
			b.Log.Debugf("Unknown INVALID_NICK response from %s: %s", b.Account, text)
		}

		return
	}

	b.Log.Warnf("Separator char was not present in RemoteNickFormat for %s", b.Account)

	sepindex := strings.LastIndex(text, " ")
	if sepindex == (len(text)-1) && mysep == "" {
		b.Log.Errorf("Relaymsg capability advertised for %s but no separator specified: %s", b.Account, text)
		b.Lock()
		b.RelayMsgFailures += 1
		b.Unlock()

		return // maybe we should panic?
	}

	sepchars := text[sepindex+1:]

	if mydebug {
		b.Log.Debugf("Prior autoconfigured separator chars: %s", mysep)
		b.Log.Debugf("Possibly updated separator chars: %s", sepchars)
	}

	if sepchars != "" && mysep != "" && !strings.ContainsAny(sepchars, mysep) {
		// we have an entirely new set of separator chars.
		b.Log.Infof("New relay separator char(s) for %s set by server: %s", b.Account, sepchars)
		mysep = strings.TrimSpace(sepchars)
		b.SetString("RelayMsgSep", mysep)
		b.Lock()
		b.RelayMsgSep = mysep
		b.relayMsgDone = true
		b.Unlock()
	}

	b.Log.Warnf("Got a RELAYMSG failure: %s", event.Last())

	b.Lock()
	b.RelayMsgFailures += 1
	b.Unlock()

	if mydebug {
		b.RLock()
		relfail := b.RelayMsgFailures
		b.RUnlock()

		b.Log.Debugf("<= Message is %#v", event)
		b.Log.Debugf("Error count is %d (casemap failures) %d (relaymsg failures)", cmfail, relfail)
	}
}

func (b *Birc) handleErrorOther(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if len(event.Params) < 2 || event.Params[0] != cmdRelayMsg || event.Params[1] == errInvalidNick {
		return // another handler will log the error
	}

	// TODO: handle more things

	// text := event.Last()

	// switch event.Params[1] {
	// case "BANNED":
	//	fallthrough
	// case "BLANK_MSG":
	//	fallthrough
	// case "PRIVS_NEEDED":
	//	fallthrough
	// case "NOT_ENABLED":
	//	fallthrough
	// default:
	b.Log.Errorf("Got a RELAYMSG failure: %s", event.Last())
	b.Lock()
	b.RelayMsgFailures += 1
	b.Unlock()

	if b.GetBool("Debug") {
		b.Log.Debugf("<= Message is %#v", event)
		b.RLock()
		cmfail := b.CasemapFailures
		relfail := b.RelayMsgFailures
		b.RUnlock()
		b.Log.Debugf("Error count is %d (casemap failures) %d (relaymsg failures)", cmfail, relfail)
	}
}

func (b *Birc) handleISupportBOT(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	b.RLock()
	if b.botModeDone {
		b.RUnlock()
		return
	}

	b.RUnlock()

	result, ok := client.GetServerOption("BOT")
	if ok {
		b.Log.Debugf("Server supports BOT: %s", result)
		client.Send(&girc.Event{Command: girc.MODE, Params: []string{b.Nick, "+" + result}})
		b.Lock()
		b.botModeDone = true
		b.Unlock()
	}
}

func (b *Birc) handleISupportCM(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	b.RLock()
	cmdone := b.caseMapDone
	b.RUnlock()

	if !b.GetBool("UseRelayMsg") || cmdone {
		return // For now, we only do anything with Casemapping if using RelayMsg
	}

	casecheck, ok := client.GetServerOption("CASEMAPPING")
	if !ok {
		return // nothing to do here
	}

	mymap := ""

	switch casecheck {
	case CM_ASCII: // For Ergo, this is set for any of "ascii", "permissive", or "precis".
		utf8mapcheck, ok := client.GetServerOption("UTF8MAPPING")
		if !ok { // UTF8MAPPING can be on a different line, so we haven't necessarily ruled out precis yet.
			mymap = CM_UNKNOWN
		} else { // We can set it to ascii later if there's an error.
			switch utf8mapcheck {
			case CM_PRECIS, "rfc7613", "rfc8265":
				mymap = CM_PRECIS
			default:
				b.Log.Errorf("Unknown UTF8MAPPING token on %s: %s", b.Account, utf8mapcheck)
			}
		}
	case CM_RFC1459:
		mymap = CM_RFC1459
	case CM_RFC1459STRICT:
		mymap = CM_RFC1459STRICT
	default:
		b.Log.Errorf("Unknown CASEMAPPING token on %s: %s", b.Account, casecheck)
	}

	switch mymap {
	case CM_UNKNOWN:
		b.Log.Debugf("Deferring initial Casemapping value until end of MOTD / ERRNOMOTD on %s", b.Account)
	case "":
		b.Log.Debugf("Falling back to ASCII on %s", b.Account)

		mymap = CM_ASCII

		fallthrough
	default:
		b.Log.Debugf("Setting initial Casemapping value on %s to: %s", b.Account, mymap)
		b.SetString("Casemapping", mymap)
		b.Lock()
		b.Casemapping = mymap
		b.caseMapDone = true
		b.Unlock()
	}
}

func (b *Birc) handleNewConnection(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	b.Log.Debug("Registering callbacks")
	i := b.i

	b.Lock()
	b.Nick = event.Params[0]
	b.Unlock()

	b.Log.Debug("Clearing handlers before adding in case of BNC reconnect")
	i.Handlers.Clear("PRIVMSG")
	i.Handlers.Clear("CTCP_ACTION")
	i.Handlers.Clear(girc.RPL_TOPICWHOTIME)
	i.Handlers.Clear(girc.NOTICE)
	i.Handlers.Clear(girc.JOIN)
	i.Handlers.Clear(girc.PART)
	i.Handlers.Clear("QUIT")
	i.Handlers.Clear(girc.KICK)
	i.Handlers.Clear("INVITE")
	i.Handlers.Clear(girc.RPL_ISUPPORT)
	i.Handlers.Clear(girc.CAP)
	i.Handlers.Clear("FAIL")

	// Foregrounded handlers for the same event will still be executed concurrently,
	// but they will all be placed in the same sync.WaitGroup,
	// and Connect() will ensure they return when the irc server connection closes.
	// Still-running backgrounded handlers will be simply abandoned,
	// therefore try to make sure they won't have any active mutex locks.

	i.Handlers.AddBg("PRIVMSG", b.handlePrivMsg)
	i.Handlers.AddBg(girc.RPL_TOPICWHOTIME, b.handleTopicWhoTime)
	i.Handlers.AddBg(girc.NOTICE, b.handleNotice)
	i.Handlers.AddBg(girc.JOIN, b.handleJoinPart)
	i.Handlers.Add(girc.JOIN, b.handleJoinPartPrefix) // to handle the initial part of the MessagePrefix calculations
	i.Handlers.AddBg(girc.PART, b.handleJoinPart)
	i.Handlers.Add("QUIT", b.handleJoinPartQUIT)      // Foreground this to make sure b.authDone, etc. get reset on ping timeout
	i.Handlers.AddBg(girc.KICK, b.handleJoinPartKICK) // Background this because it sleeps, but need to figure out an alternative.
	i.Handlers.AddBg(girc.KICK, b.handleJoinPart)     // Relay kicks of other channel members as usual
	i.Handlers.Add("INVITE", b.handleInvite)          // handleInvite obtains a read lock, so make sure it comes home

	i.Handlers.Add(girc.RPL_ISUPPORT, b.handleISupportBOT) // enable bot mode
	i.Handlers.Add(girc.RPL_ISUPPORT, b.handleISupportCM)  // determine casemapping value

	// i.Handlers.Add(girc.CAP, b.handleCapMsgid) // TODO: add this later
	// i.Handlers.Add(girc.CAP, b.handleCapMultiline) // TODO: add this later
	i.Handlers.Add(girc.CAP, b.handleCapRelay) // autodetect the separator character for relaymsg

	// see https://ircv3.net/specs/extensions/standard-replies
	i.Handlers.Add("FAIL", b.handleErrorCM)    // for Casemapping or SanitizeNick errors
	i.Handlers.Add("FAIL", b.handleErrorSEP)   // for RelayMsgSep errors
	i.Handlers.Add("FAIL", b.handleErrorOther) // for all other RelayMsg-related errors
}

func (b *Birc) handleNickServ() {
	defer b.ircHandlePanic()

	nickservnick := b.GetString("NickServNick")
	nickservpass := b.GetString("NickServPassword")

	if !b.GetBool("UseSASL") && nickservnick != "" && nickservpass != "" {
		b.Log.Debugf("Sending identify to nickserv %s", nickservnick)
		b.i.Cmd.Message(nickservnick, "IDENTIFY "+nickservpass)
	}

	if strings.EqualFold(nickservnick, "Q@CServe.quakenet.org") {
		nickservuser := b.GetString("NickServUsername")
		b.Log.Debugf("Authenticating %s against %s", nickservuser, nickservnick)
		b.i.Cmd.Message(nickservnick, "AUTH "+nickservuser+" "+nickservpass)
	}
	// give nickserv some slack
	// TODO: Do this a different way, without sleeping
	time.Sleep(time.Second * 5)
	b.Lock()
	b.authDone = true
	b.Unlock()
}

func (b *Birc) handleNotice(client *girc.Client, event girc.Event) {
	if strings.Contains(event.String(), "This nickname is registered") && event.Source.Name == b.GetString("NickServNick") {
		b.handleNickServ()
	} else {
		b.handlePrivMsg(client, event)
	}
}

func (b *Birc) handleOther(client *girc.Client, event girc.Event) {
	if b.GetInt("DebugLevel") == 1 {
		if event.Command != "CLIENT_STATE_UPDATED" &&
			event.Command != "CLIENT_GENERAL_UPDATED" {
			b.Log.Debugf("%#v", event.String())
		}
		return
	}
	switch event.Command {
	case "372", "375", "376", "250", "251", "252", "253", "254", "255", "265", "266", "002", "003", "004", "005":
		return
	}
	b.Log.Debugf("%#v", event.String())
}

func (b *Birc) handleOtherAuth(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	b.handleNickServ()
	b.handleRunCommands()
	mymaxlen := b.i.MaxEventLength() // Set this one time per (re)connect
	b.Lock()
	b.maxLen = mymaxlen
	b.Unlock()

	b.RLock()
	reldone := b.relayMsgDone
	cmdone := b.caseMapDone
	utf8done := b.utf8OnlyDone
	firstconn := b.FirstConnection
	b.RUnlock()

	// TODO: Figure out why this wasn't caught the first time.
	//
	// Did girc only send a CAP LS for this connection instead of CAP LS 302?
	// We have been doing a lot of workarounds since girc DOES record the cap values for each enabled cap,
	// but unfortunately it doesn't provide a way to export them.
	// The relaymsg capability is advertised with CAP LS, but the separator is only included with CAP LS 302.
	//
	// Send a CAP LS 302...
	if b.GetBool("UseRelayMsg") && !reldone {
		b.Log.Debugf("Requesting a CAP LS 302 to determine RelayMsgSep on %s", b.Account)

		client.Send(&girc.Event{Command: "CAP", Params: []string{"LS", "302"}})
	}

	if b.GetBool("UseRelayMsg") && !cmdone {
		var mymap string

		utf8mapcheck, ok := client.GetServerOption("UTF8MAPPING")
		if !ok { // By now we have ruled out precis, so let's try permissive first
			mymap = CM_PERMISSIVE
		} else { // We can set it to ascii later if there's an error.
			switch utf8mapcheck {
			case CM_PRECIS, "rfc7613", "rfc8265":
				mymap = CM_PRECIS
			default:
				b.Log.Errorf("Unknown UTF8MAPPING token on %s: %s", b.Account, utf8mapcheck)
				b.Log.Debug("Falling back to ASCII")

				mymap = CM_ASCII
			}
		}

		b.Log.Debugf("Setting initial Casemapping value on %s to: %s", b.Account, mymap)

		b.SetString("Casemapping", mymap)
		b.Lock()
		b.Casemapping = mymap
		b.caseMapDone = true
		b.Unlock()
	}

	// handle a UTF8ONLY ISUPPORT token, which restricts us to only sending unicode to the server.
	if !utf8done {
		_, ok := client.GetServerOption("UTF8ONLY")
		if ok { // We are restricted to sending UTF8 only
			b.SetString("Charset", utf8charset)
			b.SetBool("Utf8Only", true)
		} else { // We are free to send other charsets
			b.SetBool("Utf8Only", false)
		}

		b.Lock()
		b.utf8OnlyDone = true
		b.Unlock()
	}

	// we are now fully connected
	// only send on first connection
	if firstconn {
		b.connected <- nil
	}
}

func (b *Birc) handlePrivMsg(client *girc.Client, event girc.Event) {
	defer b.ircHandlePanic()

	if b.skipPrivMsg(event) {
		return
	}

	rmsg := config.Message{
		Username: event.Source.Name,
		Channel:  strings.ToLower(event.Params[0]),
		Account:  b.Account,
		UserID:   event.Source.Ident + "@" + event.Source.Host,
	}

	b.RLock()
	mydebug := b.DebugMode
	b.RUnlock()

	if mydebug {
		b.Log.Debugf("== Receiving PRIVMSG: %s %s %#v", event.Source.Name, event.Last(), event)
	}

	// set action event
	if ok, ctcp := event.IsCTCP(); ok {
		if ctcp.Command != girc.CTCP_ACTION {
			if mydebug {
				b.Log.Debugf("dropping user ctcp, command: %s", ctcp.Command)
			}
			return
		}
		rmsg.Event = config.EventUserAction
	}

	// set NOTICE event
	if event.Command == "NOTICE" {
		rmsg.Event = config.EventNoticeIRC
	}

	// trailing param is message content.
	// we'll treat it as a byte slice first, convert to utf-8 if needed, then do our own version of StripAction
	rmsg.Text = event.Params[len(event.Params)-1]

	mycharset := b.GetString("Charset")

	switch mycharset {
	case "utf8", utf8charset:
		break
	case "gbk", "gb18030", "gb2312", "big5", "euc-kr", "euc-jp", "shift-jis", "iso-2022-jp":
		rmsg.Text = toUTF8(mycharset, rmsg.Text)
	case "autodetect": // start detecting the charset.  fixes #120 (mostly)
		if utf8.ValidString(rmsg.Text) { // check for valid utf-8 before any other checks
			break
		}
		// detect what were sending so that we convert it to utf-8
		detector := chardet.NewTextDetector()
		result, err := detector.DetectBest([]byte(rmsg.Text))
		if err != nil {
			b.Log.Infof("detection failed for rmsg.Text: %#v", rmsg.Text)
			return
		}

		if mydebug {
			b.Log.Debugf("detected %s confidence %#v", result.Charset, result.Confidence)
		}

		mycharset = result.Charset
		// if we're not sure, just pick ISO-8859-1
		if result.Confidence < 80 {
			mycharset = "ISO-8859-1"
		}
		fallthrough
	default:
		r, err := charset.NewReader(mycharset, strings.NewReader(rmsg.Text))
		if err != nil {
			b.Log.Errorf("charset to utf-8 conversion failed: %s", err)
			return
		}

		output, _ := io.ReadAll(r)

		rmsg.Text = string(output)
	}

	// let's make sure only to modify the message text AFTER the possible utf-8 conversion.
	// strip action, we made an event if it was an action
	if event.IsAction() {
		rmsg.Text = rmsg.Text[8 : len(rmsg.Text)-1]
	}

	if mydebug {
		b.Log.Debugf("<= Sending message from %s on %s to gateway", event.Params[0], b.Account)
	}

	b.Remote <- rmsg
}

func (b *Birc) handleRunCommands() {
	for _, cmd := range b.GetStringSlice("RunCommands") {
		cmd = strings.ReplaceAll(cmd, "{BOTNICK}", b.Nick)
		if err := b.i.Cmd.SendRaw(cmd); err != nil {
			b.Log.Errorf("RunCommands %s failed: %s", cmd, err)
		}
		time.Sleep(time.Second)
	}
}

func (b *Birc) handleTopicWhoTime(client *girc.Client, event girc.Event) {
	parts := strings.Split(event.Params[2], "!")
	t, err := strconv.ParseInt(event.Params[3], 10, 64)
	if err != nil {
		b.Log.Errorf("Invalid time stamp: %s", event.Params[3])
	}
	user := parts[0]
	if len(parts) > 1 {
		user += " [" + parts[1] + "]"
	}
	b.Log.Debugf("%s: Topic set by %s [%s]", event.Command, user, time.Unix(t, 0))
}

// formatJoinLeaveText renders a join/part/quit/kick event into a human-readable
// system message. Special-cases KICK so the kicked nick (and reason, if any)
// appear instead of just "<kicker> kicks".
func formatJoinLeaveText(event girc.Event, verbose bool) string {
	source := event.Source.Name
	if verbose && event.Source.Ident != "" {
		source += " (" + event.Source.Ident + "@" + event.Source.Host + ")"
	}
	if event.Command == "KICK" && len(event.Params) >= 2 {
		text := source + " kicked " + event.Params[1]
		if reason := event.Last(); reason != "" && reason != event.Params[1] {
			text += " (" + reason + ")"
		}
		return text
	}
	return source + " " + strings.ToLower(event.Command) + "s"
}
