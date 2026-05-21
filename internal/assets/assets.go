package assets

import "embed"

// FS embeds the canonical frontend files shared by both web and GUI.
// Uses recursive embed (Go 1.24+) to include subdirectories like wailsjs/.
//
//go:embed "frontend"
var FS embed.FS
