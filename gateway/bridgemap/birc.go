//go:build !noirc

package bridgemap

import (
	birc "github.com/matterbridge-org/matterbridge/bridge/irc"
)

func init() {
	FullMap["irc"] = birc.New
}
