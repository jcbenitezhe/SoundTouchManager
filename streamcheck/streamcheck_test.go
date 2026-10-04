package streamcheck

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// mp3Frames builds n MPEG-1 layer III frames at 128 kbps, 44.1 kHz (417 bytes).
func mp3Frames(n int) []byte {
	frame := make([]byte, 417)
	frame[0], frame[1], frame[2], frame[3] = 0xFF, 0xFB, 0x90, 0x00
	return bytes.Repeat(frame, n)
}

// adtsFrames builds n bare AAC ADTS headers padded to 256 bytes each.
func adtsFrames(n int) []byte {
	frame := make([]byte, 256)
	frame[0], frame[1] = 0xFF, 0xF1
	return bytes.Repeat(frame, n)
}

func testChecker() *Checker {
	c := New()
	c.AllowPrivate = true
	return c
}

func TestCheckStreams(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mp3", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("icy-name", "Amor 95.3")
		_, _ = w.Write(mp3Frames(40))
	})
	mux.HandleFunc("/aac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/aacp")
		w.Header().Set("icy-br", "64")
		_, _ = w.Write(adtsFrames(40))
	})
	mux.HandleFunc("/octet", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(adtsFrames(40))
	})
	mux.HandleFunc("/list.pls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-scpls")
		fmt.Fprint(w, "[playlist]\nNumberOfEntries=1\nFile1=/mp3\nTitle1=Amor\n")
	})
	mux.HandleFunc("/list.m3u", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1,Amor\n/aac\n")
	})
	mux.HandleFunc("/live.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=64000,CODECS=\"mp4a.40.2\"\nchunk.m3u8\n")
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><html><body>player</body></html>")
	})
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3Frames(2))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/aac", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cases := []struct {
		path, codec string
		ok, hls     bool
		bitrate     int
		err         string
	}{
		{path: "/mp3", ok: true, codec: "MP3", bitrate: 128},
		{path: "/aac", ok: true, codec: "AAC+", bitrate: 64},
		{path: "/octet", ok: true, codec: "AAC"},
		{path: "/list.pls", ok: true, codec: "MP3", bitrate: 128},
		{path: "/list.m3u", ok: true, codec: "AAC+", bitrate: 64},
		{path: "/live.m3u8", ok: true, hls: true, codec: "AAC", bitrate: 64},
		{path: "/redirect", ok: true, codec: "AAC+", bitrate: 64},
		{path: "/page", err: ErrWebPage},
		{path: "/short", err: ErrNoAudio},
		{path: "/missing", err: ErrHTTPStatus},
	}
	c := testChecker()
	for _, tc := range cases {
		r := c.Check(context.Background(), srv.URL+tc.path)
		if r.OK != tc.ok || r.Codec != tc.codec || r.HLS != tc.hls || r.Bitrate != tc.bitrate || r.Error != tc.err {
			t.Errorf("%s: got ok=%v codec=%q hls=%v br=%d err=%q (%s), want ok=%v codec=%q hls=%v br=%d err=%q",
				tc.path, r.OK, r.Codec, r.HLS, r.Bitrate, r.Error, r.Detail, tc.ok, tc.codec, tc.hls, tc.bitrate, tc.err)
		}
	}

	if r := c.Check(context.Background(), srv.URL+"/list.pls"); r.ResolvedURL != srv.URL+"/mp3" || r.IcyName != "Amor 95.3" {
		t.Errorf("pls: resolved=%q icy=%q", r.ResolvedURL, r.IcyName)
	}
}

// A SHOUTcast v1 server answers "ICY 200 OK", which net/http rejects.
func TestCheckICYServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for {
					line, err := br.ReadString('\n')
					if err != nil || line == "\r\n" {
						break
					}
				}
				fmt.Fprint(conn, "ICY 200 OK\r\nicy-name:Old Station\r\nicy-br:96\r\ncontent-type:audio/mpeg\r\n\r\n")
				_, _ = conn.Write(mp3Frames(40))
			}(conn)
		}
	}()
	r := testChecker().Check(context.Background(), "http://"+ln.Addr().String()+"/;")
	if !r.OK || r.Codec != "MP3" || r.Bitrate != 96 || r.IcyName != "Old Station" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckRefusesPrivateAndBadURLs(t *testing.T) {
	c := New()
	for _, u := range []string{"http://127.0.0.1:8000/stream", "http://192.168.1.20/stream", "http://localhost/s", "http://radio.local/s"} {
		if r := c.Check(context.Background(), u); r.Error != ErrPrivateHost {
			t.Errorf("%s: err=%q, want %q", u, r.Error, ErrPrivateHost)
		}
	}
	for _, u := range []string{"", "ftp://example.com/s", "not a url", "https://"} {
		if r := c.Check(context.Background(), u); r.Error != ErrBadURL {
			t.Errorf("%q: err=%q, want %q", u, r.Error, ErrBadURL)
		}
	}
}

func TestExpiringParams(t *testing.T) {
	soon := strconv.FormatInt(time.Now().Add(2*time.Hour).Unix(), 10)
	cases := map[string][]string{
		"https://example.com/live.aac":                                          nil,
		"https://example.com/live.aac?dist=web&type=.aac":                       nil,
		"https://example.com/live.aac?token=abc&dist=web":                       {"token"},
		"https://example.com/live.m3u8?hdnts=exp=1~acl=*~hmac=ff":               {"hdnts"},
		"https://example.com/s?X-Amz-Signature=1&X-Amz-Expires=60":              {"X-Amz-Expires", "X-Amz-Signature"},
		"https://example.com/s?e=" + soon:                                       {"e"},
		"https://example.com/s?year=2026":                                       nil,
		"https://example.com/s.m3u8?accessKey=abc":                              {"accessKey"},
		"https://example.com/s.m3u8?k=" + soon + "_2e73b3ed0f8a61b3":            {"k"},
		"https://example.com/s?id=1234567890123":                                nil,
		"https://example.com/s?a=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefgh": {"a"},
	}
	for u, want := range cases {
		got := ExpiringParams(u)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %v, want %v", u, got, want)
		}
	}
}
