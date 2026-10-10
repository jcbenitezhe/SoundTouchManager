package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwitchBoxWLAN(t *testing.T) {
	var got struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
		Hidden   bool   `json:"hidden"`
		Force    bool   `json:"force"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/box/wlan" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Origin") != "" {
			t.Errorf("forwarded an Origin header, which the speaker would refuse")
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"switching","ssid":"Home","mechanism":"wpa"}`))
	}))
	defer srv.Close()

	out, err := newTestApp().SwitchBoxWLAN("127.0.0.1", listenPort(t, srv), " Home ", "correct-horse", true, false)
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if !out.OK || out.Mechanism != "wpa" || out.Status != "switching" {
		t.Fatalf("result %+v", out)
	}
	if got.SSID != "Home" || got.Password != "correct-horse" || !got.Hidden || got.Force {
		t.Fatalf("sent %+v", got)
	}
}

func TestSwitchBoxWLANNotVisible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"ssid-not-visible","error":"not visible","visible":["Other"]}`))
	}))
	defer srv.Close()

	out, err := newTestApp().SwitchBoxWLAN("127.0.0.1", listenPort(t, srv), "Home", "correct-horse", false, false)
	if err != nil {
		t.Fatalf("refusal must stay a result, got %v", err)
	}
	if out.OK || out.Code != "ssid-not-visible" || len(out.Visible) != 1 || out.Visible[0] != "Other" {
		t.Fatalf("result %+v", out)
	}
}

func TestSwitchBoxWLANUnreachable(t *testing.T) {
	_, err := newTestApp().SwitchBoxWLAN("127.0.0.1", deadPort(t), "Home", "correct-horse", false, false)
	if err == nil {
		t.Fatal("expected an error when nothing answers")
	}
	if strings.Contains(err.Error(), "correct-horse") {
		t.Fatal("the error includes the password")
	}
}
