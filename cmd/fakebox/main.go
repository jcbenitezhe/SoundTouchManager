// Command fakebox simulates a SoundTouch speaker's firmware on a DEVELOPER
// MACHINE, so the real STM agent and the desktop or phone app can be tried
// without a speaker: presets saved from the app land on the simulated keys,
// and pressing a key plays the station through the control page.
//
// It answers on the firmware's own ports, the ones the agent reaches through
// -box-host: the BoseApp REST API (:8090), the UPnP renderer (:8091), the
// gabbo notification bus (:8080) and the TAP console (:17000). The agent
// hands the speaker http://127.0.0.1:8888/stream/N URLs, so run both on the
// same machine.
//
// Use (developer machines only):
//
//	go run ./cmd/fakebox
//	go run ./cmd/agent -box-host 127.0.0.1 -presets /tmp/fakebox-presets.json \
//	    -apply-hosts=false -tls=false -listen-marge :18080 \
//	    -listen-marge-tls :18443 -listen-bmx :18081
//	open http://127.0.0.1:8095
//
// Then add the speaker in the app by IP (127.0.0.1, or 10.0.2.2 from the
// Android emulator) or let mDNS find the agent. -apply-hosts=false matters:
// without it the agent rewrites this machine's /etc/hosts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	bind := flag.String("bind", "127.0.0.1", "address for the firmware ports (8090, 8091, 8080, 17000); 0.0.0.0 lets other devices reach them")
	uiAddr := flag.String("ui", "127.0.0.1:8095", "address for the control page")
	name := flag.String("name", "Fake SoundTouch", "speaker name")
	model := flag.String("model", "SoundTouch 10", "speaker model, as /info reports it")
	deviceID := flag.String("id", fakeDeviceID, "device ID reported by /info; change it to run a second fakebox")
	logLevel := flag.String("log-level", "info", "debug, info, warn, error")
	flag.Parse()

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintln(os.Stderr, "bad -log-level:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d := newDevice(*name, *model)
	if *deviceID != "" {
		d.id = *deviceID
	}
	servers := []struct {
		label string
		addr  string
		h     http.Handler
	}{
		{"rest", net.JoinHostPort(*bind, "8090"), restHandler(d, logger)},
		{"upnp", net.JoinHostPort(*bind, "8091"), upnpHandler(d, logger)},
		{"gabbo", net.JoinHostPort(*bind, "8080"), gabboHandler(d, logger)},
		{"ui", *uiAddr, uiHandler(d)},
	}
	errc := make(chan error, len(servers)+1)
	var running []*http.Server
	for _, s := range servers {
		ln, err := net.Listen("tcp", s.addr)
		if err != nil {
			logger.Error("listen failed (is a real agent, another fakebox or something else on this port?)", "server", s.label, "addr", s.addr, "err", err)
			os.Exit(1)
		}
		srv := &http.Server{Handler: s.h, ReadHeaderTimeout: 10 * time.Second}
		running = append(running, srv)
		go func(label string) {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", label, err)
			}
		}(s.label)
	}
	tapLn, err := net.Listen("tcp", net.JoinHostPort(*bind, "17000"))
	if err != nil {
		logger.Error("listen failed", "server", "tap", "err", err)
		os.Exit(1)
	}
	go serveTAP(tapLn, d, logger)
	go pullWhenUnattended(ctx, d, logger)

	logger.Info("fakebox ready", "firmware", *bind, "control page", "http://"+*uiAddr)
	select {
	case <-ctx.Done():
	case err := <-errc:
		logger.Error("server failed", "err", err)
	}
	_ = tapLn.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, s := range running {
		_ = s.Shutdown(sctx)
	}
}
