// tunein.go: playback of TuneIn stations stored as "tunein:<guide id>".
//
// The preset store, favorites and recents keep only the guide id. The stream
// URL TuneIn hands out is temporary and can carry an access key, so it is
// resolved here, at the moment the speaker asks for audio, and never stored.
// The resolved stream is served exactly like any other station: HLS through
// serveHLS, everything else through the raw reconnect loop. No transcoding.

package streamproxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jcbenitezhe/SoundTouchManager/tunein"
)

// tuneInResolver is the slice of *tunein.Client the proxy needs; tests swap it.
type tuneInResolver interface {
	ResolveStream(ctx context.Context, id string) (*tunein.Stream, error)
}

// tuneInResolveTimeout bounds one Tune.ashx round trip so a slow directory
// does not hold the speaker's fetch open indefinitely.
const tuneInResolveTimeout = 10 * time.Second

func (s *Server) tuneInClient() tuneInResolver {
	s.tuneInOnce.Do(func() {
		if s.tuneIn != nil {
			return
		}
		c := tunein.New()
		// Same guarded, clock-tolerant transport as every other upstream fetch.
		c.HTTP = &http.Client{Transport: s.client.Transport, Timeout: tuneInResolveTimeout}
		s.tuneIn = c
	})
	return s.tuneIn
}

// setTuneInResolver replaces the TuneIn directory client (tests only).
func (s *Server) setTuneInResolver(r tuneInResolver) {
	s.tuneInOnce.Do(func() {})
	s.tuneIn = r
}

// serveTuneIn resolves a TuneIn guide id and streams it to the speaker. slot
// is the preset slot for a hardware/preset fetch, 0 for an ad-hoc play.
func (s *Server) serveTuneIn(w http.ResponseWriter, r *http.Request, id string, slot int) {
	ref := tunein.Scheme + id
	resolve := func(ctx context.Context) (string, error) {
		st, err := s.tuneInClient().ResolveStream(ctx, id)
		if err != nil {
			return "", err
		}
		return st.URL, nil
	}
	url, err := resolve(r.Context())
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		s.logger.Warn("stream proxy: TuneIn station could not be resolved", "id", id, "slot", slot, "err", err)
		s.recordFailure(ref, err)
		code := http.StatusBadGateway
		if errors.Is(err, tunein.ErrNoCompatibleStream) || errors.Is(err, tunein.ErrNotFound) || errors.Is(err, tunein.ErrInvalidRef) {
			code = http.StatusNotFound
		}
		http.Error(w, "TuneIn station not available", code)
		return
	}
	s.logger.Info("stream proxy: TuneIn station resolved", "id", id, "slot", slot, "url", tunein.Redact(url))
	switch {
	case isDASHURL(url):
		s.recordFailure(ref, errors.New("dash not supported"))
		http.Error(w, hlsNotPlayableMsg, http.StatusUnsupportedMediaType)
	case isHLSURL(url):
		s.serveTuneInHLS(w, r, id, url, resolve)
	default:
		s.serveRawLoop(w, r, url, resolve)
	}
}

// serveTuneInHLS plays an HLS stream and, when the playlist is rejected (an
// expired access key), resolves the station exactly once more and resumes on
// the same response.
func (s *Server) serveTuneInHLS(w http.ResponseWriter, r *http.Request, id, url string, resolve func(context.Context) (string, error)) {
	ctx := r.Context()
	o := &hlsOpts{stopOnGone: true}
	err := s.serveHLSOpts(ctx, w, r, url, o)
	sent := o.sent
	if err != nil && ctx.Err() == nil && (errors.Is(err, errHLSGone) || isPermanentUpstream(err)) {
		if next, rerr := resolve(ctx); rerr == nil {
			s.logger.Info("stream proxy: TuneIn playlist rejected, resolved a fresh URL once", "id", id, "err", err)
			o2 := &hlsOpts{resumed: sent}
			err = s.serveHLSOpts(ctx, w, r, next, o2)
			sent = sent || o2.sent
		} else {
			s.logger.Warn("stream proxy: TuneIn re-resolve failed", "id", id, "err", rerr)
		}
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	s.logger.Warn("stream proxy: TuneIn HLS playback failed", "id", id, "err", err)
	s.recordFailure(tunein.Scheme+id, err)
	if !sent {
		http.Error(w, hlsNotPlayableMsg, http.StatusUnsupportedMediaType)
	}
}
