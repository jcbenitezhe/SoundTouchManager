package streamproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jcbenitezhe/SoundTouchManager/internal/logredact"
	"github.com/jcbenitezhe/SoundTouchManager/internal/presets"
	"github.com/jcbenitezhe/SoundTouchManager/tunein"
)

// fakeTuneIn hands out the given URLs in order, one per resolution.
type fakeTuneIn struct {
	mu   sync.Mutex
	urls []string
	err  error
	ids  []string
}

func (f *fakeTuneIn) ResolveStream(_ context.Context, id string) (*tunein.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.urls) == 0 {
		return nil, tunein.ErrNoCompatibleStream
	}
	u := f.urls[0]
	if len(f.urls) > 1 {
		f.urls = f.urls[1:]
	}
	return &tunein.Stream{ID: id, URL: u, MediaType: "hls", HLS: true}, nil
}

func (f *fakeTuneIn) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ids)
}

// packedAudio is an ID3-tagged ADTS segment, the shape Apple's TuneIn HLS
// streams ship.
var packedAudio = append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), 0xFF, 0xF1, 0x50, 0x80, 0x01, 0x7F, 0xFC, 0xAA, 0xBB)

// hlsUpstream serves a VOD playlist at /ok/... and 403 at /expired/....
func hlsUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/expired/"):
			http.Error(w, "expired", http.StatusForbidden)
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg0.aac\n#EXT-X-ENDLIST\n"))
		case strings.HasSuffix(r.URL.Path, ".aac"):
			_, _ = w.Write(packedAudio)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	return up
}

func tuneInProxy(t *testing.T, store *presets.Store, f *fakeTuneIn, logs *bytes.Buffer) *httptest.Server {
	t.Helper()
	lg := silentLogger()
	if logs != nil {
		lg = slog.New(logredact.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	s := New(store, lg)
	s.client = &http.Client{} // the production client refuses loopback dials
	s.setTuneInResolver(f)
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func rawURL(base, inner string) string {
	return base + "/stream/raw?u=" + base64.RawURLEncoding.EncodeToString([]byte(inner))
}

func get(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), b.Bytes()
}

func TestTuneInAdHocPlayResolvesAndStreams(t *testing.T) {
	up := hlsUpstream(t)
	f := &fakeTuneIn{urls: []string{up.URL + "/ok/index.m3u8?accessKey=SECRET123"}}
	var logs bytes.Buffer
	srv := tuneInProxy(t, nil, f, &logs)

	code, ct, body := get(t, rawURL(srv.URL, "tunein:s345726"))
	if code != http.StatusOK || ct != "audio/aac" || !bytes.Equal(body, packedAudio) {
		t.Fatalf("got %d %q %d bytes", code, ct, len(body))
	}
	if f.calls() != 1 || f.ids[0] != "s345726" {
		t.Errorf("resolver calls = %v", f.ids)
	}
	if strings.Contains(logs.String(), "SECRET123") {
		t.Errorf("access key reached the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "accessKey=[REDACTED]") {
		t.Errorf("resolved URL not logged in redacted form:\n%s", logs.String())
	}
}

func TestTuneInPresetSlotResolvesAtFetchTime(t *testing.T) {
	up := hlsUpstream(t)
	store, err := presets.Load(filepath.Join(t.TempDir(), "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSlot(presets.Preset{Slot: 2, Name: "Apple Music Club", Type: "radio", StreamURL: "tunein:s345726", Codec: "AAC"}); err != nil {
		t.Fatal(err)
	}
	f := &fakeTuneIn{urls: []string{up.URL + "/ok/a.m3u8"}}
	srv := tuneInProxy(t, store, f, nil)

	for i := 1; i <= 2; i++ {
		code, _, body := get(t, srv.URL+"/stream/2")
		if code != http.StatusOK || !bytes.Equal(body, packedAudio) {
			t.Fatalf("fetch %d: %d, %d bytes", i, code, len(body))
		}
		if f.calls() != i {
			t.Errorf("fetch %d: %d resolutions, want one per fetch", i, f.calls())
		}
	}
	if p, _ := store.Get(2); p.StreamURL != "tunein:s345726" {
		t.Errorf("the store was rewritten to %q; only the id may be persisted", p.StreamURL)
	}
}

func TestTuneInExpiredURLIsResolvedAgainOnce(t *testing.T) {
	up := hlsUpstream(t)
	f := &fakeTuneIn{urls: []string{up.URL + "/expired/a.m3u8", up.URL + "/ok/b.m3u8"}}
	srv := tuneInProxy(t, nil, f, nil)

	code, _, body := get(t, rawURL(srv.URL, "tunein:s1"))
	if code != http.StatusOK || !bytes.Equal(body, packedAudio) {
		t.Fatalf("got %d, %d bytes", code, len(body))
	}
	if f.calls() != 2 {
		t.Errorf("resolutions = %d, want 2", f.calls())
	}
}

func TestTuneInRetriesOnlyOnce(t *testing.T) {
	up := hlsUpstream(t)
	f := &fakeTuneIn{urls: []string{up.URL + "/expired/a.m3u8"}}
	srv := tuneInProxy(t, nil, f, nil)

	code, _, _ := get(t, rawURL(srv.URL, "tunein:s1"))
	if code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", code)
	}
	if f.calls() != 2 {
		t.Errorf("resolutions = %d, want exactly 2 (no retry loop)", f.calls())
	}
}

func TestTuneInUnavailableStationReportsGone(t *testing.T) {
	f := &fakeTuneIn{err: tunein.ErrNoCompatibleStream}
	srv := tuneInProxy(t, nil, f, nil)

	if code, _, _ := get(t, rawURL(srv.URL, "tunein:s999")); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
	_, _, status := get(t, srv.URL+"/api/stream-status")
	if !strings.Contains(string(status), `"reason":"gone"`) || !strings.Contains(string(status), `"url":"tunein:s999"`) {
		t.Errorf("stream-status = %s", status)
	}
}

func TestTuneInRefusesNonPlayableRefs(t *testing.T) {
	f := &fakeTuneIn{}
	srv := tuneInProxy(t, nil, f, nil)
	if code, _, _ := get(t, rawURL(srv.URL, "tunein:p1909239")); code != http.StatusBadRequest {
		t.Errorf("show id: status = %d, want 400", code)
	}
	if f.calls() != 0 {
		t.Errorf("a non-playable ref reached the resolver")
	}
}
