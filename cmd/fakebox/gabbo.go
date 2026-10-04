package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// gabboHandler is the firmware's notification bus on :8080. The agent dials it
// with the "gabbo" subprotocol, sends nothing but pings, and reacts to the
// <updates> frames pushed here.
func gabboHandler(d *Device, logger *slog.Logger) http.Handler {
	up := websocket.Upgrader{
		Subprotocols: []string{"gabbo"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		logger.Info("gabbo: agent connected", "remote", r.RemoteAddr)
		ch := d.bus.Subscribe()
		defer d.bus.Unsubscribe(ch)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-done:
				logger.Info("gabbo: agent disconnected")
				return
			case msg := <-ch:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
				logger.Debug("gabbo: sent", "frame", string(msg))
			}
		}
	})
}
