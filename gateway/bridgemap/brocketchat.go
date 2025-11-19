//go:build !norocketchat
// +build !norocketchat

package bridgemap

import (
	brocketchat "github.com/matterbridge-org/matterbridge/bridge/rocketchat"
)

func init() {
	FullMap["rocketchat"] = brocketchat.New
}
