// Package web embeds the PWA served by tauko.
package web

import "embed"

//go:embed *.html *.js *.css *.json *.svg
var Files embed.FS
