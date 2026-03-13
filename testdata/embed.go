// Package testdata provides embedded demo images for the @matterbridge test sequence.
// Place demo.png and demo.gif in this directory before building.
package testdata

import _ "embed"

//go:embed demo.png
var DemoPNG []byte

//go:embed demo.gif
var DemoGIF []byte
