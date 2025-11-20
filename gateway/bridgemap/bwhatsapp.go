//go:build !nowhatsapp && !whatsappmulti

package bridgemap

import (
	bwhatsapp "github.com/matterbridge-org/matterbridge/bridge/whatsapp"
)

func init() {
	FullMap["whatsapp"] = bwhatsapp.New
}
