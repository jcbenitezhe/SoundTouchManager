package tunein

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Kind classifies a directory item.
type Kind string

const (
	KindLink    Kind = "link"    // opens another directory page (Ref)
	KindStation Kind = "station" // live station, playable by GuideID
	KindShow    Kind = "show"    // podcast or show, opened via Ref
	KindEpisode Kind = "episode" // podcast episode, playable by GuideID
	KindSection Kind = "section" // heading that groups Children
	KindText    Kind = "text"    // informational line, not actionable
)

// Page is one directory listing.
type Page struct {
	Title string `json:"title"`
	Items []Item `json:"items"`
}

// Item is a normalised directory entry.
type Item struct {
	Kind        Kind   `json:"kind"`
	Text        string `json:"text"`
	Subtext     string `json:"subtext,omitempty"`
	GuideID     string `json:"guideId,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Image       string `json:"image,omitempty"`
	Bitrate     int    `json:"bitrate,omitempty"`
	Reliability int    `json:"reliability,omitempty"`
	Formats     string `json:"formats,omitempty"`
	GenreID     string `json:"genreId,omitempty"`
	DurationSec int    `json:"durationSec,omitempty"`
	Children    []Item `json:"children,omitempty"`
}

// Info is metadata for a single id.
type Info struct {
	ID          string `json:"id"`
	Kind        Kind   `json:"kind"`
	Name        string `json:"name"`
	Slogan      string `json:"slogan,omitempty"`
	Image       string `json:"image,omitempty"`
	Location    string `json:"location,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
	Hosts       string `json:"hosts,omitempty"`
}

// Stream is a resolved, temporary playback location. URL is excluded from
// JSON so it cannot leak through an API response by accident.
type Stream struct {
	ID          string `json:"id"`
	URL         string `json:"-"`
	MediaType   string `json:"mediaType"`
	Bitrate     int    `json:"bitrate,omitempty"`
	Reliability int    `json:"reliability,omitempty"`
	HLS         bool   `json:"hls"`
	Finite      bool   `json:"finite"`
}

// flexInt accepts a JSON number, a numeric string, an empty string or null.
// TuneIn sends numbers as strings in outlines and as numbers in Tune.ashx.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(strings.TrimSpace(s))
	}
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

type rawOutline struct {
	Element       string       `json:"element"`
	Type          string       `json:"type"`
	Text          string       `json:"text"`
	URL           string       `json:"URL"`
	GuideID       string       `json:"guide_id"`
	Subtext       string       `json:"subtext"`
	Bitrate       flexInt      `json:"bitrate"`
	Reliability   flexInt      `json:"reliability"`
	Formats       string       `json:"formats"`
	Item          string       `json:"item"`
	Image         string       `json:"image"`
	GenreID       string       `json:"genre_id"`
	TopicDuration flexInt      `json:"topic_duration"`
	Children      []rawOutline `json:"children"`
}

func normalizeOutlines(raw []rawOutline) []Item {
	out := make([]Item, 0, len(raw))
	for _, r := range raw {
		if it, ok := normalizeOutline(r); ok {
			out = append(out, it)
		}
	}
	return out
}

func normalizeOutline(r rawOutline) (Item, bool) {
	it := Item{
		Text:        strings.TrimSpace(r.Text),
		Subtext:     strings.TrimSpace(r.Subtext),
		Image:       httpsImage(r.Image),
		Bitrate:     int(r.Bitrate),
		Reliability: int(r.Reliability),
		Formats:     r.Formats,
		GenreID:     r.GenreID,
	}
	if it.Text == "" {
		return Item{}, false
	}
	switch {
	case r.Children != nil:
		it.Kind = KindSection
		it.Children = normalizeOutlines(r.Children)
		if len(it.Children) == 0 {
			return Item{}, false
		}
	case r.Type == "audio":
		id := normalizeID(r.GuideID)
		if id == "" {
			id = idFromURL(r.URL)
		}
		switch {
		case !guideIDRe.MatchString(id):
			return Item{}, false
		case r.Item == "topic" || idKind(id) == 't':
			it.Kind = KindEpisode
			it.DurationSec = int(r.TopicDuration)
		case idKind(id) == 's':
			it.Kind = KindStation
		default:
			return Item{}, false
		}
		it.GuideID = id
	case r.Type == "link":
		it.Ref = refFromURL(r.URL)
		if it.Ref == "" {
			return Item{}, false
		}
		id := normalizeID(r.GuideID)
		if r.Item == "show" || idKind(id) == 'p' {
			it.Kind = KindShow
			if guideIDRe.MatchString(id) {
				it.GuideID = id
			}
		} else {
			it.Kind = KindLink
		}
	default:
		it.Kind = KindText
	}
	return it, true
}

func idFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), Host) {
		return ""
	}
	return normalizeID(u.Query().Get("id"))
}

// httpsImage upgrades TuneIn CDN artwork to https and drops anything else
// that is not http(s).
func httpsImage(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "https":
	case "http":
		h := strings.ToLower(u.Hostname())
		if h == "tunein.com" || strings.HasSuffix(h, ".tunein.com") {
			u.Scheme = "https"
		}
	default:
		return ""
	}
	return u.String()
}

type rawDescribe struct {
	Element     string `json:"element"`
	GuideID     string `json:"guide_id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Slogan      string `json:"slogan"`
	Logo        string `json:"logo"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Language    string `json:"language"`
	GenreName   string `json:"genre_name"`
	Hosts       string `json:"hosts"`
}

func (r rawDescribe) info(fallbackID string) *Info {
	id := normalizeID(r.GuideID)
	if !guideIDRe.MatchString(id) {
		id = fallbackID
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = strings.TrimSpace(r.Title)
	}
	kind := KindText
	switch idKind(id) {
	case 's':
		kind = KindStation
	case 'p':
		kind = KindShow
	case 't':
		kind = KindEpisode
	}
	return &Info{
		ID:          id,
		Kind:        kind,
		Name:        name,
		Slogan:      strings.TrimSpace(r.Slogan),
		Image:       httpsImage(r.Logo),
		Location:    strings.TrimSpace(r.Location),
		Genre:       strings.TrimSpace(r.GenreName),
		Language:    strings.TrimSpace(r.Language),
		Description: strings.TrimSpace(r.Description),
		Hosts:       strings.TrimSpace(r.Hosts),
	}
}

type rawStream struct {
	Element     string  `json:"element"`
	URL         string  `json:"url"`
	Reliability flexInt `json:"reliability"`
	Bitrate     flexInt `json:"bitrate"`
	MediaType   string  `json:"media_type"`
}

var formatRank = map[string]int{"mp3": 0, "aac": 1, "hls": 2}

func rank(mt string) int {
	if r, ok := formatRank[mt]; ok {
		return r
	}
	return len(formatRank)
}

// pickStream chooses the most reliable usable stream; at equal reliability
// plain MP3 beats AAC beats HLS, because the speaker handles them in that
// order of robustness.
func pickStream(raw []rawStream) (*Stream, error) {
	var best *Stream
	for _, r := range raw {
		if r.Element != "audio" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(r.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || isPlaceholder(u) {
			continue
		}
		mt := strings.ToLower(strings.TrimSpace(r.MediaType))
		st := &Stream{
			URL:         u.String(),
			MediaType:   mt,
			Bitrate:     int(r.Bitrate),
			Reliability: int(r.Reliability),
			HLS:         mt == "hls" || strings.HasSuffix(strings.ToLower(u.Path), ".m3u8"),
		}
		if best == nil || st.Reliability > best.Reliability ||
			(st.Reliability == best.Reliability && rank(st.MediaType) < rank(best.MediaType)) {
			best = st
		}
	}
	if best == nil {
		return nil, ErrNoCompatibleStream
	}
	return best, nil
}

// isPlaceholder spots the "not compatible / not available" announcement
// MP3s TuneIn returns instead of an error.
func isPlaceholder(u *url.URL) bool {
	if strings.EqualFold(u.Hostname(), "cdn-cms.tunein.com") {
		return true
	}
	p := strings.ToLower(u.Path)
	return strings.Contains(p, "/service/audio/") &&
		(strings.Contains(p, "notcompatible") || strings.Contains(p, "notavailable"))
}
