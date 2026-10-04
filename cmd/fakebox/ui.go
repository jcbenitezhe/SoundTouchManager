package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

//go:embed ui.html
var uiPage []byte

// uiHandler is the control page: the speaker's front panel (keys 1-6, power,
// volume) plus its loudspeaker, an <audio> element playing whatever the
// agent told the "speaker" to play.
func uiHandler(d *Device) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(uiPage)
	})
	mux.HandleFunc("GET /ui/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d.Snapshot())
	})
	mux.HandleFunc("GET /ui/events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch := d.ui.Subscribe()
		defer d.ui.Unsubscribe(ch)
		first, _ := json.Marshal(d.Snapshot())
		_, _ = fmt.Fprintf(w, "data: %s\n\n", first)
		fl.Flush()
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
				fl.Flush()
			case <-ping.C:
				_, _ = io.WriteString(w, ": ping\n\n")
				fl.Flush()
			}
		}
	})
	mux.HandleFunc("POST /ui/press/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.PathValue("n"))
		d.PressPreset(n)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /ui/power", func(w http.ResponseWriter, r *http.Request) {
		d.TogglePower()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /ui/stop", func(w http.ResponseWriter, r *http.Request) {
		d.Stop()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /ui/volume", func(w http.ResponseWriter, r *http.Request) {
		v, err := strconv.Atoi(r.URL.Query().Get("v"))
		if err != nil {
			http.Error(w, "v must be 0..100", http.StatusBadRequest)
			return
		}
		d.SetVolume(v)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// pullWhenUnattended consumes the playing stream while no control page is
// open. A real speaker pulls the stream it was told to play, and the agent
// verifies a recall by watching that pull; without it the agent would keep
// re-pushing. With a page open, the page's <audio> is the one pulling.
func pullWhenUnattended(ctx context.Context, d *Device, logger *slog.Logger) {
	var (
		cur    string
		cancel context.CancelFunc
	)
	stop := func() {
		if cancel != nil {
			cancel()
			cancel = nil
		}
		cur = ""
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		want := ""
		if d.ui.Count() == 0 {
			want = d.Playing()
		}
		if want != cur {
			stop()
			if want != "" {
				cur = want
				pctx, c := context.WithCancel(ctx)
				cancel = c
				go pull(pctx, want, logger)
			}
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case <-t.C:
		}
	}
}

func pull(ctx context.Context, url string, logger *slog.Logger) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("speaker: stream pull failed", "url", url, "err", err)
		}
		return
	}
	defer resp.Body.Close()
	logger.Info("speaker: pulling stream (no control page open)", "url", url, "status", resp.StatusCode)
	_, _ = io.Copy(io.Discard, resp.Body)
}
