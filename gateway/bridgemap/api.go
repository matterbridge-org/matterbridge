//go:build !noapi
// +build !noapi

package bridgemap

import (
	"github.com/matterbridge-org/matterbridge/bridge/api"
)

func init() {
	FullMap["api"] = api.New
}
