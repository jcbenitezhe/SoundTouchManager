package tunein

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const rootJSON = `{ "head": {"title": "Browse", "status": "200"}, "body": [
 { "element" : "outline", "type" : "link", "text" : "Local Radio", "URL" : "http://opml.radiotime.com/Browse.ashx?c=local", "key" : "local" },
 { "element" : "outline", "type" : "link", "text" : "Music", "URL" : "http://opml.radiotime.com/Browse.ashx?c=music", "key" : "music" },
 { "element" : "outline", "type" : "link", "text" : "By Location", "URL" : "http://opml.radiotime.com/Browse.ashx?id=r0", "key" : "location" },
 { "element" : "outline", "type" : "link", "text" : "Elsewhere", "URL" : "http://evil.example/Browse.ashx?c=x" },
 { "element" : "outline", "type" : "link", "text" : "Podcasts", "URL" : "http://opml.radiotime.com/Browse.ashx?c=podcast", "key" : "podcast" }
] }`

const genreJSON = `{ "head": {"title": "Dance", "status": "200"}, "body": [
 { "element" : "outline", "text" : "Local Stations (1)", "key" : "local", "children" : [
   { "element" : "outline", "type" : "audio", "text" : "BEAT 100.9", "URL" : "http://opml.radiotime.com/Tune.ashx?id=s87618",
     "bitrate" : "128", "reliability" : "98", "guide_id" : "s87618", "subtext" : "Dance hits", "formats" : "mp3",
     "item" : "station", "image" : "http://cdn-radiotime-logos.tunein.com/s87618q.png" } ] },
 { "element" : "outline", "text" : "Empty group", "children" : [] },
 { "element" : "outline", "type" : "audio", "text" : "No id station", "URL" : "http://opml.radiotime.com/Tune.ashx?id=" },
 { "element" : "outline", "type" : "link", "text" : "More Stations", "URL" : "http://opml.radiotime.com/Browse.ashx?id=g59&filter=s&offset=26" }
] }`

const searchJSON = `{ "head": {"title": "Search Results: apple", "status": "200"}, "body": [
 { "element" : "outline", "type" : "link", "text" : "The Mix New Music Club", "URL" : "http://opml.radiotime.com/Tune.ashx?c=pbrowse&id=p1909239",
   "guide_id" : "p1909239", "subtext" : "Emerging artists", "item" : "show", "image" : "http://cdn-profiles.tunein.com/p1909239/images/logoq.png?t=3" },
 { "element" : "outline", "type" : "audio", "text" : "Apple FM", "URL" : "http://opml.radiotime.com/Tune.ashx?id=s120798",
   "bitrate" : "64", "reliability" : "10", "guide_id" : "s120798", "subtext" : "Yesterday's Best Music", "genre_id" : "g2755",
   "formats" : "mp3", "item" : "station", "image" : "http://cdn-radiotime-logos.tunein.com/s120798q.png" },
 { "element" : "outline", "text" : "Search results (not playable)" }
] }`

const showJSON = `{ "head": {"title": "The Mix New Music Club Listening Options", "status": "200"}, "body": [
 { "element" : "outline", "text" : "Recent Episodes", "key" : "topics", "children" : [
   { "element" : "outline", "type" : "audio", "text" : "Episode one (6m)", "URL" : "http://opml.radiotime.com/Tune.ashx?id=t569445012&sid=p1909239",
     "guide_id" : "t569445012", "stream_type" : "download", "topic_duration" : "412", "subtext" : "Sunday Aug 2", "item" : "topic" } ] }
] }`

const describeJSON = `{ "head": {"status": "200"}, "body": [
{"element":"station","guide_id":"s345726","name":"Apple Music Club","slogan":"Where the mix never ends.",
 "logo":"https://cdn-profiles.tunein.com/s345726/images/logoq.png?t=1","location":"US","description":"Dance and electronic.",
 "language":"English","genre_id":"g59","genre_name":"Dance & Electronic","is_available":false}] }`

const tuneHLSJSON = `{ "head": {"status": "200"}, "body": [
 { "element" : "audio", "url": "https://itsliveradio.apple.com/3p-tune-in/tune_in/1740613859-ts/index-ts.m3u8?accessKey=FAKE_TOKEN_123",
   "reliability": 100, "bitrate": 64, "media_type": "hls", "position": 0, "is_hls_advanced": "false", "is_direct": true }] }`

const tunePlaceholderJSON = `{ "head": {"status": "200"}, "body": [
 { "element" : "audio", "url": "http://cdn-cms.tunein.com/service/Audio/notcompatible.enUS.mp3",
   "reliability": 100, "bitrate": 24, "media_type": "mp3", "is_direct": true }] }`

const tuneMixedJSON = `{ "head": {"status": "200"}, "body": [
 { "element" : "audio", "url": "https://hls.example/live.m3u8", "reliability": 90, "bitrate": 128, "media_type": "hls" },
 { "element" : "audio", "url": "https://mp3.example/live", "reliability": 90, "bitrate": 128, "media_type": "mp3" },
 { "element" : "audio", "url": "https://aac.example/live", "reliability": 40, "bitrate": 64, "media_type": "aac" },
 { "element" : "audio", "url": "ftp://bad.example/x", "reliability": 100, "media_type": "mp3" },
 { "element" : "playlist", "url": "https://x.example/y", "reliability": 100, "media_type": "mp3" }
] }`

const tuneEpisodeJSON = `{ "head": {"status": "200"}, "body": [
 { "element" : "audio", "url": "https://podcast.example/ep.mp3?rss_browser=abc", "reliability": 75, "bitrate": 20, "media_type": "mp3" }] }`

// testServer answers each endpoint from a map keyed by "Endpoint?c=..&id=..",
// "Endpoint?id=..", "Endpoint?c=.." or the endpoint alone, and counts requests.
func testServer(t *testing.T, routes map[string]string) (*Client, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Query().Get("render") != "json" {
			http.Error(w, "render=json missing", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("formats") != "mp3,aac,hls" {
			http.Error(w, "formats missing, HLS-only stations would read Not Supported", http.StatusBadRequest)
			return
		}
		ep := strings.TrimPrefix(r.URL.Path, "/")
		q := r.URL.Query()
		for _, key := range []string{
			ep + "?c=" + q.Get("c") + "&id=" + q.Get("id"),
			ep + "?id=" + q.Get("id"),
			ep + "?c=" + q.Get("c"),
			ep,
		} {
			if body, ok := routes[key]; ok {
				if body == "HTTP500" {
					http.Error(w, "boom", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.base = srv.URL
	return c, &hits
}

func TestRootNormalisesLinksAndDropsForeignHosts(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Browse.ashx": rootJSON})
	p, err := c.Root(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Browse" {
		t.Errorf("title = %q", p.Title)
	}
	want := []struct{ text, ref string }{
		{"Local Radio", "Browse.ashx?c=local"},
		{"Music", "Browse.ashx?c=music"},
		{"By Location", "Browse.ashx?id=r0"},
		{"Podcasts", "Browse.ashx?c=podcast"},
	}
	if len(p.Items) != len(want) {
		t.Fatalf("items = %+v", p.Items)
	}
	for i, w := range want {
		if p.Items[i].Kind != KindLink || p.Items[i].Text != w.text || p.Items[i].Ref != w.ref {
			t.Errorf("item %d = %+v, want %s %s", i, p.Items[i], w.text, w.ref)
		}
	}
}

func TestBrowseNestedSectionsAndPaging(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Browse.ashx?id=g59": genreJSON})
	p, err := c.Browse(context.Background(), "Browse.ashx?id=g59")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("items = %+v", p.Items)
	}
	sec := p.Items[0]
	if sec.Kind != KindSection || len(sec.Children) != 1 {
		t.Fatalf("section = %+v", sec)
	}
	st := sec.Children[0]
	if st.Kind != KindStation || st.GuideID != "s87618" || st.Bitrate != 128 || st.Reliability != 98 {
		t.Errorf("station = %+v", st)
	}
	if st.Image != "https://cdn-radiotime-logos.tunein.com/s87618q.png" {
		t.Errorf("image not upgraded to https: %q", st.Image)
	}
	more := p.Items[1]
	if more.Kind != KindLink || more.Ref != "Browse.ashx?filter=s&id=g59&offset=26" {
		t.Errorf("paging link = %+v", more)
	}
}

func TestSearchNormalisesStationsShowsAndText(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Search.ashx": searchJSON})
	p, err := c.Search(context.Background(), "  apple  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 3 {
		t.Fatalf("items = %+v", p.Items)
	}
	show, st, txt := p.Items[0], p.Items[1], p.Items[2]
	if show.Kind != KindShow || show.GuideID != "p1909239" || show.Ref != "Tune.ashx?c=pbrowse&id=p1909239" {
		t.Errorf("show = %+v", show)
	}
	if st.Kind != KindStation || st.GuideID != "s120798" || st.Formats != "mp3" || st.GenreID != "g2755" {
		t.Errorf("station = %+v", st)
	}
	if txt.Kind != KindText {
		t.Errorf("text = %+v", txt)
	}
	if empty, err := c.Search(context.Background(), "   "); err != nil || len(empty.Items) != 0 {
		t.Errorf("blank query = %+v, %v", empty, err)
	}
}

func TestShowEpisodes(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Tune.ashx?c=pbrowse&id=p1909239": showJSON})
	p, err := c.Browse(context.Background(), "Tune.ashx?c=pbrowse&id=p1909239")
	if err != nil {
		t.Fatal(err)
	}
	ep := p.Items[0].Children[0]
	if ep.Kind != KindEpisode || ep.GuideID != "t569445012" || ep.DurationSec != 412 {
		t.Errorf("episode = %+v", ep)
	}
}

func TestBrowseCachesMetadata(t *testing.T) {
	c, hits := testServer(t, map[string]string{"Browse.ashx": rootJSON})
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, err := c.Root(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if *hits != 1 {
		t.Errorf("hits = %d, want 1 (cached)", *hits)
	}
	now = now.Add(cacheTTL + time.Second)
	if _, err := c.Root(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *hits != 2 {
		t.Errorf("hits = %d, want 2 after expiry", *hits)
	}
}

func TestDescribeStation(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Describe.ashx?id=s345726": describeJSON})
	info, err := c.Describe(context.Background(), "S345726")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "s345726" || info.Kind != KindStation || info.Name != "Apple Music Club" ||
		info.Genre != "Dance & Electronic" || info.Location != "US" || info.Image == "" {
		t.Errorf("info = %+v", info)
	}
}

func TestResolveStreamHLSWithToken(t *testing.T) {
	c, hits := testServer(t, map[string]string{"Tune.ashx?id=s345726": tuneHLSJSON})
	for i := 0; i < 2; i++ {
		st, err := c.ResolveStream(context.Background(), "s345726")
		if err != nil {
			t.Fatal(err)
		}
		if !st.HLS || st.MediaType != "hls" || st.Bitrate != 64 || st.Finite || st.ID != "s345726" {
			t.Errorf("stream = %+v", st)
		}
		if !HasToken(st.URL) {
			t.Errorf("token not detected in resolved URL")
		}
		if strings.Contains(Redact(st.URL), "FAKE_TOKEN_123") {
			t.Errorf("Redact leaked the token: %s", Redact(st.URL))
		}
	}
	if *hits != 2 {
		t.Errorf("hits = %d: resolutions must never be cached", *hits)
	}
}

func TestResolveStreamPlaceholderIsNoCompatibleStream(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Tune.ashx?id=s999999": tunePlaceholderJSON})
	_, err := c.ResolveStream(context.Background(), "s999999")
	if !errors.Is(err, ErrNoCompatibleStream) {
		t.Fatalf("err = %v, want ErrNoCompatibleStream", err)
	}
}

func TestResolveStreamPrefersReliabilityThenFormat(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Tune.ashx?id=s1": tuneMixedJSON})
	st, err := c.ResolveStream(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://mp3.example/live" || st.HLS {
		t.Errorf("picked %+v, want the MP3 at equal reliability", st)
	}
}

func TestResolveStreamEpisodeIsFinite(t *testing.T) {
	c, _ := testServer(t, map[string]string{"Tune.ashx?id=t569445012": tuneEpisodeJSON})
	st, err := c.ResolveStream(context.Background(), "t569445012")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Finite || st.MediaType != "mp3" {
		t.Errorf("episode stream = %+v", st)
	}
}

func TestResolveStreamRejectsNonPlayableIDs(t *testing.T) {
	c, hits := testServer(t, map[string]string{})
	for _, id := range []string{"p1909239", "g59", "", "s12x", "https://x"} {
		if _, err := c.ResolveStream(context.Background(), id); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("%q: err = %v, want ErrInvalidRef", id, err)
		}
	}
	if *hits != 0 {
		t.Errorf("invalid ids reached the network (%d hits)", *hits)
	}
}

func TestErrorsAreClassified(t *testing.T) {
	c, _ := testServer(t, map[string]string{
		"Browse.ashx?c=down":    "HTTP500",
		"Browse.ashx?c=garbage": "<html>not json</html>",
		"Browse.ashx?c=fault":   `{"head":{"status":"400","fault":"Invalid id"},"body":[]}`,
		"Browse.ashx?c=empty":   `{"head":{"status":"200"},"body":[]}`,
		"Tune.ashx?id=s5":       `{"head":{"status":"200"},"body":[]}`,
		"Tune.ashx?id=s6":       `{"head":{"status":"200"},"body":{"oops":1}}`,
	})
	ctx := context.Background()
	if _, err := c.Browse(ctx, "Browse.ashx?c=down"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("HTTP 500: %v", err)
	}
	if _, err := c.Browse(ctx, "Browse.ashx?c=garbage"); !errors.Is(err, ErrBadResponse) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := c.Browse(ctx, "Browse.ashx?c=fault"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fault: %v", err)
	}
	if p, err := c.Browse(ctx, "Browse.ashx?c=empty"); err != nil || len(p.Items) != 0 {
		t.Errorf("empty: %+v %v", p, err)
	}
	if _, err := c.ResolveStream(ctx, "s5"); !errors.Is(err, ErrNoCompatibleStream) {
		t.Errorf("empty tune body: %v", err)
	}
	if _, err := c.ResolveStream(ctx, "s6"); !errors.Is(err, ErrBadResponse) {
		t.Errorf("object tune body: %v", err)
	}
}

func TestTimeoutIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := New()
	c.base = srv.URL
	c.HTTP.Timeout = 50 * time.Millisecond
	if _, err := c.ResolveStream(context.Background(), "s345726"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
