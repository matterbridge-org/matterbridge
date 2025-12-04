//go:build !noxmpp
// +build !noxmpp

package bridgemap

import (
	bxmpp "github.com/matterbridge-org/matterbridge/bridge/xmpp"
)

func init() {
	FullMap["xmpp"] = bxmpp.New
}
