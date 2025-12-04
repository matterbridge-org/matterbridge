//go:build !novk
// +build !novk

package bridgemap

import (
	bvk "github.com/matterbridge-org/matterbridge/bridge/vk"
)

func init() {
	FullMap["vk"] = bvk.New
}
