package main

import "embed"

// The embed below requires frontend/dist to exist at build time.
// An empty frontend/dist/.gitkeep is tracked so a fresh clone can
// run `go build` even without `wails build` first; the real Vite
// output replaces it during the desktop-app build pipeline.
//
//go:embed all:frontend/dist
var assets embed.FS
