package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFollowerStreamURLRewritesLoopback(t *testing.T) {
	got := followerStreamURL("http://127.0.0.1:8888/stream/3?x=1", "192.0.2.10")
	want := "http://192.0.2.10:17008/stream/3?x=1"
	if got != want {
		t.Fatalf("got %s", got)
	}
	if got := followerStreamURL("http://192.0.2.10:8888/stream/1", "192.0.2.10"); got != "http://192.0.2.10:17008/stream/1" {
		t.Fatalf("master's own agent port: %s", got)
	}
	public := "http://example.com/radio.mp3"
	if got := followerStreamURL(public, "192.0.2.10"); got != public {
		t.Fatalf("public stream must stay put: %s", got)
	}
}

func TestListsUsByIDOrAddress(t *testing.T) {
	doc := agentZoneDoc{
		Remembered: []agentZonePeer{{DeviceID: "aabbccddeeff", IP: "192.0.2.28"}},
	}
	if !listsUs(doc, "AABBCCDDEEFF", "192.0.2.28") {
		t.Fatal("device id in the remembered group must count")
	}
	doc = agentZoneDoc{Members: []agentZonePeer{{IP: "192.0.2.28"}}}
	if !listsUs(doc, "AABBCCDDEEFF", "192.0.2.28") {
		t.Fatal("the address the leader used must count")
	}
	if listsUs(agentZoneDoc{}, "AABBCCDDEEFF", "192.0.2.28") {
		t.Fatal("an empty group document must not count")
	}
}

func TestSetZoneThenGetZone(t *testing.T) {
	d := newDevice("Fake SoundTouch", "SoundTouch 10")
	srv := httptest.NewServer(restHandler(d, quiet))
	defer srv.Close()

	if body := get(t, srv.URL+"/getZone"); !strings.Contains(body, "<zone />") {
		t.Fatalf("standalone zone: %s", body)
	}
	res, err := http.Post(srv.URL+"/setZone", "text/xml", strings.NewReader(
		`<zone master="112233445566" senderIPAddress="192.0.2.10"><member ipaddress="192.0.2.28">AABBCCDDEEFF</member></zone>`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("setZone status %d", res.StatusCode)
	}
	body := get(t, srv.URL+"/getZone")
	for _, want := range []string{`master="112233445566"`, `senderIPAddress="192.0.2.10"`, `senderIsMaster="false"`, `>AABBCCDDEEFF</member>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("getZone lacks %s: %s", want, body)
		}
	}
	res, err = http.Post(srv.URL+"/removeZoneSlave", "text/xml", strings.NewReader(
		`<zone master="112233445566" senderIPAddress="192.0.2.10"><member ipaddress="192.0.2.28">AABBCCDDEEFF</member></zone>`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if body := get(t, srv.URL+"/getZone"); !strings.Contains(body, "<zone />") {
		t.Fatalf("zone after remove: %s", body)
	}
}

func TestJoinWhenTheLeaderNamesUs(t *testing.T) {
	d := newDevice("Fake SoundTouch", "SoundTouch 10")
	prevInfo, prevGroup, prevNP := fetchCallerInfo, fetchCallerGroup, fetchCallerNowPlaying
	t.Cleanup(func() {
		fetchCallerInfo, fetchCallerGroup, fetchCallerNowPlaying = prevInfo, prevGroup, prevNP
	})
	fetchCallerInfo = func(context.Context, string) (string, string) {
		return "112233445566", "SoundTouch 20"
	}
	fetchCallerGroup = func(context.Context, string) (agentZoneDoc, bool) {
		return agentZoneDoc{Remembered: []agentZonePeer{{DeviceID: "AABBCCDDEEFF", IP: "192.0.2.28"}}}, true
	}
	fetchCallerNowPlaying = func(context.Context, string) (string, string, string, bool) {
		return "UPNP", "http://127.0.0.1:8888/stream/2", "Radio", true
	}
	d.observeZonePoll("192.0.2.10", "192.0.2.28")
	if strings.Contains(d.ZoneXML(), `master="112233445566"`) {
		t.Fatal("one poll must not join the group")
	}
	d.observeZonePoll("192.0.2.10", "192.0.2.28")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(d.ZoneXML(), `master="112233445566"`) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(d.ZoneXML(), `master="112233445566"`) {
		t.Fatalf("did not join: %s", d.ZoneXML())
	}
	deadline = time.Now().Add(2 * time.Second)
	var loc string
	for time.Now().Before(deadline) {
		loc = d.Snapshot().Playback.Location
		if loc != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if loc != "http://192.0.2.10:17008/stream/2" {
		t.Fatalf("playback location: %q", loc)
	}
}
