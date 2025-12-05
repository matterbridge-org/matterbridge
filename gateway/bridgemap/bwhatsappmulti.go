//go:build whatsappmulti
// +build whatsappmulti

package bridgemap

import (
	bwhatsapp "github.com/matterbridge-org/matterbridge/bridge/whatsappmulti"
)

func init() {
	FullMap["whatsapp"] = bwhatsapp.New
}
