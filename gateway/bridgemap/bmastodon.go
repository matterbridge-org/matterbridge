//go:build !nomastodon
// +build !nomastodon

package bridgemap

import (
	bmastodon "github.com/matterbridge-org/matterbridge/bridge/mastodon"
)

func init() {
	FullMap["mastodon"] = bmastodon.New
}
