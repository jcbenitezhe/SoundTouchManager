//go:build tuneinlive

// Live checks against the public TuneIn directory. Not part of CI:
//
//	go test -tags tuneinlive -run Live -v ./tunein
package tunein

import (
	"context"
	"testing"
	"time"
)

func TestLiveAppleMusicClub(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New()

	info, err := c.Describe(ctx, "s345726")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	t.Logf("describe: %s / %s / %s", info.Name, info.Genre, info.Location)

	st, err := c.ResolveStream(ctx, "s345726")
	if err != nil {
		t.Fatalf("ResolveStream: %v", err)
	}
	t.Logf("stream: hls=%v type=%s bitrate=%d url=%s", st.HLS, st.MediaType, st.Bitrate, Redact(st.URL))
	if !st.HLS {
		t.Errorf("expected an HLS stream for s345726")
	}

	id, err := c.ResolveShareLink(ctx, "http://tun.in/sfK2i")
	if err != nil || id != "s345726" {
		t.Errorf("short link: %q, %v", id, err)
	}
}

func TestLiveBrowseSearchPodcast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := New()

	root, err := c.Root(ctx)
	if err != nil || len(root.Items) == 0 {
		t.Fatalf("Root: %v", err)
	}
	for _, it := range root.Items {
		t.Logf("root: %-6s %-24s %s", it.Kind, it.Text, it.Ref)
	}

	res, err := c.Search(ctx, "jazz")
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("Search: %v", err)
	}
	t.Logf("search jazz: %d items, first %s %s", len(res.Items), res.Items[0].Kind, res.Items[0].Text)

	show, err := c.Browse(ctx, "Tune.ashx?c=pbrowse&id=p1909239")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var ep string
	for _, sec := range show.Items {
		for _, ch := range sec.Children {
			if ch.Kind == KindEpisode && ep == "" {
				ep = ch.GuideID
				t.Logf("episode: %s (%ds)", ch.Text, ch.DurationSec)
			}
		}
	}
	if ep == "" {
		t.Fatal("no episode in show")
	}
	st, err := c.ResolveStream(ctx, ep)
	if err != nil {
		t.Fatalf("episode stream: %v", err)
	}
	t.Logf("episode stream: finite=%v type=%s", st.Finite, st.MediaType)
}
