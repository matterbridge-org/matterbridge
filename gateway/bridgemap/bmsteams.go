//go:build !nomsteams

package bridgemap

import (
	bmsteams "github.com/matterbridge-org/matterbridge/bridge/msteams"
)

func init() {
	FullMap["msteams"] = bmsteams.New
}
