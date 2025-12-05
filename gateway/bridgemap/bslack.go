//go:build !noslack
// +build !noslack

package bridgemap

import (
	bslack "github.com/matterbridge-org/matterbridge/bridge/slack"
)

func init() {
	FullMap["slack-legacy"] = bslack.NewLegacy
	FullMap["slack"] = bslack.New
	UserTypingSupport["slack"] = struct{}{}
}
