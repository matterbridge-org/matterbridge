//go:build !nozulip
// +build !nozulip

package bridgemap

import (
	bzulip "github.com/matterbridge-org/matterbridge/bridge/zulip"
)

func init() {
	FullMap["zulip"] = bzulip.New
}
