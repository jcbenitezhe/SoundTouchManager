package main

import (
	"context"
	"testing"
	"time"
)

// Field case of 2026-09-06: STM was removed from a SoundTouch 10 over SSH, the
// box rebooted into its stock firmware, and the Listen to music card kept the
// "v0.9.74" badge for as long as the app ran. The stock :8090 answered every
// periodic refresh, and a presence-only sighting keeps the cached STM record.
// After an in-app uninstall the cached record must BE the stock speaker.
func TestMarkHostStockRewritesRecordAsStock(t *testing.T) {
	a := testApp()
	box := knownBoxFixture("192.0.2.60", "DEVU")
	box.Version = "0.9.74"
	box.Build = "2026-09-06-1200"
	box.PortVerified = true
	box.BoxHealth = "ok"
	a.discCache = map[string]discEntry{box.Host: {box: box, seen: time.Now()}}
	a.stmKnown = map[string]discEntry{box.DeviceID: {box: box, seen: time.Now()}}
	a.otaPinned = map[string]time.Time{box.Host: time.Now()}

	a.markHostStock(box.Host)

	e, ok := a.discCache[box.Host]
	if !ok {
		t.Fatalf("the box must stay listed after the uninstall, cache=%v", a.discCache)
	}
	got := e.box
	if got.Kind != "stock" || got.Version != "" || got.Build != "" || got.Port != 8090 || got.PortVerified {
		t.Errorf("record must be a plain stock speaker, got %+v", got)
	}
	if got.STMNotRunning {
		t.Errorf("an in-app uninstall yields an ordinary stock box, not an agent-gone one: %+v", got)
	}
	if got.FriendlyName != box.FriendlyName || got.Model != box.Model || got.DeviceID != box.DeviceID ||
		got.SerialNumber != box.SerialNumber || got.Host != box.Host {
		t.Errorf("identity fields must survive, got %+v", got)
	}
	if got.BoxHealth != "" {
		t.Errorf("agent-only state must be cleared, got BoxHealth=%q", got.BoxHealth)
	}
	if _, still := a.stmKnown[box.DeviceID]; still {
		t.Errorf("STM identity memory must be dropped")
	}
	if _, still := a.otaPinned[box.Host]; still {
		t.Errorf("a stale OTA pin must not keep forcing STM on a removed box")
	}
	if _, pinned := a.stockPinned[box.Host]; !pinned {
		t.Errorf("host must be pinned stock")
	}

	// The periodic refresh sees only the stock :8090 answer: the record must
	// stay stock, with no version, instead of resurrecting the STM record.
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return BoxInfo{}, false }
	a.portOpenFn = func(host string, port int, timeoutMs int) bool { return port == 8090 }
	out, err := a.RefreshKnownBoxes()
	if err != nil {
		t.Fatalf("RefreshKnownBoxes: %v", err)
	}
	if len(out) != 1 || out[0].Kind != "stock" || out[0].Version != "" || out[0].STMSilent {
		t.Fatalf("refresh after uninstall must serve a stock record, got %+v", out)
	}

	// A full discovery that still hears the agent's old announcement (the box's
	// responder can serve it after the agent died; PortVerified false because
	// no probe reached an agent) must not bring the version badge back.
	stale := knownBoxFixture(box.Host, box.DeviceID)
	stale.Version = "0.9.74"
	stale.PortVerified = false
	seen := map[string]BoxInfo{box.Host: stale}
	a.mergeDiscoveryCacheWith(seen, map[string]bool{box.Host: true})
	if got := seen[box.Host]; got.Kind != "stock" || got.Version != "" {
		t.Errorf("stale announcement must not relabel the removed box, got %+v", got)
	}
	if got := a.discCache[box.Host].box; got.Kind != "stock" || got.Version != "" {
		t.Errorf("cache must stay stock, got %+v", got)
	}
	if _, memo := a.stmKnown[box.DeviceID]; memo {
		t.Errorf("a stale announcement must not re-record the STM identity memory")
	}

	// STM reinstalled: an agent that answers a live probe lifts the pin and the
	// box is STM again.
	back := knownBoxFixture(box.Host, box.DeviceID)
	back.Version = "0.9.75"
	back.PortVerified = true
	seen = map[string]BoxInfo{box.Host: back}
	a.mergeDiscoveryCacheWith(seen, nil)
	if got := seen[box.Host]; got.Kind != "str" || got.Version != "0.9.75" {
		t.Errorf("a verified agent sighting must restore the STM record, got %+v", got)
	}
	if _, pinned := a.stockPinned[box.Host]; pinned {
		t.Errorf("a verified agent sighting must lift the stock pin")
	}
}

// markHostStock on a host the cache does not hold must still pin it, and must
// not invent a record.
func TestMarkHostStockUnknownHostOnlyPins(t *testing.T) {
	a := testApp()
	a.markHostStock("192.0.2.61")
	if len(a.discCache) != 0 {
		t.Errorf("no record must be invented, cache=%v", a.discCache)
	}
	if _, pinned := a.stockPinned["192.0.2.61"]; !pinned {
		t.Errorf("host must be pinned stock")
	}
}

// A cached STM box whose agent stops answering while its stock :8090 keeps
// answering is degraded to "STM not running" after stmGoneMisses consecutive
// presence-only refreshes spanning stmGoneMinSpan, instead of serving the old
// agent version forever. Until then each served copy carries STMSilent so the
// Settings pane can already tell "answers its Bose firmware" from "dead".
func TestRefreshKnownBoxesDegradesSilentAgent(t *testing.T) {
	a := testApp()
	box := knownBoxFixture("192.0.2.62", "DEVS")
	box.Version = "0.9.74"
	a.discCache = map[string]discEntry{box.Host: {box: box, seen: time.Now()}}
	a.stmKnown = map[string]discEntry{box.DeviceID: {box: box, seen: time.Now()}}
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return BoxInfo{}, false }
	a.portOpenFn = func(host string, port int, timeoutMs int) bool { return port == 8090 }

	refresh := func() BoxInfo {
		t.Helper()
		out, err := a.RefreshKnownBoxes()
		if err != nil || len(out) != 1 {
			t.Fatalf("RefreshKnownBoxes = %v, %v", out, err)
		}
		return out[0]
	}

	// First two silent refreshes: still STM (a reboot looks the same), served
	// with the transient STMSilent marker, streak counting.
	for i := 1; i <= stmGoneMisses-1; i++ {
		got := refresh()
		if got.Kind != "str" || got.Version != "0.9.74" || !got.STMSilent {
			t.Fatalf("refresh %d: box must still count as STM with STMSilent, got %+v", i, got)
		}
		if e := a.discCache[box.Host]; e.stmMisses != i || e.stmMissSince.IsZero() {
			t.Fatalf("refresh %d: streak must count, entry=%+v", i, e)
		}
		if a.discCache[box.Host].box.STMSilent {
			t.Fatalf("STMSilent must never be cached")
		}
	}

	// Third refresh, but only seconds into the streak: the count alone must
	// not degrade (three quick Refresh presses during a slow reboot).
	if got := refresh(); got.Kind != "str" {
		t.Fatalf("a short streak must not degrade, got %+v", got)
	}

	// Same count, streak old enough: degrade.
	e := a.discCache[box.Host]
	e.stmMissSince = time.Now().Add(-stmGoneMinSpan - time.Second)
	a.discCache[box.Host] = e
	got := refresh()
	if got.Kind != "stock" || !got.STMNotRunning || got.Version != "" || got.Build != "" || got.Port != 8090 {
		t.Fatalf("silent agent must degrade to a stock box flagged STMNotRunning, got %+v", got)
	}
	if got.FriendlyName != box.FriendlyName || got.Model != box.Model || got.DeviceID != box.DeviceID {
		t.Errorf("identity must survive the degrade, got %+v", got)
	}
	if _, memo := a.stmKnown[box.DeviceID]; memo {
		t.Errorf("the STM identity memory must go with the degrade, or the next stock sighting relabels the box STM")
	}

	// Later presence-only refreshes keep it that way: no flicker back to STM.
	if got := refresh(); got.Kind != "stock" || !got.STMNotRunning {
		t.Fatalf("degraded record must stay degraded while the agent is silent, got %+v", got)
	}

	// A stale announcement alone (STM label, nothing verified) must not
	// resurrect the version badge either.
	stale := knownBoxFixture(box.Host, box.DeviceID)
	stale.Version = "0.9.74"
	seen := map[string]BoxInfo{box.Host: stale}
	a.mergeDiscoveryCacheWith(seen, map[string]bool{box.Host: true})
	if got := seen[box.Host]; got.Kind != "stock" || !got.STMNotRunning || got.Version != "" {
		t.Errorf("unverified STM label must not resurrect a degraded box, got %+v", got)
	}

	// The agent answers again (repair, reinstall, or it just came back): STM,
	// no marker, streak cleared.
	live := knownBoxFixture(box.Host, box.DeviceID)
	live.Version = "0.9.75"
	live.PortVerified = true
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return live, true }
	got = refresh()
	if got.Kind != "str" || got.STMNotRunning || got.STMSilent || got.Version != "0.9.75" {
		t.Fatalf("a live agent must restore the STM record, got %+v", got)
	}
	if e := a.discCache[box.Host]; e.stmMisses != 0 || !e.stmMissSince.IsZero() {
		t.Errorf("a confirmed sighting must clear the streak, entry=%+v", e)
	}
	if _, memo := a.stmKnown[box.DeviceID]; !memo {
		t.Errorf("a confirmed sighting must re-record the STM identity memory")
	}
}

// A confirmed agent sighting in the middle of a silent streak resets it: the
// degrade needs stmGoneMisses CONSECUTIVE misses.
func TestSilentAgentStreakResetsOnConfirmedSighting(t *testing.T) {
	a := testApp()
	box := knownBoxFixture("192.0.2.63", "DEVR")
	a.discCache = map[string]discEntry{box.Host: {box: box, seen: time.Now(),
		stmMisses: stmGoneMisses - 1, stmMissSince: time.Now().Add(-time.Hour)}}
	live := box
	live.PortVerified = true
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return live, true }
	a.portOpenFn = func(host string, port int, timeoutMs int) bool { return false }
	if _, err := a.RefreshKnownBoxes(); err != nil {
		t.Fatal(err)
	}
	if e := a.discCache[box.Host]; e.stmMisses != 0 || !e.stmMissSince.IsZero() || e.box.Kind != "str" {
		t.Fatalf("confirmed sighting must reset the streak, entry=%+v", e)
	}
	// The next silent refresh starts a fresh streak of one, not the fatal third.
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return BoxInfo{}, false }
	a.portOpenFn = func(host string, port int, timeoutMs int) bool { return port == 8090 }
	out, err := a.RefreshKnownBoxes()
	if err != nil || len(out) != 1 || out[0].Kind != "str" {
		t.Fatalf("first miss after a confirmation must not degrade, got %v %v", out, err)
	}
	if e := a.discCache[box.Host]; e.stmMisses != 1 {
		t.Errorf("streak must restart at one, entry=%+v", e)
	}
}

// A box mid-update is covered by the OTA pin: its agent is down on purpose
// while the stock port answers, and that must never read as "STM not running".
func TestSilentAgentStreakSkipsBoxMidOTA(t *testing.T) {
	a := testApp()
	box := knownBoxFixture("192.0.2.64", "DEVO")
	a.discCache = map[string]discEntry{box.Host: {box: box, seen: time.Now(),
		stmMisses: stmGoneMisses + 2, stmMissSince: time.Now().Add(-time.Hour)}}
	a.notePostOTA(box.Host)
	a.probeSTMFn = func(ctx context.Context, host string) (BoxInfo, bool) { return BoxInfo{}, false }
	a.portOpenFn = func(host string, port int, timeoutMs int) bool { return port == 8090 }
	out, err := a.RefreshKnownBoxes()
	if err != nil || len(out) != 1 {
		t.Fatalf("RefreshKnownBoxes = %v, %v", out, err)
	}
	if out[0].Kind != "str" || out[0].STMNotRunning || !out[0].OTAPending {
		t.Fatalf("box mid-OTA must stay STM, got %+v", out[0])
	}
}

// An install the app runs on a box it removed STM from earlier outranks the
// stock pin: the OTA pin keeps the box STM through its reboot.
func TestOTAPinOutranksStockPin(t *testing.T) {
	a := testApp()
	box := knownBoxFixture("192.0.2.65", "DEVI")
	a.discCache = map[string]discEntry{box.Host: {box: box, seen: time.Now()}}
	a.markHostStock(box.Host)
	a.notePostOTA(box.Host)
	if _, pinned := a.stockPinned[box.Host]; pinned {
		t.Fatalf("notePostOTA must lift the stock pin")
	}
	seen := map[string]BoxInfo{}
	a.mergeDiscoveryCacheWith(seen, nil)
	if got := seen[box.Host]; got.Kind != "str" || got.STMNotRunning {
		t.Fatalf("box being reinstalled must be served as STM, got %+v", got)
	}
}
