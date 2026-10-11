package main

import "testing"

func TestClockDisplayBodyMexicoStaysPut(t *testing.T) {
	body := clockDisplayBody(true, "America/Mexico_City", 0, "TIME_FORMAT_24HOUR_ID")
	want := `<clockDisplay><clockConfig userEnable="true" timezoneInfo="NOT_SET" userOffsetMinute="-360" timeFormat="TIME_FORMAT_24HOUR_ID" /></clockDisplay>`
	if body != want {
		t.Fatalf("Mexico City body:\n got %s\nwant %s", body, want)
	}
}

func TestClockDisplayBodyNamedZoneKeepsDST(t *testing.T) {
	body := clockDisplayBody(true, "Europe/Berlin", 120, "TIME_FORMAT_24HOUR_ID")
	want := `<clockDisplay><clockConfig userEnable="true" timezoneInfo="Europe/Berlin" userOffsetMinute="0" timeFormat="TIME_FORMAT_24HOUR_ID" /></clockDisplay>`
	if body != want {
		t.Fatalf("Berlin body:\n got %s\nwant %s", body, want)
	}
}

func TestClockDisplayBodyBorderStillFollowsUS(t *testing.T) {
	body := clockDisplayBody(true, "America/Tijuana", -420, "TIME_FORMAT_12HOUR_ID")
	want := `<clockDisplay><clockConfig userEnable="true" timezoneInfo="America/Tijuana" userOffsetMinute="0" timeFormat="TIME_FORMAT_12HOUR_ID" /></clockDisplay>`
	if body != want {
		t.Fatalf("Tijuana body:\n got %s\nwant %s", body, want)
	}
}
