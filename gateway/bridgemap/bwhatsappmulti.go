//go:build !nowhatsappmulti

package bridgemap

import (
	bwhatsapp "github.com/matterbridge-org/matterbridge/bridge/whatsappmulti"
)

func init() {
	FullMap["whatsapp"] = bwhatsapp.New
}
