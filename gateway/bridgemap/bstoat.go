//go:build !nostoat

package bridgemap

import (
	bstoat "github.com/matterbridge-org/matterbridge/bridge/stoat"
)

func init() {
	FullMap["stoat"] = bstoat.New
}
