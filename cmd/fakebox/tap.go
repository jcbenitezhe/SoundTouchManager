package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// serveTAP is the firmware's text console on :17000: one command per
// connection, answered and left open until the client hangs up (the agent
// reads until it has been quiet for 700 ms).
func serveTAP(ln net.Listener, d *Device, logger *slog.Logger) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil && line == "" {
				return
			}
			cmd := strings.TrimSpace(line)
			reply := runTAP(d, cmd)
			logger.Info("tap", "cmd", cmd, "reply", reply)
			_, _ = fmt.Fprintf(conn, "%s\r\n", reply)
		}()
	}
}

// runTAP executes one console command and returns the reply line.
func runTAP(d *Device, cmd string) string {
	args := splitTAP(cmd)
	if len(args) < 2 {
		return "Command not found"
	}
	switch strings.ToLower(args[0]) + " " + args[1] {
	case "ws AddPreset":
		// ws AddPreset <SOURCE> <TYPE> <LOCATION> <LABEL> <SOURCEACCOUNT> <PRESETID>
		if len(args) != 8 {
			return "usage: ws AddPreset <SOURCE> <TYPE> <LOCATION> <LABEL> <SOURCEACCOUNT> <PRESETID>"
		}
		slot, err := strconv.Atoi(args[7])
		if err != nil {
			return "AddPreset - failed due to invalid preset id"
		}
		if err := d.StorePreset(Preset{Slot: slot, Source: args[2], Type: args[3], Location: args[4], Name: args[5], Account: args[6]}); err != nil {
			return "AddPreset - failed: " + err.Error()
		}
		return "OK"
	case "ws RemovePreset":
		if len(args) != 3 {
			return "usage: ws RemovePreset <PRESETID>"
		}
		slot, err := strconv.Atoi(args[2])
		if err != nil || d.RemovePreset(slot) != nil {
			return "RemovePreset - failed due to invalid preset id"
		}
		return "OK"
	case "sys presetkey":
		if len(args) < 3 {
			return "usage: sys presetkey <1-6> <p|h>"
		}
		slot, err := strconv.Atoi(args[2])
		if err != nil || slot < 1 || slot > 6 {
			return "invalid preset key"
		}
		d.PressPreset(slot)
		return "OK"
	case "sys power":
		d.TogglePower()
		return "OK"
	}
	return "Command not found"
}

// splitTAP splits on spaces, keeping a double-quoted label as one argument.
func splitTAP(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote, has = !inQuote, true
		case r == ' ' && !inQuote:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}
