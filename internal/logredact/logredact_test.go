package logredact

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

const secretURL = "https://itsliveradio.example/x/index-ts.m3u8?accessKey=SECRET123"

func TestRedactsEveryShape(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	u, _ := url.Parse(secretURL)

	lg.Info("play request url="+secretURL, "url", secretURL)
	lg.Warn("fetch", "err", fmt.Errorf("Get %q: 403", secretURL))
	lg.Info("stringer", "u", u)
	lg.With("station", secretURL).Info("with")
	lg.WithGroup("g").Info("group", slog.Group("inner", "url", secretURL))
	lg.Debug("plain", "url", "https://plain.example/stream.mp3", "n", 3, "err", errors.New("boom"))

	out := buf.String()
	if strings.Contains(out, "SECRET123") {
		t.Fatalf("token leaked:\n%s", out)
	}
	if got := strings.Count(out, "accessKey=[REDACTED]"); got < 6 {
		t.Errorf("expected every shape redacted, got %d:\n%s", got, out)
	}
	for _, keep := range []string{"https://plain.example/stream.mp3", "n=3", "err=boom"} {
		if !strings.Contains(out, keep) {
			t.Errorf("non-secret attr %q lost:\n%s", keep, out)
		}
	}
}

func TestEnabledFollowsNext(t *testing.T) {
	h := New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(t.Context(), slog.LevelInfo) || !h.Enabled(t.Context(), slog.LevelError) {
		t.Error("Enabled does not follow the wrapped handler")
	}
}
