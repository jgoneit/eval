// Package schemas exposes the public Eval JSON Schemas as embedded resources.
package schemas

import "embed"

// Files contains the observation, draft, and extension schemas shipped with
// this exact evalctl build.
//
//go:embed *.json extensions/*.json
var Files embed.FS
