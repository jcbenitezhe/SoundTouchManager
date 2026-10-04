package main

import (
	"encoding/json"
	"fmt"
	"html"
	"sync"
)

// fakeDeviceID is a placeholder in the shape of a SoundTouch device ID (the
// speaker's MAC without separators). Never put a real one here.
const fakeDeviceID = "AABBCCDDEEFF"

// Preset is one hardware key as the firmware stores it.
type Preset struct {
	Slot     int    `json:"slot"`
	Source   string `json:"source"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Account  string `json:"account"`
}

// Playback is what the speaker is doing right now.
type Playback struct {
	Source   string `json:"source"` // STANDBY, INVALID_SOURCE, UPNP
	Location string `json:"location"`
	Name     string `json:"name"`
	State    string `json:"state"` // PLAY_STATE, BUFFERING_STATE, PAUSE_STATE, STOP_STATE
	Slot     int    `json:"slot"`  // preset that started it, 0 when none
}

// Snapshot is the state the control page renders.
type Snapshot struct {
	Name     string     `json:"name"`
	Model    string     `json:"model"`
	Presets  [6]*Preset `json:"presets"`
	Playback Playback   `json:"playback"`
	Volume   int        `json:"volume"`
	Log      []string   `json:"log"`
}

// Device is the fake firmware's whole state. Every change is pushed to the
// gabbo bus (as the firmware would) and to the control page.
type Device struct {
	mu       sync.Mutex
	name     string
	model    string
	account  string
	presets  [6]*Preset
	play     Playback
	volume   int
	pending  string // SetAVTransportURI target waiting for Play
	pendName string
	log      []string

	bus *Bus // gabbo frames to the agent
	ui  *Bus // JSON snapshots to the control page
}

func newDevice(name, model string) *Device {
	return &Device{
		name:   name,
		model:  model,
		volume: 30,
		play:   Playback{Source: "INVALID_SOURCE", State: "STOP_STATE"},
		bus:    newBus(),
		ui:     newBus(),
	}
}

const maxLog = 40

// note appends to the activity log shown on the control page. Caller holds mu.
func (d *Device) note(format string, args ...any) {
	d.log = append(d.log, fmt.Sprintf(format, args...))
	if len(d.log) > maxLog {
		d.log = d.log[len(d.log)-maxLog:]
	}
}

func (d *Device) snapshotLocked() Snapshot {
	s := Snapshot{Name: d.name, Model: d.model, Playback: d.play, Volume: d.volume}
	for i, p := range d.presets {
		if p != nil {
			cp := *p
			s.Presets[i] = &cp
		}
	}
	s.Log = append([]string(nil), d.log...)
	return s
}

// Snapshot returns a copy of the state for the control page.
func (d *Device) Snapshot() Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

// changed pushes the new state to the control page. Caller holds mu.
func (d *Device) changedLocked() {
	b, err := json.Marshal(d.snapshotLocked())
	if err == nil {
		d.ui.Publish(b)
	}
}

// StorePreset is TAP `ws AddPreset`.
func (d *Device) StorePreset(p Preset) error {
	if p.Slot < 1 || p.Slot > 6 {
		return fmt.Errorf("invalid preset id %d", p.Slot)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := p
	d.presets[p.Slot-1] = &cp
	d.note("preset %d stored: %s", p.Slot, p.Name)
	d.bus.Publish([]byte(d.presetsUpdatedLocked()))
	d.changedLocked()
	return nil
}

// RemovePreset is TAP `ws RemovePreset`.
func (d *Device) RemovePreset(slot int) error {
	if slot < 1 || slot > 6 {
		return fmt.Errorf("invalid preset id %d", slot)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.presets[slot-1] = nil
	d.note("preset %d removed", slot)
	d.bus.Publish([]byte(d.presetsUpdatedLocked()))
	d.changedLocked()
	return nil
}

// PressPreset is a hardware key press (the control page, TAP
// `sys presetkey N p`, or REST /key). Like the firmware it announces the
// selection on the bus; the agent answers by pushing the stream over UPnP.
// It reports false for an empty key, which the firmware ignores.
func (d *Device) PressPreset(slot int) bool {
	if slot < 1 || slot > 6 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.presets[slot-1]
	if p == nil {
		d.note("key %d pressed: empty, ignored", slot)
		d.changedLocked()
		return false
	}
	d.play = Playback{Source: p.Source, Location: p.Location, Name: p.Name, State: "BUFFERING_STATE", Slot: slot}
	d.note("key %d pressed: %s", slot, p.Name)
	d.bus.Publish([]byte(fmt.Sprintf(`<updates deviceID="%s"><nowSelectionUpdated>%s</nowSelectionUpdated></updates>`,
		fakeDeviceID, presetXML(p))))
	d.bus.Publish([]byte(d.nowPlayingUpdatedLocked()))
	d.changedLocked()
	return true
}

// SetURI is UPnP SetAVTransportURI: remembered until Play.
func (d *Device) SetURI(uri, title string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending, d.pendName = uri, title
	if uri == "" {
		d.note("UPnP: transport cleared")
	} else {
		d.note("UPnP: told to play %s", uri)
	}
}

// Play is UPnP Play.
func (d *Device) Play() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending != "" {
		name := d.pendName
		slot := 0
		if d.play.Location == d.pending {
			slot = d.play.Slot
			if name == "" {
				name = d.play.Name
			}
		}
		for _, p := range d.presets {
			if p != nil && p.Location == d.pending {
				slot = p.Slot
				if name == "" {
					name = p.Name
				}
			}
		}
		d.play = Playback{Source: "UPNP", Location: d.pending, Name: name, Slot: slot}
	}
	if d.play.Location == "" {
		return
	}
	d.play.State = "PLAY_STATE"
	d.note("UPnP: playing %s", d.play.Location)
	d.bus.Publish([]byte(d.nowPlayingUpdatedLocked()))
	d.changedLocked()
}

// Pause and Stop are UPnP Pause / Stop.
func (d *Device) Pause() { d.setState("PAUSE_STATE", "UPnP: paused") }
func (d *Device) Stop()  { d.setState("STOP_STATE", "UPnP: stopped") }

func (d *Device) setState(state, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.play.Source == "STANDBY" {
		return
	}
	d.play.State = state
	d.note("%s", msg)
	d.bus.Publish([]byte(d.nowPlayingUpdatedLocked()))
	d.changedLocked()
}

// TogglePower is TAP `sys power`: a toggle, exactly like the firmware.
func (d *Device) TogglePower() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.play.Source == "STANDBY" {
		d.play = Playback{Source: "INVALID_SOURCE", State: "STOP_STATE"}
		d.note("power: on")
	} else {
		d.play = Playback{Source: "STANDBY", State: "STOP_STATE"}
		d.note("power: standby")
	}
	d.bus.Publish([]byte(d.nowPlayingUpdatedLocked()))
	d.changedLocked()
}

// Standby is REST /standby: unlike the POWER key it never wakes the box.
func (d *Device) Standby() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.play.Source == "STANDBY" {
		return
	}
	d.play = Playback{Source: "STANDBY", State: "STOP_STATE"}
	d.note("power: standby")
	d.bus.Publish([]byte(d.nowPlayingUpdatedLocked()))
	d.changedLocked()
}

// SetVolume clamps to the firmware's 0..100.
func (d *Device) SetVolume(v int) {
	v = max(0, min(100, v))
	d.mu.Lock()
	defer d.mu.Unlock()
	d.volume = v
	d.bus.Publish([]byte(fmt.Sprintf(`<updates deviceID="%s"><volumeUpdated><volume><targetvolume>%d</targetvolume><actualvolume>%d</actualvolume><muteenabled>false</muteenabled></volume></volumeUpdated></updates>`,
		fakeDeviceID, v, v)))
	d.changedLocked()
}

// SetAccount is POST /setMargeAccount (autopair).
func (d *Device) SetAccount(uuid string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.account = uuid
	d.note("paired with account %s", uuid)
	d.changedLocked()
}

// --- firmware XML ---

func esc(s string) string { return html.EscapeString(s) }

func presetXML(p *Preset) string {
	return fmt.Sprintf(`<preset id="%d" createdOn="0" updatedOn="0"><ContentItem source="%s" type="%s" location="%s" sourceAccount="%s" isPresetable="true"><itemName>%s</itemName></ContentItem></preset>`,
		p.Slot, esc(p.Source), esc(p.Type), esc(p.Location), esc(p.Account), esc(p.Name))
}

func (d *Device) presetsLocked() string {
	out := "<presets>"
	for _, p := range d.presets {
		if p != nil {
			out += presetXML(p)
		}
	}
	return out + "</presets>"
}

// PresetsXML is GET /presets.
func (d *Device) PresetsXML() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return `<?xml version="1.0" encoding="UTF-8" ?>` + d.presetsLocked()
}

func (d *Device) presetsUpdatedLocked() string {
	return fmt.Sprintf(`<updates deviceID="%s"><presetsUpdated>%s</presetsUpdated></updates>`, fakeDeviceID, d.presetsLocked())
}

func (d *Device) nowPlayingLocked() string {
	p := d.play
	if p.Source == "STANDBY" || p.Source == "INVALID_SOURCE" {
		return fmt.Sprintf(`<nowPlaying deviceID="%s" source="%s"><ContentItem source="%s" isPresetable="false" /></nowPlaying>`,
			fakeDeviceID, p.Source, p.Source)
	}
	return fmt.Sprintf(`<nowPlaying deviceID="%s" source="%s" sourceAccount="UPnPUserName"><ContentItem source="%s" location="%s" sourceAccount="UPnPUserName" isPresetable="true"><itemName>%s</itemName></ContentItem><track>%s</track><stationName>%s</stationName><playStatus>%s</playStatus></nowPlaying>`,
		fakeDeviceID, esc(p.Source), esc(p.Source), esc(p.Location), esc(p.Name), esc(p.Name), esc(p.Name), p.State)
}

// NowPlayingXML is GET /now_playing.
func (d *Device) NowPlayingXML() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return `<?xml version="1.0" encoding="UTF-8" ?>` + d.nowPlayingLocked()
}

func (d *Device) nowPlayingUpdatedLocked() string {
	return fmt.Sprintf(`<updates deviceID="%s"><nowPlayingUpdated>%s</nowPlayingUpdated></updates>`, fakeDeviceID, d.nowPlayingLocked())
}

// InfoXML is GET /info. The software version is the firmware STM targets.
func (d *Device) InfoXML(ip string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?><info deviceID="%s"><name>%s</name><type>%s</type><margeAccountUUID>%s</margeAccountUUID><components><component><componentCategory>SCM</componentCategory><softwareVersion>27.0.6.46330.5043500 fakebox</softwareVersion><serialNumber>FAKE0000000000000000000</serialNumber></component></components><margeURL>https://streaming.bose.com</margeURL><networkInfo type="SCM"><macAddress>%s</macAddress><ipAddress>%s</ipAddress></networkInfo><moduleType>sm2</moduleType><variant>rhino</variant><variantMode>normal</variantMode><countryCode>US</countryCode><regionCode>US</regionCode></info>`,
		fakeDeviceID, esc(d.name), esc(d.model), esc(d.account), fakeDeviceID, esc(ip))
}

// VolumeXML is GET /volume.
func (d *Device) VolumeXML() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?><volume deviceID="%s"><targetvolume>%d</targetvolume><actualvolume>%d</actualvolume><muteenabled>false</muteenabled></volume>`,
		fakeDeviceID, d.volume, d.volume)
}

// TransportState is UPnP GetTransportInfo's CurrentTransportState.
func (d *Device) TransportState() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.play.State {
	case "PLAY_STATE":
		return "PLAYING"
	case "PAUSE_STATE":
		return "PAUSED_PLAYBACK"
	case "BUFFERING_STATE":
		return "TRANSITIONING"
	}
	return "STOPPED"
}

// Playing reports the URL being played, empty when not playing.
func (d *Device) Playing() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.play.State == "PLAY_STATE" {
		return d.play.Location
	}
	return ""
}
