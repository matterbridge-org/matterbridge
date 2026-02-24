// +build !nodiscord

package bridgemap

import (
	bfluxer "github.com/matterbridge-org/matterbridge/bridge/fluxer"
)

func init() {
	FullMap["fluxer"] = bfluxer.New
}
