package static

import "embed"

// FS contains the static assets served by Songdock.
//
//go:embed songdock_logo_dark.v2.png song_artwork_placeholder.png lucide.svg platformadmin.v1.css admin-auth.v1.css admin-base.v1.css admin-home.v1.css admin-song-form.v1.css favicon/*
var FS embed.FS
