//go:build !nomastodon
// +build !nomastodon

package bridgemap

import (
	bmastodon "github.com/42wim/matterbridge/bridge/mastodon"
)

func init() {
	FullMap["mastodon"] = bmastodon.New
}
