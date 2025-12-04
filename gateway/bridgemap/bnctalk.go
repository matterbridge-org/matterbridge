//go:build !nonctalk
// +build !nonctalk

package bridgemap

import (
	btalk "github.com/matterbridge-org/matterbridge/bridge/nctalk"
)

func init() {
	FullMap["nctalk"] = btalk.New
}
