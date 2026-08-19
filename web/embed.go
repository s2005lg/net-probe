package web

import "embed"

// Dist embeds the built frontend assets (web/dist). Build the frontend first
// with `cd web && npm run build`; the files are git-ignored.
//
//go:embed all:dist
var Dist embed.FS
