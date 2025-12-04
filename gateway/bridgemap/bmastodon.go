//go:build !nomastodon

package bridgemap

import (
	bmastodon "github.com/matterbridge-org/matterbridge/bridge/mastodon"
)

//nolint:gochecknoinits
func init() {
	FullMap["mastodon"] = bmastodon.New
}
