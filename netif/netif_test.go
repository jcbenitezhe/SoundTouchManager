package netif

import (
	"net"
	"testing"
)

func TestStaticListAnswersEveryLookup(t *testing.T) {
	t.Cleanup(func() { SetStatic(nil) })
	_, lan, _ := net.ParseCIDR("192.0.2.10/24")
	lan.IP = net.ParseIP("192.0.2.10").To4()
	wlan := net.Interface{Index: 7, Name: "wlan0", Flags: net.FlagUp | net.FlagMulticast}
	SetStatic([]Iface{{Iface: wlan, Addrs: []*net.IPNet{lan}}})

	ifs, err := Interfaces()
	if err != nil || len(ifs) != 1 || ifs[0].Name != "wlan0" {
		t.Fatalf("Interfaces() = %v, %v", ifs, err)
	}
	all, err := InterfaceAddrs()
	if err != nil || len(all) != 1 || all[0].String() != "192.0.2.10/24" {
		t.Fatalf("InterfaceAddrs() = %v, %v", all, err)
	}
	own, err := Addrs(wlan)
	if err != nil || len(own) != 1 {
		t.Fatalf("Addrs(wlan0) = %v, %v", own, err)
	}
	other, _ := Addrs(net.Interface{Index: 99})
	if len(other) != 0 {
		t.Fatalf("Addrs of an unknown index = %v, want none", other)
	}
}

func TestEmptyStaticListPassesThrough(t *testing.T) {
	SetStatic(nil)
	want, werr := net.Interfaces()
	got, gerr := Interfaces()
	if (werr == nil) != (gerr == nil) || len(want) != len(got) {
		t.Fatalf("pass-through differs: net=%d,%v netif=%d,%v", len(want), werr, len(got), gerr)
	}
}
