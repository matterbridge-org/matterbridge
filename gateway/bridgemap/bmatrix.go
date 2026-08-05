//go:build !nomatrix

package bridgemap

import (
	bmatrix "github.com/matterbridge-org/matterbridge/bridge/matrix"
)

func init() {
	FullMap["matrix"] = bmatrix.New
}
