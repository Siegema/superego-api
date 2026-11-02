// Package assets bundles the website's static files into the executable.
package assets

import "embed"

// Files contains only explicitly embedded public assets.
//
//go:embed style.css
var Files embed.FS
