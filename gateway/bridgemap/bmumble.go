//go:build !nomumble

package bridgemap

import (
	bmumble "github.com/matterbridge-org/matterbridge/bridge/mumble"
)

func init() {
	FullMap["mumble"] = bmumble.New
}
