// Package templates bundles the server-rendered HTML into the executable.
package templates

import "embed"

// Files contains the website's HTML templates, not user-provided content.
//
//go:embed *.html
var Files embed.FS
