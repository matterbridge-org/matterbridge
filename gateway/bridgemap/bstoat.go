//go:build !nostoat

package bridgemap

import (
	bstoat "github.com/matterbridge-org/matterbridge/bridge/stoat"
)

func init() { //nolint:gochecknoinits
	FullMap["stoat"] = bstoat.New
}
