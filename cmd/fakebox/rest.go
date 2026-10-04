package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	reAccountID = regexp.MustCompile(`<accountId>([^<]*)</accountId>`)
	reKey       = regexp.MustCompile(`<key[^>]*state="([^"]*)"[^>]*>([^<]*)</key>`)
	reVolume    = regexp.MustCompile(`<volume>\s*(\d+)\s*</volume>`)
)

// restHandler is the BoseApp REST API on :8090: the subset the agent and the
// desktop app read. Anything else answers the firmware's 404 error shape so
// an unexpected call shows up in the log instead of silently succeeding.
func restHandler(d *Device, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	xml := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		xml(w, d.InfoXML(localIP(r)))
	})
	mux.HandleFunc("GET /now_playing", func(w http.ResponseWriter, r *http.Request) { xml(w, d.NowPlayingXML()) })
	mux.HandleFunc("GET /presets", func(w http.ResponseWriter, r *http.Request) { xml(w, d.PresetsXML()) })
	// No LOCAL_INTERNET_RADIO here: the agent then keeps every preset on the
	// UPnP path, the one STM uses on boxes without a TuneIn account.
	mux.HandleFunc("GET /sources", func(w http.ResponseWriter, r *http.Request) {
		xml(w, fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?><sources deviceID="%s"><sourceItem source="UPNP" sourceAccount="UPnPUserName" status="READY" isLocal="false" multiroomallowed="true">UPnPUserName</sourceItem><sourceItem source="AUX" sourceAccount="AUX" status="READY" isLocal="true" multiroomallowed="true">AUX IN</sourceItem></sources>`, fakeDeviceID))
	})
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		xml(w, `<?xml version="1.0" encoding="UTF-8" ?><setupState state="SETUP_LEAVE" />`)
	})
	mux.HandleFunc("POST /setup", func(w http.ResponseWriter, r *http.Request) { xml(w, `<status>/setup</status>`) })
	mux.HandleFunc("POST /setMargeAccount", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		id := "fake-account"
		if m := reAccountID.FindSubmatch(body); m != nil && len(m[1]) > 0 {
			id = string(m[1])
		}
		d.SetAccount(id)
		xml(w, `<status>/setMargeAccount</status>`)
	})
	// Native TuneIn-style selection is refused, so the agent falls back to UPnP.
	mux.HandleFunc("POST /select", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		xml(w, fmt.Sprintf(`<errors deviceID="%s"><error value="1005" name="UNKNOWN_SOURCE_ERROR" severity="Unknown">fakebox has no native radio</error></errors>`, fakeDeviceID))
	})
	mux.HandleFunc("POST /key", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if m := reKey.FindSubmatch(body); m != nil && string(m[1]) == "release" {
			key := string(m[2])
			switch {
			case strings.HasPrefix(key, "PRESET_"):
				if n, err := strconv.Atoi(strings.TrimPrefix(key, "PRESET_")); err == nil {
					d.PressPreset(n)
				}
			case key == "POWER":
				d.TogglePower()
			case key == "PLAY":
				d.Play()
			case key == "PAUSE":
				d.Pause()
			case key == "STOP":
				d.Stop()
			}
		}
		xml(w, `<status>/key</status>`)
	})
	// GET like the firmware, which answers a POST to /standby with 400.
	mux.HandleFunc("GET /standby", func(w http.ResponseWriter, r *http.Request) {
		d.Standby()
		xml(w, `<status>/standby</status>`)
	})
	mux.HandleFunc("GET /volume", func(w http.ResponseWriter, r *http.Request) { xml(w, d.VolumeXML()) })
	mux.HandleFunc("POST /volume", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if m := reVolume.FindSubmatch(body); m != nil {
			v, _ := strconv.Atoi(string(m[1]))
			d.SetVolume(v)
		}
		xml(w, `<status>/volume</status>`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("rest: not implemented", "method", r.Method, "path", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		xml(w, fmt.Sprintf(`<errors deviceID="%s"><error value="404" name="HTTP_STATUS_NOT_FOUND" severity="Unknown">%s is not implemented by fakebox</error></errors>`,
			fakeDeviceID, esc(r.URL.Path)))
	})
	return mux
}

// localIP is the address the request reached, reported as the speaker's IP.
func localIP(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if host, _, err := net.SplitHostPort(a.String()); err == nil {
			return host
		}
	}
	return "127.0.0.1"
}
