//go:build android || stmbridge

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// The bridge build runs without a Wails window. Every wailsrt call that needs
// one would log.Fatalf on the plain context the bridge hands to startup, so
// none of them may be reached from here.
const hasNativeWindow = false

var errNoFileDialog = errors.New("opening a file is not available in this app")

func uiEmit(_ context.Context, name string, data ...any) {
	bridgeEvents.publish(name, data)
}

func uiQuit(_ context.Context) {
	os.Exit(0)
}

// uiSaveFileDialog has no dialog to show, so it saves under the export
// directory the host app named (STM_EXPORT_DIR), falling back to the home
// directory, and returns that path as if the user had picked it.
func uiSaveFileDialog(_ context.Context, opts wailsrt.SaveDialogOptions) (string, error) {
	dir := os.Getenv("STM_EXPORT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(opts.DefaultFilename)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "stm-export"
	}
	return filepath.Join(dir, name), nil
}

func uiOpenFileDialog(_ context.Context, _ wailsrt.OpenDialogOptions) (string, error) {
	return "", errNoFileDialog
}
