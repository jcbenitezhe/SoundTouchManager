// Package netif lists the host's network interfaces and their addresses.
//
// On every desktop platform it is a thin pass-through to package net. It
// exists for Android: from Android 11 on, apps may not bind a NETLINK_ROUTE
// socket, so net.Interfaces, net.InterfaceAddrs and (*net.Interface).Addrs all
// fail there (golang/go#40569). The Android host app reads the Wi-Fi link from
// the platform APIs instead and hands it over through SetStatic, after which
// every lookup in this package answers from that list.
package netif

import (
	"net"
	"sync"
)

// Iface is one interface with its IPv4/IPv6 prefixes, as supplied to SetStatic.
type Iface struct {
	Iface net.Interface
	Addrs []*net.IPNet
}

var (
	mu     sync.RWMutex
	static []Iface
)

// SetStatic replaces the platform lookups with a fixed interface list. A nil
// or empty list restores the pass-through to package net.
func SetStatic(ifaces []Iface) {
	mu.Lock()
	defer mu.Unlock()
	static = append([]Iface(nil), ifaces...)
}

func staticList() ([]Iface, bool) {
	mu.RLock()
	defer mu.RUnlock()
	return static, len(static) > 0
}

// Interfaces is net.Interfaces, or the static list when one is set.
func Interfaces() ([]net.Interface, error) {
	list, ok := staticList()
	if !ok {
		return net.Interfaces()
	}
	out := make([]net.Interface, len(list))
	for i, s := range list {
		out[i] = s.Iface
	}
	return out, nil
}

// InterfaceAddrs is net.InterfaceAddrs, or every address of the static list.
func InterfaceAddrs() ([]net.Addr, error) {
	list, ok := staticList()
	if !ok {
		return net.InterfaceAddrs()
	}
	var out []net.Addr
	for _, s := range list {
		for _, a := range s.Addrs {
			out = append(out, a)
		}
	}
	return out, nil
}

// Addrs is iface.Addrs(), or the static addresses of the interface with the
// same index.
func Addrs(iface net.Interface) ([]net.Addr, error) {
	list, ok := staticList()
	if !ok {
		return iface.Addrs()
	}
	var out []net.Addr
	for _, s := range list {
		if s.Iface.Index != iface.Index {
			continue
		}
		for _, a := range s.Addrs {
			out = append(out, a)
		}
	}
	return out, nil
}
