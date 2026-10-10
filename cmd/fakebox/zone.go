package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A follower confirms a group by answering GET /getZone with the leader's
// device ID. A real SoundTouch learns that from the leader's firmware. This
// stand-in learns it when the leader's agent polls /getZone while its own
// group document already names us, then pulls the leader's stream so the
// control page plays the same audio.

type zoneMember struct {
	ID   string
	IP   string
	Role string
}

type zoneState struct {
	MasterID string
	MasterIP string
	Name     string
	Members  []zoneMember
}

type agentZoneDoc struct {
	Master     string          `json:"master"`
	Members    []agentZonePeer `json:"members"`
	Remembered []agentZonePeer `json:"remembered"`
}

type agentZonePeer struct {
	DeviceID string `json:"deviceID"`
	IP       string `json:"ip"`
}

// listsUs reports whether a leader's group document names this speaker, by
// device ID or by the address the leader used to reach us.
func listsUs(doc agentZoneDoc, selfID, selfIP string) bool {
	for _, m := range append(append([]agentZonePeer{}, doc.Members...), doc.Remembered...) {
		if selfID != "" && strings.EqualFold(strings.TrimSpace(m.DeviceID), selfID) {
			return true
		}
		if selfIP != "" && m.IP != "" && m.IP == selfIP {
			return true
		}
	}
	return false
}

// followerStreamURL rewrites a leader's own loopback stream into the address
// a follower on the LAN can open. Anything that is already a public URL is
// left untouched.
func followerStreamURL(location, masterIP string) string {
	if masterIP == "" {
		return location
	}
	u, err := url.Parse(location)
	if err != nil || u.Host == "" {
		return location
	}
	host := u.Hostname()
	port := u.Port()
	loopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !loopback && host != masterIP {
		return location
	}
	if port != "" && port != "8888" && port != "17008" {
		return location
	}
	u.Host = net.JoinHostPort(masterIP, "17008")
	return u.String()
}

func (d *Device) ZoneXML() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.zoneXMLLocked()
}

func (d *Device) zoneXMLLocked() string {
	if d.zone.MasterID == "" {
		return `<?xml version="1.0" encoding="UTF-8" ?><zone />`
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?><zone master="`)
	b.WriteString(esc(d.zone.MasterID))
	b.WriteString(`" senderIPAddress="`)
	b.WriteString(esc(d.zone.MasterIP))
	b.WriteString(`" senderIsMaster="`)
	if strings.EqualFold(d.zone.MasterID, d.id) {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString(`">`)
	for _, m := range d.zone.Members {
		b.WriteString(`<member ipaddress="`)
		b.WriteString(esc(m.IP))
		b.WriteString(`"`)
		if m.Role != "" {
			b.WriteString(` role="`)
			b.WriteString(esc(m.Role))
			b.WriteString(`"`)
		}
		b.WriteString(`>`)
		b.WriteString(esc(m.ID))
		b.WriteString(`</member>`)
	}
	b.WriteString(`</zone>`)
	return b.String()
}

type postedZone struct {
	XMLName  xml.Name `xml:"zone"`
	Master   string   `xml:"master,attr"`
	SenderIP string   `xml:"senderIPAddress,attr"`
	Members  []struct {
		ID   string `xml:",chardata"`
		IP   string `xml:"ipaddress,attr"`
		Role string `xml:"role,attr"`
	} `xml:"member"`
}

func parsePostedZone(body []byte) (postedZone, error) {
	var z postedZone
	if err := xml.Unmarshal(body, &z); err != nil {
		return postedZone{}, err
	}
	return z, nil
}

// applyPostedZone stores a /setZone, /addZoneSlave or /removeZoneSlave body.
// add appends members; remove drops them and clears the zone when none remain.
func (d *Device) applyPostedZone(body []byte, mode, caller string) error {
	z, err := parsePostedZone(body)
	if err != nil {
		return err
	}
	master := strings.TrimSpace(z.Master)
	masterIP := strings.TrimSpace(z.SenderIP)
	if masterIP == "" {
		masterIP = caller
	}
	var members []zoneMember
	for _, m := range z.Members {
		id := strings.TrimSpace(m.ID)
		if id == "" || strings.EqualFold(id, master) {
			continue
		}
		members = append(members, zoneMember{ID: id, IP: strings.TrimSpace(m.IP), Role: strings.TrimSpace(m.Role)})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch mode {
	case "add":
		if d.zone.MasterID == "" {
			d.zone.MasterID = master
			d.zone.MasterIP = masterIP
		}
		for _, m := range members {
			if !zoneHas(d.zone.Members, m.ID) {
				d.zone.Members = append(d.zone.Members, m)
			}
		}
	case "remove":
		for _, m := range members {
			d.zone.Members = dropZoneMember(d.zone.Members, m.ID)
		}
		if len(d.zone.Members) == 0 {
			d.clearZoneLocked()
			return nil
		}
	default:
		if master == "" {
			d.clearZoneLocked()
			return nil
		}
		d.zone.MasterID = master
		if masterIP != "" {
			d.zone.MasterIP = masterIP
		}
		d.zone.Members = members
	}
	d.noteZoneLocked()
	return nil
}

func zoneHas(ms []zoneMember, id string) bool {
	for _, m := range ms {
		if strings.EqualFold(m.ID, id) {
			return true
		}
	}
	return false
}

func dropZoneMember(ms []zoneMember, id string) []zoneMember {
	out := ms[:0]
	for _, m := range ms {
		if !strings.EqualFold(m.ID, id) {
			out = append(out, m)
		}
	}
	return out
}

func (d *Device) clearZoneLocked() {
	d.zone = zoneState{}
	d.zoneGen++
	d.note("left the group")
	d.bus.Publish([]byte(fmt.Sprintf(`<updates deviceID="%s"><zoneUpdated><zone /></zoneUpdated></updates>`, d.id)))
	d.changedLocked()
}

func (d *Device) noteZoneLocked() {
	d.zoneGen++
	gen := d.zoneGen
	masterIP := d.zone.MasterIP
	masterID := d.zone.MasterID
	name := d.zone.Name
	follow := masterID != "" && !strings.EqualFold(masterID, d.id)
	label := name
	if label == "" {
		label = "the other speaker"
	}
	if follow {
		d.note("joined the group led by %s", label)
	} else {
		d.note("leading a group")
	}
	d.bus.Publish([]byte(d.zoneFrameLocked()))
	d.changedLocked()
	if follow {
		go d.followLeader(gen, masterID, masterIP, name)
	}
}

func (d *Device) zoneFrameLocked() string {
	body := strings.TrimPrefix(d.zoneXMLLocked(), `<?xml version="1.0" encoding="UTF-8" ?>`)
	return fmt.Sprintf(`<updates deviceID="%s"><zoneUpdated>%s</zoneUpdated></updates>`, d.id, body)
}

// becomeFollower records a leader found by polling and starts pulling its audio.
func (d *Device) becomeFollower(masterID, masterIP, name, selfIP string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.EqualFold(d.zone.MasterID, masterID) && d.zone.MasterIP == masterIP {
		return
	}
	d.zone.MasterID = masterID
	d.zone.MasterIP = masterIP
	d.zone.Name = name
	if selfIP != "" && !zoneHas(d.zone.Members, d.id) {
		d.zone.Members = []zoneMember{{ID: d.id, IP: selfIP}}
	}
	d.noteZoneLocked()
}

// observeZonePoll counts how often one remote speaker asks for our zone.
// The leader's agent polls several times while forming; a single stray read
// does not join anything.
func (d *Device) observeZonePoll(caller, selfIP string) {
	if caller == "" || selfIP == "" {
		return
	}
	d.mu.Lock()
	if d.zone.MasterIP == caller && d.zone.MasterID != "" {
		d.mu.Unlock()
		return
	}
	now := time.Now()
	st := d.zonePolls[caller]
	if st.first.IsZero() || now.Sub(st.first) > 3*time.Second {
		st = zonePoll{first: now}
	}
	st.count++
	trip := st.count >= 2 && !st.looking
	if trip {
		st.looking = true
	}
	if d.zonePolls == nil {
		d.zonePolls = map[string]zonePoll{}
	}
	d.zonePolls[caller] = st
	d.mu.Unlock()
	if trip {
		go d.joinFromCaller(caller, selfIP)
	}
}

type zonePoll struct {
	count   int
	first   time.Time
	looking bool
}

func (d *Device) finishZoneLook(caller string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.zonePolls[caller]
	st.looking = false
	st.count = 0
	d.zonePolls[caller] = st
}

func (d *Device) joinFromCaller(caller, selfIP string) {
	defer d.finishZoneLook(caller)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	id, name := fetchCallerInfo(ctx, caller)
	if id == "" || strings.EqualFold(id, d.id) {
		return
	}
	doc, ok := fetchCallerGroup(ctx, caller)
	if !ok || !listsUs(doc, d.id, selfIP) {
		return
	}
	master := strings.TrimSpace(doc.Master)
	if master == "" {
		master = id
	}
	d.becomeFollower(master, caller, name, selfIP)
}

func (d *Device) followLeader(gen int, masterID, masterIP, name string) {
	d.pullLeader(masterIP, name)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	misses := 0
	for {
		d.mu.Lock()
		alive := d.zoneGen == gen && strings.EqualFold(d.zone.MasterID, masterID)
		d.mu.Unlock()
		if !alive {
			return
		}
		<-t.C
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		doc, ok := fetchCallerGroup(ctx, masterIP)
		src, loc, title, playing := fetchCallerNowPlaying(ctx, masterIP)
		cancel()
		d.mu.Lock()
		alive = d.zoneGen == gen && strings.EqualFold(d.zone.MasterID, masterID)
		selfIP := ""
		for _, m := range d.zone.Members {
			if strings.EqualFold(m.ID, d.id) {
				selfIP = m.IP
			}
		}
		d.mu.Unlock()
		if !alive {
			return
		}
		if ok {
			if listsUs(doc, d.id, selfIP) {
				misses = 0
			} else {
				misses++
				if misses >= 2 {
					d.mu.Lock()
					if d.zoneGen == gen {
						d.clearZoneLocked()
					}
					d.mu.Unlock()
					d.Stop()
					return
				}
			}
		}
		if playing {
			d.playLeader(followerStreamURL(loc, masterIP), titleOr(title, src))
		}
	}
}

func titleOr(title, src string) string {
	if title != "" {
		return title
	}
	return src
}

func (d *Device) pullLeader(masterIP, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, loc, title, playing := fetchCallerNowPlaying(ctx, masterIP)
	if !playing {
		return
	}
	if title == "" {
		title = name
	}
	d.playLeader(followerStreamURL(loc, masterIP), title)
}

func (d *Device) playLeader(uri, title string) {
	if uri == "" {
		return
	}
	d.mu.Lock()
	same := d.play.Location == uri && (d.play.State == "PLAY_STATE" || d.play.State == "BUFFERING_STATE")
	d.mu.Unlock()
	if same {
		return
	}
	d.SetURI(uri, title)
	d.Play()
}

var zoneClient = &http.Client{Timeout: 1200 * time.Millisecond}

var fetchCallerInfo = func(ctx context.Context, ip string) (id, name string) {
	body, ok := zoneGET(ctx, "http://"+net.JoinHostPort(ip, "8090")+"/info")
	if !ok {
		return "", ""
	}
	return attr(body, "deviceID"), xmlText(body, "name")
}

var fetchCallerGroup = func(ctx context.Context, ip string) (agentZoneDoc, bool) {
	for _, port := range []string{"17008", "8888"} {
		body, ok := zoneGET(ctx, "http://"+net.JoinHostPort(ip, port)+"/api/box/zone")
		if !ok {
			continue
		}
		var doc agentZoneDoc
		if json.Unmarshal(body, &doc) != nil {
			continue
		}
		return doc, true
	}
	return agentZoneDoc{}, false
}

var fetchCallerNowPlaying = func(ctx context.Context, ip string) (source, location, title string, playing bool) {
	body, ok := zoneGET(ctx, "http://"+net.JoinHostPort(ip, "8090")+"/now_playing")
	if !ok {
		return "", "", "", false
	}
	source = attr(body, "source")
	location = attr(body, "location")
	title = xmlText(body, "itemName")
	status := xmlText(body, "playStatus")
	playing = location != "" && source != "STANDBY" && source != "INVALID_SOURCE" && status != "STOP_STATE" && status != "PAUSE_STATE"
	return source, location, title, playing
}

func zoneGET(ctx context.Context, rawURL string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false
	}
	resp, err := zoneClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, false
	}
	return b, true
}

func attr(body []byte, name string) string {
	s := string(body)
	key := name + `="`
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return html.UnescapeString(rest[:j])
}

func xmlText(body []byte, tag string) string {
	s := string(body)
	open := "<" + tag + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, "</"+tag+">")
	if j < 0 {
		return ""
	}
	return html.UnescapeString(rest[:j])
}

func (d *Device) acceptZone(w http.ResponseWriter, r *http.Request, mode string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err := d.applyPostedZone(body, mode, callerIP(r)); err != nil {
		http.Error(w, "bad zone", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = io.WriteString(w, `<status>/`+mode+`Zone</status>`)
}

// callerIP is the remote side of a firmware request, or "" for this machine.
func callerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() {
		return ""
	}
	return ip.String()
}
