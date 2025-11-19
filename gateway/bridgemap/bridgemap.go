package bridgemap

import (
	"github.com/matterbridge-org/matterbridge/bridge"
)

var (
	FullMap           = map[string]bridge.Factory{}
	UserTypingSupport = map[string]struct{}{}
)
