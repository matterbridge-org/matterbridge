//go:build !nodiscord
// +build !nodiscord

package bridgemap

import (
	bdiscord "github.com/matterbridge-org/matterbridge/bridge/discord"
)

func init() {
	FullMap["discord"] = bdiscord.New
	UserTypingSupport["discord"] = struct{}{}
}
