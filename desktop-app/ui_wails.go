//go:build !android && !stmbridge

package main

import (
	"context"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// hasNativeWindow reports whether the app runs inside a Wails window it can
// measure and move. The bridge build (ui_bridge.go) has none.
const hasNativeWindow = true

func uiEmit(ctx context.Context, name string, data ...any) {
	wailsrt.EventsEmit(ctx, name, data...)
}

func uiQuit(ctx context.Context) {
	wailsrt.Quit(ctx)
}

func uiSaveFileDialog(ctx context.Context, opts wailsrt.SaveDialogOptions) (string, error) {
	return wailsrt.SaveFileDialog(ctx, opts)
}

func uiOpenFileDialog(ctx context.Context, opts wailsrt.OpenDialogOptions) (string, error) {
	return wailsrt.OpenFileDialog(ctx, opts)
}
