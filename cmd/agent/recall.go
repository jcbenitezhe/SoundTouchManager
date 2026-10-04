// URL predicates shared by the preset recall and verify paths.

package main

import (
	"regexp"
	"strings"
	"time"

	"github.com/jcbenitezhe/SoundTouchManager/internal/boxurl"
	"github.com/jcbenitezhe/SoundTouchManager/internal/webui"
	"github.com/jcbenitezhe/SoundTouchManager/tunein"
)

// isSTMStreamURL reports whether u is one of STM's own stream URLs (the radio
// stream proxy or the Spotify Ogg passthrough), as opposed to a stale Bose
// ContentItem location that a re-sync has not yet replaced. Deliberately loose
// (substring): used only to PREFER the store URL over a box-provided location.
func isSTMStreamURL(u string) bool {
	return strings.Contains(u, "/stream/") || strings.Contains(u, "/spotify/")
}

// ownBoxPresetLocRe matches exactly the locations STM itself writes into the
// box's preset slots (boxurl.Preset / boxurl.StreamSlot / boxurl.SpotifySlot).
// The reconcile prune keys DELETION off this, so it must never match a foreign
// station URL that merely contains "/stream/".
var ownBoxPresetLocRe = regexp.MustCompile(`^http://127\.0\.0\.1:\d+/(?:stream/[1-6]|spotify/stream(?:-[1-6])?\.ogg)$`)

// ownNativePresetLocPrefix is the second shape STM writes: a native
// LOCAL_INTERNET_RADIO station whose descriptor is served by this agent's own
// orion adapter. It is relative on purpose (the firmware resolves it against
// the BMX service baseUrl). The prune must recognise it too, or a native slot
// the store no longer backs would survive as a dead hardware key forever.
const ownNativePresetLocPrefix = "/core02/svc-bmx-adapter-orion/prod/orion/station?data="

// isOwnBoxPresetLocation reports whether loc is a box-preset location STM
// itself wrote (strict match), the only shape the prune may remove.
//
// Three shapes: the absolute UPnP proxy forms, the orion-prefixed native
// form, and the RELATIVE "/station?data=" form the speaker reports STM's
// native slots in (the form OrionStationLocation writes). The third was
// missing: on a box whose six keys were all native, the prune and
// the store recovery matched nothing, so the dead keys of a removed install
// were never pruned and never recovered. It is matched by decoding, strictly:
// only a payload whose stream is this agent's own per-key proxy counts, a
// foreign descriptor with an external streamUrl does not.
func isOwnBoxPresetLocation(loc string) bool {
	if ownBoxPresetLocRe.MatchString(loc) || strings.HasPrefix(loc, ownNativePresetLocPrefix) {
		return true
	}
	_, ok := webui.StationLocationOwnSlot(loc)
	return ok
}

// isPlayableURL reports whether u is an absolute HTTP(S) URL the UPnP renderer can
// actually load. Stale Bose-cloud ContentItems use relative, schemeless locations
// (e.g. "/v1/playback/station/...") that the box rejects with UPnP 402.
func isPlayableURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// storePlayURL is what the box should fetch for a stored preset stream. A
// TuneIn reference is not a URL the box can open; the slot's stream proxy
// resolves it at fetch time.
func storePlayURL(slot int, streamURL string) string {
	if tunein.IsRef(streamURL) {
		return boxurl.StreamSlot(slot)
	}
	return streamURL
}

// userStopAbortsVerify is the recall-verify stand-down decision: only a user
// stop that happened strictly AFTER the recall started aborts the verify
// re-push loop (stop-after-recall-start, the same semantics the webui's soft
// recall side settled on), never a rolling window. An older stop must not
// suppress the recall the user just asked for; strict After also biases a
// same-instant tie toward completing the recall, since the recall's own
// transport flip can emit a transient STOP_STATE.
func userStopAbortsVerify(recallStart, lastStop time.Time) bool {
	return !lastStop.IsZero() && lastStop.After(recallStart)
}
