// Package web embeds the templates and static assets of the web services.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static templates
var files embed.FS

// Static holds the assets served under /static/.
var Static, _ = fs.Sub(files, "static")

// Templates holds the HTML templates (one directory per service).
var Templates, _ = fs.Sub(files, "templates")
