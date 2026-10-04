package webui

import (
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcbenitezhe/SoundTouchManager/internal/presets"
)

// newSaveGateServer builds a Server whose preset store persists into a temp
// dir, with no box behind it (boxHost empty skips the hardware sync).
func newSaveGateServer(t *testing.T) *Server {
	t.Helper()
	store, err := presets.Load(filepath.Join(t.TempDir(), "presets.json"))
	if err != nil {
		t.Fatalf("presets.Load: %v", err)
	}
	return &Server{
		presets: store,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func putPreset(t *testing.T, s *Server, slot int, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("PUT", fmt.Sprintf("/api/presets/%d", slot), strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handlePresetSlot(w, r)
	return w
}

// A type=radio preset with an EMPTY stream URL used to slip through the
// invalid-URL gate ("StreamURL != "" && !isHTTPURL") and save as a dead
// preset: assignable, but the box's /stream/<slot> fetch 404s forever.
// It must be rejected with 422 and never reach the store.
func TestPresetSaveRejectsEmptyStreamURL(t *testing.T) {
	s := newSaveGateServer(t)
	w := putPreset(t, s, 3, `{"name":"Absolut Relax","type":"radio","stream_url":""}`)
	if w.Code != 422 {
		t.Fatalf("empty stream URL: status = %d, want 422 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "stream-url-missing") {
		t.Errorf("want code stream-url-missing, got %s", w.Body.String())
	}
	if _, ok := s.presets.Get(3); ok {
		t.Error("dead preset (empty stream URL) was stored anyway")
	}
}

// A well-formed radio preset must still save.
func TestPresetSaveAcceptsRadioURL(t *testing.T) {
	s := newSaveGateServer(t)
	w := putPreset(t, s, 2, `{"name":"Absolut Relax","type":"radio","stream_url":"http://stream.example/relax","codec":"AAC+"}`)
	if w.Code != 200 {
		t.Fatalf("valid radio preset: status = %d (body %s)", w.Code, w.Body.String())
	}
	p, ok := s.presets.Get(2)
	if !ok || p.StreamURL != "http://stream.example/relax" {
		t.Fatalf("stored preset = %+v, ok=%v", p, ok)
	}
	// The station codec must round-trip so recalls can label AAC correctly.
	if p.Codec != "AAC+" {
		t.Errorf("stored codec = %q, want AAC+", p.Codec)
	}
}

// A preset with no stream URL but a replayable Spotify URI is a mis-typed
// Spotify preset: heal it instead of rejecting (keeps the other playable-content
// path working).
func TestPresetSaveHealsSpotifyURIWithEmptyStreamURL(t *testing.T) {
	s := newSaveGateServer(t)
	w := putPreset(t, s, 4, `{"name":"My List","type":"radio","stream_url":"","uri":"spotify:playlist:37i9dQZF1DWX7rdRjOECPW"}`)
	if w.Code != 200 {
		t.Fatalf("spotify-uri heal: status = %d (body %s)", w.Code, w.Body.String())
	}
	p, ok := s.presets.Get(4)
	if !ok || p.Type != "spotify" {
		t.Fatalf("stored preset = %+v, ok=%v, want healed type=spotify", p, ok)
	}
}

// A TuneIn preset stores only the station id; the stream proxy resolves the
// temporary stream URL at play time.
func TestPresetSaveAcceptsTuneInRef(t *testing.T) {
	s := newSaveGateServer(t)
	w := putPreset(t, s, 6, `{"name":"Apple Music Club","type":"radio","stream_url":"tunein:s345726","codec":"AAC"}`)
	if w.Code != 200 {
		t.Fatalf("tunein preset: status = %d (body %s)", w.Code, w.Body.String())
	}
	if p, ok := s.presets.Get(6); !ok || p.StreamURL != "tunein:s345726" {
		t.Fatalf("stored preset = %+v, ok=%v", p, ok)
	}
	for _, bad := range []string{"tunein:p1909239", "tunein:", "tunein:s1?accessKey=x"} {
		w := putPreset(t, s, 5, `{"name":"x","type":"radio","stream_url":"`+bad+`"}`)
		if w.Code != 422 {
			t.Errorf("%q: status = %d, want 422", bad, w.Code)
		}
	}
}

// The pre-existing gate for a malformed (non-http, non-spotify) URL must keep
// rejecting.
func TestPresetSaveRejectsNonHTTPURL(t *testing.T) {
	s := newSaveGateServer(t)
	w := putPreset(t, s, 5, `{"name":"Broken","type":"radio","stream_url":"/v1/playback/station/x"}`)
	if w.Code != 422 {
		t.Fatalf("non-http stream URL: status = %d, want 422 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "stream-url-invalid") {
		t.Errorf("want code stream-url-invalid, got %s", w.Body.String())
	}
}
