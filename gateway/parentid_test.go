package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var parentIDTestConfig = []byte(`
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
`)

const (
	slackAccount = "slack.test"
	mmAccount    = "mattermost.test"
)

func TestGetDestParentID(t *testing.T) {
	r := maketestRouter(parentIDTestConfig)
	gw := r.Gateways["bridge1"]
	mm := gw.Bridges[mmAccount]
	slack := gw.Bridges[slackAccount]
	mmChannel := gw.Channels["town-squaremattermost.test"]

	slackChannel := gw.Channels["generalslack.test"]
	if !assert.NotNil(t, mm) || !assert.NotNil(t, slack) ||
		!assert.NotNil(t, mmChannel) || !assert.NotNil(t, slackChannel) {
		return
	}

	// Thread root posted on Mattermost and relayed to Slack.
	gw.Messages.Add("mattermost mmroot1", []*BrMsgID{{slack, "slack 1731700000.000100", slackChannel.ID}})
	// Thread root posted on Mattermost whose Slack copy has no known ID
	// (e.g. failed send, or a file upload whose share was not visible yet).
	gw.Messages.Add("mattermost 45174abcdefghijklmnopqrstu", []*BrMsgID{})
	// Thread root posted on Slack and relayed to Mattermost.
	gw.Messages.Add("slack 1731700000.000200", []*BrMsgID{{mm, "mattermost mmcopy2", mmChannel.ID}})

	tests := []struct {
		name      string
		canonical string
		dest      string
		want      string
	}{
		{"no parent", "", slackAccount, ""},
		{"mattermost root, slack copy known", "mattermost mmroot1", slackAccount, "1731700000.000100"},
		{"slack root, mattermost copy known", "slack 1731700000.000200", mmAccount, "mmcopy2"},
		{"slack root, reply relayed back to slack", "slack 1731700000.000200", slackAccount, "1731700000.000200"},
		{"mattermost root, reply relayed back to mattermost", "mattermost mmroot1", mmAccount, "mmroot1"},
		// Regression: this used to return "mattermost 45174abc..." and Slack
		// rejected the reply with invalid_thread_ts, losing the message.
		{"mattermost root, slack copy unknown", "mattermost 45174abcdefghijklmnopqrstu", slackAccount, ""},
		{"unknown foreign parent", "discord 123", mmAccount, ""},
	}

	channels := map[string]string{slackAccount: slackChannel.ID, mmAccount: mmChannel.ID}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := gw.Bridges[tt.dest]
			channel := gw.Channels[channels[tt.dest]]
			assert.Equal(t, tt.want, gw.getDestParentID(tt.canonical, dest, channel))
		})
	}
}
