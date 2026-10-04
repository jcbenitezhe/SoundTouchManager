package radiobrowser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStatusOf(t *testing.T) {
	cases := []struct {
		s    Station
		want string
	}{
		{Station{LastCheckOK: 1, LastCheckTime: "2026-09-30 10:00:00"}, StatusWorking},
		{Station{LastCheckOK: 0, LastCheckTime: "2026-09-30 10:00:00"}, StatusBroken},
		{Station{LastCheckOK: 0, LastCheckTime: ""}, StatusUnknown},
		{Station{LastCheckOK: 0, LastCheckTime: "0000-00-00 00:00:00"}, StatusUnknown},
	}
	for _, c := range cases {
		if got := StatusOf(c.s); got != c.want {
			t.Errorf("StatusOf(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestNewStationFormPrefersCodes(t *testing.T) {
	f := NewStation{
		Name: " Amor 95.3 ", URL: "https://example.com/amor.aac",
		CountryCode: "mx", ISO31662: "mx-cmx", State: "Ciudad de México",
		LanguageCodes: "SPA", Language: "spanish", Tags: "romantic,pop",
	}.Form()
	want := map[string]string{
		"name": "Amor 95.3", "url": "https://example.com/amor.aac",
		"countrycode": "MX", "iso_3166_2": "MX-CMX", "languagecodes": "spa", "tags": "romantic,pop",
	}
	for k, v := range want {
		if f.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.Get(k), v)
		}
	}
	for _, k := range []string{"state", "language", "homepage", "favicon"} {
		if f.Has(k) {
			t.Errorf("%s should not be sent, got %q", k, f.Get(k))
		}
	}

	g := NewStation{Name: "x", URL: "https://example.com/s", State: "Jalisco", Language: "spanish"}.Form()
	if g.Get("state") != "Jalisco" || g.Get("language") != "spanish" {
		t.Errorf("legacy fields not sent without codes: %v", g)
	}
}

func TestAddPostsFormAndReturnsUUID(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/json/add" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(body))
		_, _ = io.WriteString(w, `{"ok":true,"message":"station was added","uuid":"00000000-0000-0000-0000-000000000001"}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), ipHTTP: srv.Client(), mirrors: []string{srv.URL + "/json"}}

	res, err := c.Add(context.Background(), NewStation{Name: "Amor 95.3", URL: "https://example.com/amor.aac", CountryCode: "MX"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UUID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("uuid = %q", res.UUID)
	}
	if got.Get("name") != "Amor 95.3" || got.Get("countrycode") != "MX" {
		t.Fatalf("form = %v", got)
	}
}

func TestAddReportsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":false,"message":"url is not valid"}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), ipHTTP: srv.Client(), mirrors: []string{srv.URL + "/json"}}
	if _, err := c.Add(context.Background(), NewStation{Name: "x", URL: "https://example.com/s"}); err == nil || !strings.Contains(err.Error(), "url is not valid") {
		t.Fatalf("err = %v", err)
	}
}

// A mirror that received the request must not be followed by a second one: a
// timeout or 5xx there may still have created the station.
func TestAddDoesNotRetryAfterTheRequestWasSent(t *testing.T) {
	var second atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oops", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
		_, _ = io.WriteString(w, `{"ok":true,"uuid":"u"}`)
	}))
	defer good.Close()
	c := &Client{HTTP: http.DefaultClient, ipHTTP: http.DefaultClient, mirrors: []string{bad.URL + "/json", good.URL + "/json"}}
	if _, err := c.Add(context.Background(), NewStation{Name: "x", URL: "https://example.com/s"}); err == nil {
		t.Fatal("want error from the first mirror")
	}
	if second.Load() != 0 {
		t.Fatal("second mirror was tried after the first one received the request")
	}
}

func TestAddMovesOnWhenTheMirrorIsUnreachable(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"uuid":"u"}`)
	}))
	defer good.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	c := &Client{HTTP: http.DefaultClient, ipHTTP: http.DefaultClient, mirrors: []string{deadURL + "/json", good.URL + "/json"}}
	res, err := c.Add(context.Background(), NewStation{Name: "x", URL: "https://example.com/s"})
	if err != nil || res.UUID != "u" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestByUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/known") {
			_, _ = io.WriteString(w, `[{"stationuuid":"known","name":"Amor 95.3","codec":"AAC","bitrate":64,"lastcheckok":1,"lastchecktime":"2026-09-30 10:00:00"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), ipHTTP: srv.Client(), mirrors: []string{srv.URL + "/json"}}

	st, ok, err := c.ByUUID(context.Background(), "known")
	if err != nil || !ok || st.Codec != "AAC" || StatusOf(st) != StatusWorking {
		t.Fatalf("st=%+v ok=%v err=%v", st, ok, err)
	}
	if _, ok, err := c.ByUUID(context.Background(), "fresh"); err != nil || ok {
		t.Fatalf("fresh: ok=%v err=%v", ok, err)
	}
}

func TestByNameCountryFilter(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("countrycode"))
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), ipHTTP: srv.Client(), mirrors: []string{srv.URL + "/json"}}

	for _, cc := range []string{"mx", "", "MEX"} {
		if _, err := c.ByName(context.Background(), "Amor", cc, 5); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(got, ",") != "MX,," {
		t.Fatalf("countrycode sent: %q, want MX then none for empty and invalid", got)
	}
}
