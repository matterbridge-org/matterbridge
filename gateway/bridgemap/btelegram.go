//go:build !notelegram
// +build !notelegram

package bridgemap

import (
	btelegram "github.com/matterbridge-org/matterbridge/bridge/telegram"
)

func init() {
	FullMap["telegram"] = btelegram.New
}
