package gateway

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/matterbridge-org/matterbridge/bridge"
	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBridge records the messages sent to it and returns predictable IDs.
type fakeBridge struct {
	*bridge.Config

	mu   sync.Mutex
	sent []config.Message
	n    int
}

func (f *fakeBridge) Send(msg config.Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sent = append(f.sent, msg)
	if msg.IsReaction() || msg.ID != "" {
		return "", nil
	}

	f.n++

	return fmt.Sprintf("%s-copy-%d", f.Protocol, f.n), nil
}

func (f *fakeBridge) Connect() error                         { return nil }
func (f *fakeBridge) Disconnect() error                      { return nil }
func (f *fakeBridge) JoinChannel(_ config.ChannelInfo) error { return nil }
func (f *fakeBridge) SanitizeNick(_ *config.Message) error   { return nil }

func (f *fakeBridge) last() config.Message {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.sent) == 0 {
		return config.Message{}
	}

	return f.sent[len(f.sent)-1]
}

func (f *fakeBridge) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.sent)
}

const (
	slackTS = "1731700000.000100"
	bob     = "bob"
	alice   = "alice"
	mmProto = "mattermost"
	// channels and protocols of the test configs
	slackProto = "slack"
	slackChan  = "general"
	mmChan     = "town-square"
)

var reactionTestConfig = []byte(`
[mattermost.test]
PreserveThreading=true
PreserveReactions=true
[slack.test]
PreserveThreading=true
PreserveReactions=true
[irc.test]
PreserveReactions=true

[[gateway]]
    name = "bridge1"
    enable=true

    [[gateway.inout]]
    account = "mattermost.test"
    channel = "town-square"

    [[gateway.inout]]
    account="slack.test"
    channel="general"

    [[gateway.inout]]
    account="irc.test"
    channel="#general"
`)

func TestReactions(t *testing.T) {
	fakes := map[string]*fakeBridge{}
	factory := func(cfg *bridge.Config) bridge.Bridger {
		f := &fakeBridge{Config: cfg}
		fakes[cfg.Account] = f

		return f
	}
	r := maketestRouterWithBridges(reactionTestConfig, map[string]bridge.Factory{
		mmProto: factory, slackProto: factory, ircProtocol: factory,
	})
	require.NoError(t, r.Start())

	mm, slack, irc := fakes[mmAccount], fakes[slackAccount], fakes["irc.test"]
	require.NotNil(t, mm)
	require.NotNil(t, slack)
	require.NotNil(t, irc)

	send := func(msg config.Message) {
		r.Message <- msg

		time.Sleep(50 * time.Millisecond)
	}

	// A message posted on Slack is relayed to Mattermost as mattermost-copy-1.
	send(config.Message{Account: slackAccount, Channel: slackChan, Username: alice, Text: "hello", ID: slackTS})
	require.Equal(t, "hello", mm.last().Text)

	ircBefore := irc.count()

	// A Mattermost user reacts on the Mattermost copy: Slack gets it on the original.
	send(config.Message{Account: mmAccount, Channel: mmChan, Username: bob, UserID: "u1",
		Event: config.EventReactionAdd, ID: "mattermost-copy-1", Text: "+1"})

	got := slack.last()
	assert.Equal(t, config.EventReactionAdd, got.Event)
	assert.Equal(t, slackTS, got.ID)
	assert.Equal(t, "+1", got.Text)

	// A Slack user reacts on the original: Mattermost gets it on the copy.
	send(config.Message{Account: slackAccount, Channel: slackChan, Username: "carol", UserID: "U2",
		Event: config.EventReactionRemove, ID: slackTS, Text: "heart"})

	got = mm.last()
	assert.Equal(t, config.EventReactionRemove, got.Event)
	assert.Equal(t, "mattermost-copy-1", got.ID)

	// Bridges without reaction support never receive them (as text or otherwise).
	assert.Equal(t, ircBefore, irc.count())

	// Reactions to unknown messages are dropped.
	slackBefore := slack.count()

	send(config.Message{Account: mmAccount, Channel: mmChan, Username: bob,
		Event: config.EventReactionAdd, ID: "unknown", Text: "+1"})
	assert.Equal(t, slackBefore, slack.count())

	// A reaction must not be recorded as a message: a thread reply on the
	// Mattermost copy must still be mapped to the Slack original.
	send(config.Message{Account: mmAccount, Channel: mmChan, Username: bob, Text: "reply",
		ID: "mmreply", ParentID: "mattermost-copy-1"})

	got = slack.last()
	assert.Equal(t, "reply", got.Text)
	assert.Equal(t, slackTS, got.ParentID)
}

func TestReactionsOptIn(t *testing.T) {
	fakes := map[string]*fakeBridge{}
	factory := func(cfg *bridge.Config) bridge.Bridger {
		f := &fakeBridge{Config: cfg}
		fakes[cfg.Account] = f

		return f
	}
	r := maketestRouterWithBridges([]byte(`
[mattermost.test]
server=""
[slack.test]
server=""

[[gateway]]
    name = "bridge1"
    enable=true

    [[gateway.inout]]
    account = "mattermost.test"
    channel = "town-square"

    [[gateway.inout]]
    account="slack.test"
    channel="general"
`), map[string]bridge.Factory{mmProto: factory, slackProto: factory})
	require.NoError(t, r.Start())

	r.Message <- config.Message{Account: slackAccount, Channel: slackChan, Username: alice, Text: "hello", ID: "1"}

	time.Sleep(50 * time.Millisecond)

	before := fakes[mmAccount].count()

	r.Message <- config.Message{Account: slackAccount, Channel: slackChan, Username: alice,
		Event: config.EventReactionAdd, ID: "1", Text: "+1"}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, before, fakes[mmAccount].count(), "reactions are only relayed with PreserveReactions")
}
