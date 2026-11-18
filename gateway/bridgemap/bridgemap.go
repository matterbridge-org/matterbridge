package bridgemap

import (
	"github.com/matterbridge-org/matterbridge/bridge"
)

var (
	FullMap             = map[string]bridge.Factory{}
	UserTypingSupport   = map[string]struct{}{}
	SanitizeNickSupport = map[string]struct{}{}
	// ReactionSupport lists the protocols supporting config.EventReactionAdd
	// and config.EventReactionRemove.
	ReactionSupport = map[string]struct{}{}
)
