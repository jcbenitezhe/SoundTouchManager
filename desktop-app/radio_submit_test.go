package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jcbenitezhe/SoundTouchManager/streamcheck"
)

// submitFixture serves a fake stream and a fake radio-browser mirror whose
// byurl answer is dupJSON for every URL.
type submitFixture struct {
	stream string
	adds   atomic.Int32
}

func newSubmitFixture(t *testing.T, dupJSON string) *submitFixture {
	t.Helper()
	f := &submitFixture{}
	frame := make([]byte, 417)
	frame[0], frame[1], frame[2] = 0xFF, 0xFB, 0x90
	audio := bytes.Repeat(frame, 40)
	streams := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/page") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>player</html>")
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audio)
	}))
	t.Cleanup(streams.Close)
	f.stream = streams.URL

	old := streamChecker
	streamChecker = streamcheck.New()
	streamChecker.AllowPrivate = true
	t.Cleanup(func() { streamChecker = old })

	submittedURLs.Lock()
	submittedURLs.m = map[string]string{}
	submittedURLs.Unlock()

	withTestRadioMirror(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/add":
			f.adds.Add(1)
			_, _ = io.WriteString(w, `{"ok":true,"message":"station was added","uuid":"00000000-0000-0000-0000-0000000000aa"}`)
		case "/json/stations/byurl":
			_, _ = io.WriteString(w, dupJSON)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	})
	return f
}

func (f *submitFixture) draft(path string) RadioSubmitDraft {
	return RadioSubmitDraft{Name: "Amor 95.3", URL: f.stream + path, CountryCode: "mx", Tags: "Romantic, pop,romantic"}
}

func TestRadioSubmitHappyPathAndNoDoubleSubmit(t *testing.T) {
	f := newSubmitFixture(t, `[]`)
	a := &App{}

	pc := a.RadioPrecheckSubmit(f.draft("/live.mp3"))
	if !pc.CanSubmit || !pc.Stream.OK || pc.Stream.Codec != "MP3" {
		t.Fatalf("precheck = %+v", pc)
	}
	if pc.Fields["countrycode"] != "MX" || pc.Fields["tags"] != "romantic,pop" {
		t.Fatalf("fields = %v", pc.Fields)
	}

	res, err := a.RadioSubmitStation(f.draft("/live.mp3"), true)
	if err != nil || res.UUID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := a.RadioSubmitStation(f.draft("/live.mp3"), true); err == nil || !strings.Contains(err.Error(), problemAlreadySent) {
		t.Fatalf("second submit err = %v", err)
	}
	if n := f.adds.Load(); n != 1 {
		t.Fatalf("add called %d times, want 1", n)
	}
}

func TestRadioSubmitRefusals(t *testing.T) {
	cases := []struct {
		name, path, dup, want string
		confirmed             bool
	}{
		{name: "unconfirmed", path: "/live.mp3", dup: `[]`, want: "STM_NOT_CONFIRMED"},
		{name: "duplicate url", path: "/live.mp3", dup: `[{"stationuuid":"x","name":"Amor","lastcheckok":1,"lastchecktime":"2026-09-30 10:00:00"}]`, want: problemDuplicateURL, confirmed: true},
		{name: "web page", path: "/page", dup: `[]`, want: problemStreamFailed, confirmed: true},
		{name: "expiring token", path: "/live.mp3?token=abc", dup: `[]`, want: problemExpiringToken, confirmed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSubmitFixture(t, tc.dup)
			_, err := (&App{}).RadioSubmitStation(f.draft(tc.path), tc.confirmed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if f.adds.Load() != 0 {
				t.Fatal("add was called")
			}
		})
	}
}

func TestRadioPrecheckMissingFields(t *testing.T) {
	newSubmitFixture(t, `[]`)
	pc := (&App{}).RadioPrecheckSubmit(RadioSubmitDraft{CountryCode: "Mexico"})
	for _, want := range []string{problemNameMissing, problemURLMissing, problemBadCountry} {
		if !contains(pc.Problems, want) {
			t.Errorf("problems %v lack %s", pc.Problems, want)
		}
	}
	if pc.CanSubmit {
		t.Error("CanSubmit with missing fields")
	}
	if pc := (&App{}).RadioPrecheckSubmit(RadioSubmitDraft{}); !contains(pc.Problems, problemNoCountry) {
		t.Errorf("problems %v lack %s", pc.Problems, problemNoCountry)
	}
}

func TestPlaceholderName(t *testing.T) {
	for _, n := range []string{"test", "TEST", " test 2 ", "Prueba", "pruebas", "asdf", "xxx", "123", "demo", "test_1"} {
		if !placeholderName.MatchString(n) {
			t.Errorf("%q not caught", n)
		}
	}
	for _, n := range []string{"Amor 95.3", "Radio Test FM", "Testa Radio", "Demo Radio Berlin", "1LIVE", "Bayern 3", "FM 100"} {
		if placeholderName.MatchString(n) {
			t.Errorf("%q wrongly caught", n)
		}
	}
}
