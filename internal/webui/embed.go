package webui

import (
	_ "embed"
	"time"
)

const shutdownTimeout = 5 * time.Second

// iconPNG is the STM app icon, served at /icon.png for the favicon, the iOS
// apple-touch-icon and the PWA manifest, so a phone that saves the page to its
// home screen gets a proper STM icon.
//
//go:embed assets/icon.png
var iconPNG []byte

// iconLargePNG is the 512x512 STM icon, served at /icon-large.png and declared
// in the PWA manifest so Android has an icon >= 512px, its bar for offering an
// installable home-screen app rather than only a bookmark. Palette PNG (~50 KB).
//
//go:embed assets/icon-large.png
var iconLargePNG []byte

// indexHTML is the self-contained controller page the agent serves on "/". It is
// the phone remote: a mobile-first page (no desktop app needed) that drives the
// box over the same REST API the desktop app uses. It is PWA-capable (save to
// home screen), shows volume + input + presets + transport, links to the other
// STM speakers on the network, and is branded as SoundTouch Manager.
//
//go:embed assets/index.html
var indexHTML string
