package assets

import "embed"

// FS embeds the canonical frontend files shared by both web and GUI.
//
//go:embed "frontend/*"
var FS embed.FS
