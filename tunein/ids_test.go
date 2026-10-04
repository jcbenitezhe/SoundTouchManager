package tunein

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	ok := map[string]string{
		"s345726":   "s345726",
		" S345726 ": "s345726",
		"p1909239":  "p1909239",
		"https://tunein.com/radio/Apple-Music-Club-s345726/":                                      "s345726",
		"https://tunein.com/radio/Apple-Music-Club-s345726":                                       "s345726",
		"tunein.com/radio/Apple-Music-Club-s345726/":                                              "s345726",
		"http://tunein.com/station/?StationId=345726":                                             "s345726",
		"https://tunein.com/podcasts/Music-Podcasts/The-Mix-New-Music-Club-p1909239/":             "p1909239",
		"https://tunein.com/podcasts/Music/The-Mix-p1909239/?topicId=569445012":                   "t569445012",
		"Listen to Apple Music Club on TuneIn https://tunein.com/radio/Apple-Music-Club-s345726/": "s345726",
	}
	for in, want := range ok {
		got, err := ParseID(in)
		if err != nil || got != want {
			t.Errorf("ParseID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "g59", "hello", "https://example.com/radio/x-s345726/",
		"https://tunein.com/radio/", "https://evil.example/?StationId=1",
		"javascript:alert(1)",
	}
	for _, in := range bad {
		if got, err := ParseID(in); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("ParseID(%q) = %q, %v; want ErrInvalidRef", in, got, err)
		}
	}
}

func TestIsPlayableID(t *testing.T) {
	for id, want := range map[string]bool{"s1": true, "T22": true, "p1": false, "g59": false, "": false, "s": false} {
		if got := IsPlayableID(id); got != want {
			t.Errorf("IsPlayableID(%q) = %v", id, got)
		}
	}
}

func TestRefRoundTrip(t *testing.T) {
	if got := Ref(" S345726 "); got != "tunein:s345726" {
		t.Errorf("Ref = %q", got)
	}
	if Ref("p1909239") != "" || Ref("") != "" {
		t.Error("Ref accepted a non-playable id")
	}
	for in, want := range map[string]bool{
		"tunein:s345726": true, "TuneIn:T5": true, "tunein:p1": false, "tunein:": false,
		"https://tunein.com/radio/x-s1/": false, "s345726": false, "tunein:s1?x=y": false,
	} {
		if IsRef(in) != want {
			t.Errorf("IsRef(%q) = %v", in, !want)
		}
	}
	if id, ok := RefID("tunein:S42"); !ok || id != "s42" {
		t.Errorf("RefID = %q, %v", id, ok)
	}
}

func TestParseRefAllowList(t *testing.T) {
	good := map[string]string{
		"":                                "Browse.ashx?",
		"Browse.ashx?c=music":             "Browse.ashx?c=music",
		"/Browse.ashx?id=g59&filter=s":    "Browse.ashx?filter=s&id=g59",
		"Browse.ashx?c=music&render=xml":  "Browse.ashx?c=music",
		"Tune.ashx?c=pbrowse&id=p1909239": "Tune.ashx?c=pbrowse&id=p1909239",
		"browse.ashx?id=r0&offset=26":     "Browse.ashx?id=r0&offset=26",
	}
	for in, want := range good {
		r, err := parseRef(in)
		if err != nil {
			t.Errorf("parseRef(%q): %v", in, err)
			continue
		}
		if got := r.endpoint + "?" + r.params.Encode(); got != want {
			t.Errorf("parseRef(%q) = %q, want %q", in, got, want)
		}
	}
	bad := []string{
		"Tune.ashx?id=s345726",                // playback is not browsing
		"Search.ashx?query=x",                 // not via a ref
		"Describe.ashx?id=s1",                 // not via a ref
		"Browse.ashx?url=http://evil.example", // unknown parameter
		"Browse.ashx?c=" + url.QueryEscape("a b"),
		"Browse.ashx?id=../../etc",
		"https://evil.example/Browse.ashx?c=music",
		"Browse.ashx?c=music&c=talk",
		"../admin",
	}
	for _, in := range bad {
		if _, err := parseRef(in); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("parseRef(%q) accepted, want ErrInvalidRef", in)
		}
	}
}

func TestBrowseNeverLeavesHost(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"head":{"status":"200"},"body":[]}`))
	}))
	defer srv.Close()
	c := New()
	c.base = srv.URL
	if _, err := c.Browse(context.Background(), "https://evil.example/Browse.ashx?c=music"); err == nil {
		t.Fatal("absolute foreign ref accepted")
	}
	if len(paths) != 0 {
		t.Errorf("request issued for a rejected ref: %v", paths)
	}
}

func TestResolveShareLinkFollowsShortLinkWithinTuneIn(t *testing.T) {
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://linkshortener.tunein.com/sfK2i", http.StatusMovedPermanently)
	}))
	defer short.Close()
	shortener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://tunein.com/station/?StationId=345726", http.StatusMovedPermanently)
	}))
	defer shortener.Close()

	c := New()
	c.HTTP = &http.Client{Transport: rewriteTransport{map[string]string{
		"tun.in":                   strings.TrimPrefix(short.URL, "http://"),
		"linkshortener.tunein.com": strings.TrimPrefix(shortener.URL, "http://"),
	}}}
	id, err := c.ResolveShareLink(context.Background(), "Escucha esto: http://tun.in/sfK2i")
	if err != nil || id != "s345726" {
		t.Fatalf("ResolveShareLink = %q, %v", id, err)
	}
}

func TestResolveShareLinkRefusesToLeaveTuneIn(t *testing.T) {
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/?StationId=1", http.StatusFound)
	}))
	defer short.Close()
	c := New()
	c.HTTP = &http.Client{Transport: rewriteTransport{map[string]string{
		"tun.in": strings.TrimPrefix(short.URL, "http://"),
	}}}
	if _, err := c.ResolveShareLink(context.Background(), "http://tun.in/abc"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("err = %v, want ErrInvalidRef", err)
	}
}

func TestResolveShareLinkParsesLongLinksOffline(t *testing.T) {
	c := New()
	c.HTTP = &http.Client{Transport: failTransport{}}
	id, err := c.ResolveShareLink(context.Background(), "https://tunein.com/radio/Apple-Music-Club-s345726/")
	if err != nil || id != "s345726" {
		t.Fatalf("got %q, %v", id, err)
	}
}

// rewriteTransport sends requests for the mapped hosts to local test servers.
type rewriteTransport struct{ hosts map[string]string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if to, ok := rt.hosts[r.URL.Hostname()]; ok {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = to
		r.Host = to
		return http.DefaultTransport.RoundTrip(r)
	}
	return nil, errors.New("unexpected host " + r.URL.Host)
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network must not be used")
}
