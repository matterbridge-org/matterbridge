//go:build !nosshchat

package bridgemap

import (
	bsshchat "github.com/matterbridge-org/matterbridge/bridge/sshchat"
)

func init() {
	FullMap["sshchat"] = bsshchat.New
}
