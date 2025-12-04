//go:build !nomattermost
// +build !nomattermost

package bridgemap

import (
	bmattermost "github.com/matterbridge-org/matterbridge/bridge/mattermost"
)

func init() {
	FullMap["mattermost"] = bmattermost.New
}
