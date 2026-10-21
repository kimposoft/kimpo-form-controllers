// Package formcontrollers exposes immutable build metadata shared by the plugin entrypoint.
package formcontrollers

import (
	"bytes"
	_ "embed"
)

// rootManifestJSON is the only manifest embedded into the Form Controllers binary.
//
//go:embed manifest.json
var rootManifestJSON []byte

// ManifestJSON returns a copy of the tracked root manifest bytes.
func ManifestJSON() []byte {
	return bytes.Clone(rootManifestJSON)
}
