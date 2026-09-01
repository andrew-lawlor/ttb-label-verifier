// Package webassets embeds the HTML templates and static files (htmx,
// stylesheet) into the compiled binary, so deployment is just the single
// binary — no separate asset directory to ship alongside it.
package webassets

import "embed"

//go:embed web/templates/*.html
var TemplatesFS embed.FS

//go:embed web/static/*
var StaticFS embed.FS
