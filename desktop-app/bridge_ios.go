//go:build stmbridge

// iOS entry point. An iOS app may not start another executable, so the bridge
// is built with -buildmode=c-archive and linked into the app, which calls
// STMBridgeStart from Swift instead of reading STM_BRIDGE_LISTEN from a child.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"log/slog"
	"net"
	"os"
	"sync"
)

// iOS reclaims the sockets of a suspended app, and the backend lives in the
// app process, so it cannot be restarted on its own.
const bridgeRelisten = true

// bridgePlatformInit undoes the pure-Go resolver: an app sandbox has no
// /etc/resolv.conf, and the system resolver follows Wi-Fi and VPN changes.
func bridgePlatformInit() {
	net.DefaultResolver.PreferGo = false
}

var iosBridge struct {
	once sync.Once
	addr string
	err  error
}

// STMBridgeStart starts the backend once and returns "host:port", or
// "error: ..." when it could not start. Later calls return the first result.
// The Go runtime reads the environment when the app loads, before Swift runs,
// so the host's settings arrive as arguments. The caller frees the result.
//
//export STMBridgeStart
func STMBridgeStart(addr, token, exportDir *C.char) *C.char {
	iosBridge.once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				logCrash("bridge", r)
				panic(r)
			}
		}()
		_ = os.Setenv("STM_BRIDGE_ADDR", C.GoString(addr))
		_ = os.Setenv("STM_BRIDGE_TOKEN", C.GoString(token))
		_ = os.Setenv("STM_EXPORT_DIR", C.GoString(exportDir))

		srv, serve, _, err := startBridge(context.Background())
		if err != nil {
			iosBridge.err = err
			return
		}
		iosBridge.addr = srv.addr
		go func() {
			if err := serve(); err != nil {
				slog.Error("bridge: serve", slog.Any("err", err))
			}
		}()
	})
	if iosBridge.err != nil {
		return C.CString("error: " + iosBridge.err.Error())
	}
	return C.CString(iosBridge.addr)
}
