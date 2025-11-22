//go:build !nomastodon

package bridgemap

import (
	bmastodon "github.com/42wim/matterbridge/bridge/mastodon"
)

//nolint:gochecknoinits
func init() {
	FullMap["mastodon"] = bmastodon.New
}
