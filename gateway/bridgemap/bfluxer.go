//go:build !nofluxer

package bridgemap

import (
	bfluxer "github.com/matterbridge-org/matterbridge/bridge/fluxer"
)

func init() { //nolint:gochecknoinits
	FullMap["fluxer"] = bfluxer.New
}
