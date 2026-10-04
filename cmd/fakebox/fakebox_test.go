package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jcbenitezhe/SoundTouchManager/internal/boxws"
	"github.com/jcbenitezhe/SoundTouchManager/internal/upnp"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSplitTAPKeepsQuotedLabel(t *testing.T) {
	got := splitTAP(`ws AddPreset UPNP audio http://127.0.0.1:8888/stream/1 "Amor 95.3 CDMX" UPnPUserName 1`)
	want := []string{"ws", "AddPreset", "UPNP", "audio", "http://127.0.0.1:8888/stream/1", "Amor 95.3 CDMX", "UPnPUserName", "1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

// The commands below are the exact shapes internal/boxcli writes.
func TestTAPPresetCommands(t *testing.T) {
	d := newDevice("x", "y")
	if r := runTAP(d, `ws AddPreset UPNP audio http://127.0.0.1:8888/stream/3 "Radio Tres" UPnPUserName 3`); r != "OK" {
		t.Fatalf("AddPreset: %q", r)
	}
	p := d.Snapshot().Presets[2]
	if p == nil || p.Name != "Radio Tres" || p.Location != "http://127.0.0.1:8888/stream/3" || p.Source != "UPNP" {
		t.Fatalf("stored %+v", p)
	}
	if r := runTAP(d, `ws AddPreset UPNP audio http://x "y" UPnPUserName 7`); !strings.Contains(r, "failed") {
		t.Fatalf("slot 7 must be refused like the firmware does, got %q", r)
	}
	if r := runTAP(d, "sys presetkey 3 p"); r != "OK" {
		t.Fatalf("presetkey: %q", r)
	}
	if pb := d.Snapshot().Playback; pb.Slot != 3 || pb.State != "BUFFERING_STATE" {
		t.Fatalf("after key 3: %+v", pb)
	}
	if r := runTAP(d, "ws RemovePreset 3"); r != "OK" || d.Snapshot().Presets[2] != nil {
		t.Fatalf("RemovePreset: %q", r)
	}
	if r := runTAP(d, "sys power"); r != "OK" || d.Snapshot().Playback.Source != "STANDBY" {
		t.Fatalf("power toggle: %q %+v", r, d.Snapshot().Playback)
	}
	if r := runTAP(d, "bogus"); r != "Command not found" {
		t.Fatalf("unknown: %q", r)
	}
}

// The agent reads /presets with this regex (cmd/agent/reconcile.go) and
// scans /now_playing for source=, location= and the play state.
func TestRESTShapesTheAgentParses(t *testing.T) {
	d := newDevice("x", "y")
	_ = d.StorePreset(Preset{Slot: 2, Source: "UPNP", Type: "audio", Location: "http://127.0.0.1:8888/stream/2", Name: "A & B", Account: "UPnPUserName"})
	srv := httptest.NewServer(restHandler(d, quiet))
	defer srv.Close()

	body := get(t, srv.URL+"/presets")
	if !regexp.MustCompile(`<preset id="2"`).MatchString(body) || !strings.Contains(body, "A &amp; B") {
		t.Fatalf("/presets: %s", body)
	}
	if np := get(t, srv.URL+"/now_playing"); !strings.Contains(np, `source="INVALID_SOURCE"`) || strings.Contains(np, "STANDBY") {
		t.Fatalf("idle /now_playing: %s", np)
	}
	d.SetURI("http://127.0.0.1:8888/stream/2", "")
	d.Play()
	np := get(t, srv.URL+"/now_playing")
	for _, want := range []string{`source="UPNP"`, `location="http://127.0.0.1:8888/stream/2"`, "PLAY_STATE", "<itemName>A &amp; B</itemName>"} {
		if !strings.Contains(np, want) {
			t.Fatalf("/now_playing lacks %s: %s", want, np)
		}
	}
	if s := get(t, srv.URL+"/sources"); strings.Contains(s, "LOCAL_INTERNET_RADIO") {
		t.Fatal("/sources must not offer native radio, the agent would leave the UPnP path")
	}
	if info := get(t, srv.URL+"/info"); !strings.Contains(info, `deviceID="`+fakeDeviceID+`"`) || !strings.Contains(info, "<type>y</type>") {
		t.Fatalf("/info: %s", info)
	}
	resp, err := http.Post(srv.URL+"/select", "text/xml", strings.NewReader("<ContentItem/>"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("/select must refuse so the agent falls back to UPnP, got %d", resp.StatusCode)
	}
	for range 2 {
		if s := get(t, srv.URL+"/standby"); !strings.Contains(s, "/standby") {
			t.Fatalf("/standby: %s", s)
		}
		if np := get(t, srv.URL+"/now_playing"); !strings.Contains(np, `source="STANDBY"`) {
			t.Fatalf("/standby must leave the box in standby, not toggle it: %s", np)
		}
	}
}

func TestUPnPWithTheAgentsRenderer(t *testing.T) {
	d := newDevice("x", "y")
	srv := httptest.NewServer(upnpHandler(d, quiet))
	defer srv.Close()
	r := &upnp.Renderer{ControlURL: srv.URL + "/AVTransport/Control", Client: srv.Client()}
	ctx := context.Background()

	if err := r.PlayURL(ctx, "http://127.0.0.1:8888/stream/4?x=1&y=2", "Café FM", ""); err != nil {
		t.Fatal(err)
	}
	pb := d.Snapshot().Playback
	if pb.Location != "http://127.0.0.1:8888/stream/4?x=1&y=2" || pb.State != "PLAY_STATE" || pb.Name != "Café FM" {
		t.Fatalf("after PlayURL: %+v", pb)
	}
	if err := r.Pause(ctx); err != nil || d.TransportState() != "PAUSED_PLAYBACK" {
		t.Fatalf("pause: %v %s", err, d.TransportState())
	}
	if err := r.Stop(ctx); err != nil || d.TransportState() != "STOPPED" {
		t.Fatalf("stop: %v %s", err, d.TransportState())
	}
}

// recorder is a boxws.Handler that keeps what the agent's bus client saw.
type recorder struct {
	mu       sync.Mutex
	selected []string
	presets  [][]boxws.BoxPreset
}

func (h *recorder) OnPresetSelected(_ context.Context, slot int, location, title string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.selected = append(h.selected, string(rune('0'+slot))+" "+location+" "+title)
}
func (h *recorder) OnPresetsChanged(_ context.Context, p []boxws.BoxPreset) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.presets = append(h.presets, p)
}
func (h *recorder) OnRemoteSkip(context.Context, bool)               {}
func (h *recorder) OnUserStop(context.Context)                       {}
func (h *recorder) OnThumbActivity(context.Context)                  {}
func (h *recorder) OnPowerKey(context.Context)                       {}
func (h *recorder) OnSourceAux(context.Context)                      {}
func (h *recorder) OnZoneChanged(context.Context, boxws.ZoneState)   {}
func (h *recorder) OnGroupChanged(context.Context, boxws.GroupState) {}
func (h *recorder) OnPowerWake(context.Context)                      {}

func (h *recorder) snapshot() ([]string, [][]boxws.BoxPreset) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.selected...), append([][]boxws.BoxPreset(nil), h.presets...)
}

// A key press must reach the agent's own gabbo client as a preset selection
// carrying the stored location: that is what starts a recall in the agent.
func TestKeyPressReachesTheAgentsBusClient(t *testing.T) {
	d := newDevice("x", "y")
	srv := httptest.NewServer(gabboHandler(d, quiet))
	defer srv.Close()

	rec := &recorder{}
	c := boxws.New(quiet, "ws"+strings.TrimPrefix(srv.URL, "http")+"/", rec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, "agent connected", func() bool { return c.Connected() && d.bus.Count() == 1 })

	_ = d.StorePreset(Preset{Slot: 2, Source: "UPNP", Type: "audio", Location: "http://127.0.0.1:8888/stream/2", Name: "1LIVE", Account: "UPnPUserName"})
	waitFor(t, "presetsUpdated", func() bool { _, p := rec.snapshot(); return len(p) > 0 })
	_, lists := rec.snapshot()
	if got := lists[len(lists)-1]; len(got) != 1 || got[0].Slot != 2 || got[0].Source != "UPNP" {
		t.Fatalf("box presets seen by the agent: %+v", got)
	}

	if !d.PressPreset(2) {
		t.Fatal("press on a stored key must be accepted")
	}
	waitFor(t, "preset selection", func() bool { s, _ := rec.snapshot(); return len(s) > 0 })
	sel, _ := rec.snapshot()
	if sel[0] != "2 http://127.0.0.1:8888/stream/2 1LIVE" {
		t.Fatalf("selection seen by the agent: %q", sel[0])
	}
	if d.PressPreset(5) {
		t.Fatal("an empty key must be ignored")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
