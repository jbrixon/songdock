package static

import "embed"

// FS contains the static assets served by Songdock.
//
//go:embed songdock_logo_dark.v2.png song_artwork_placeholder.png lucide.svg favicon/*
var FS embed.FS
