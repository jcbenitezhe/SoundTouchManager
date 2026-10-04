// App-side TuneIn directory: browse, search, station details and shared-link
// resolution. Like radio search, the catalogue queries run in the app; the
// speaker only ever receives "tunein:<id>" and resolves the temporary stream
// URL itself at play time, so no access key crosses the LAN. The one place a
// resolved URL reaches the UI is TuneInListenURL, for playing on this device,
// and the frontend holds it only in its audio element.
package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jcbenitezhe/SoundTouchManager/tunein"
)

var tuneInClient = tunein.New()

func (a *App) tuneInCtx() (context.Context, context.CancelFunc) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 20*time.Second)
}

// TuneInBrowse opens a directory reference from an earlier page ("" is the
// root). Only TuneIn directory references are accepted.
func (a *App) TuneInBrowse(ref string) (*tunein.Page, error) {
	ctx, cancel := a.tuneInCtx()
	defer cancel()
	return nonNilPage(tuneInClient.Browse(ctx, ref))
}

// TuneInSearch searches stations, shows and episodes.
func (a *App) TuneInSearch(query string) (*tunein.Page, error) {
	ctx, cancel := a.tuneInCtx()
	defer cancel()
	return nonNilPage(tuneInClient.Search(ctx, query))
}

// TuneInStation is what the frontend needs to play or save a TuneIn item.
type TuneInStation struct {
	tunein.Info
	// Ref is the canonical value to play and to store ("tunein:s345726");
	// empty for a show, which is opened rather than played.
	Ref string `json:"ref,omitempty"`
	// Codec labels the stream for the speaker's decoder (AAC or MP3).
	Codec    string `json:"codec,omitempty"`
	Bitrate  int    `json:"bitrate,omitempty"`
	Finite   bool   `json:"finite,omitempty"`
	Playable bool   `json:"playable"`
	// Reason is set when Playable is false: "unavailable" (no stream STM can
	// play right now) or "show" (open it to pick an episode).
	Reason string `json:"reason,omitempty"`
}

// TuneInStationInfo describes an id and checks that it currently resolves to
// a stream the speaker can play.
func (a *App) TuneInStationInfo(id string) (TuneInStation, error) {
	ctx, cancel := a.tuneInCtx()
	defer cancel()
	return tuneInStation(ctx, id)
}

// TuneInResolveShare accepts what the TuneIn app's Share sheet produces (a
// tunein.com link, a tun.in short link, or text containing one) and returns
// the item behind it.
func (a *App) TuneInResolveShare(text string) (TuneInStation, error) {
	ctx, cancel := a.tuneInCtx()
	defer cancel()
	id, err := tuneInClient.ResolveShareLink(ctx, text)
	if err != nil {
		return TuneInStation{}, err
	}
	return tuneInStation(ctx, id)
}

// TuneInListen is a freshly resolved stream for playback on this device.
type TuneInListen struct {
	URL    string `json:"url"`
	HLS    bool   `json:"hls"`
	Finite bool   `json:"finite,omitempty"`
}

// TuneInListenURL resolves a station or episode for the app's own player. The
// URL is temporary and may carry an access key: never log or store it.
func (a *App) TuneInListenURL(id string) (TuneInListen, error) {
	ctx, cancel := a.tuneInCtx()
	defer cancel()
	st, err := tuneInClient.ResolveStream(ctx, id)
	if err != nil {
		return TuneInListen{}, err
	}
	return TuneInListen{URL: st.URL, HLS: st.HLS, Finite: st.Finite}, nil
}

func tuneInStation(ctx context.Context, id string) (TuneInStation, error) {
	info, err := tuneInClient.Describe(ctx, id)
	if err != nil {
		return TuneInStation{}, err
	}
	out := TuneInStation{Info: *info}
	if !tunein.IsPlayableID(info.ID) {
		out.Reason = "show"
		return out, nil
	}
	st, err := tuneInClient.ResolveStream(ctx, info.ID)
	switch {
	case errors.Is(err, tunein.ErrNoCompatibleStream), errors.Is(err, tunein.ErrNotFound):
		out.Reason = "unavailable"
		return out, nil
	case err != nil:
		return TuneInStation{}, err
	}
	out.Ref = tunein.Ref(info.ID)
	out.Codec = tuneInCodec(st)
	out.Bitrate = st.Bitrate
	out.Finite = st.Finite
	out.Playable = true
	return out, nil
}

// tuneInCodec maps the stream type onto the codec names presets already use.
// HLS reaches the speaker as ADTS AAC after the agent's demux.
func tuneInCodec(st *tunein.Stream) string {
	switch {
	case st.HLS, strings.EqualFold(st.MediaType, "aac"):
		return "AAC"
	case strings.EqualFold(st.MediaType, "mp3"):
		return "MP3"
	}
	return ""
}

func nonNilPage(p *tunein.Page, err error) (*tunein.Page, error) {
	if p == nil {
		p = &tunein.Page{}
	}
	if p.Items == nil {
		p.Items = []tunein.Item{}
	}
	return p, err
}
