// Package content embeds the courses shipped with the binary.
package content

import "embed"

// Courses holds courses/<course>/course.json and courses/<course>/decks/*.json.
//
//go:embed courses
var Courses embed.FS
