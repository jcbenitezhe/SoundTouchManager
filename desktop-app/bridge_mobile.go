//go:build android || stmandroid

package main

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/jcbenitezhe/SoundTouchManager/netif"
)

// The Android host restarts the whole backend process instead.
const bridgeRelisten = false

// bridgePlatformInit replaces the two lookups an Android app process cannot
// make itself, with values the host app read from the platform APIs:
//
//	STM_DNS          comma-separated DNS servers of the active network
//	STM_NET_IFACES   semicolon-separated "name|index|addr/prefix[,addr/prefix]"
//
// A pure-Go binary finds no /etc/resolv.conf on Android and would ask
// 127.0.0.1:53, and netlink is closed to apps from Android 11 on.
func bridgePlatformInit() {
	servers := splitNonEmpty(os.Getenv("STM_DNS"), ",")
	if len(servers) == 0 {
		servers = []string{"1.1.1.1", "8.8.8.8"}
	}
	net.DefaultResolver.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		var lastErr error
		for _, s := range servers {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(s, "53"))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}

	if ifaces := parseStaticIfaces(os.Getenv("STM_NET_IFACES")); len(ifaces) > 0 {
		netif.SetStatic(ifaces)
	}
}

func parseStaticIfaces(spec string) []netif.Iface {
	var out []netif.Iface
	for _, entry := range splitNonEmpty(spec, ";") {
		parts := strings.Split(entry, "|")
		if len(parts) != 3 {
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil || index <= 0 {
			continue
		}
		it := netif.Iface{Iface: net.Interface{
			Index: index,
			MTU:   1500,
			Name:  parts[0],
			Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast | net.FlagRunning,
		}}
		for _, cidr := range splitNonEmpty(parts[2], ",") {
			ip, ipnet, err := net.ParseCIDR(cidr)
			if err != nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			ipnet.IP = ip
			it.Addrs = append(it.Addrs, ipnet)
		}
		out = append(out, it)
	}
	return out
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
