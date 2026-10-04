package radiobrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Station check states as the add-station flow shows them.
const (
	StatusWorking = "working" // checked, reachable
	StatusBroken  = "broken"  // checked, not reachable
	StatusUnknown = "unknown" // not checked yet (a fresh entry)
)

// StatusOf classifies a station by radio-browser's last stream check.
func StatusOf(s Station) string {
	switch {
	case neverChecked(s):
		return StatusUnknown
	case s.LastCheckOK == 1:
		return StatusWorking
	default:
		return StatusBroken
	}
}

// NewStation is what POST /json/add accepts. Only Name and URL are required.
// State and Language are deprecated upstream in favour of ISO31662 and
// LanguageCodes but still accepted; they are sent only when the codes are
// missing.
type NewStation struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Homepage      string `json:"homepage"`
	Favicon       string `json:"favicon"`
	CountryCode   string `json:"countrycode"`
	ISO31662      string `json:"iso_3166_2"`
	State         string `json:"state"`
	LanguageCodes string `json:"languagecodes"`
	Language      string `json:"language"`
	Tags          string `json:"tags"`
}

// Form returns the exact fields Add sends, so a caller can show the user what
// will be submitted before it is.
func (n NewStation) Form() url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val = strings.TrimSpace(val); val != "" {
			v.Set(k, val)
		}
	}
	set("name", n.Name)
	set("url", n.URL)
	set("homepage", n.Homepage)
	set("favicon", n.Favicon)
	set("countrycode", strings.ToUpper(n.CountryCode))
	set("iso_3166_2", strings.ToUpper(n.ISO31662))
	if strings.TrimSpace(n.ISO31662) == "" {
		set("state", n.State)
	}
	set("languagecodes", strings.ToLower(n.LanguageCodes))
	if strings.TrimSpace(n.LanguageCodes) == "" {
		set("language", n.Language)
	}
	set("tags", n.Tags)
	return v
}

// AddResult is radio-browser's reply to /json/add.
type AddResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	UUID    string `json:"uuid"`
}

// Add submits a new station. Unlike the read calls it moves to the next mirror
// only when the request never reached the current one (DNS or connect
// failure): a timeout after sending may still have created the station, and a
// blind retry would create it twice.
func (c *Client) Add(ctx context.Context, st NewStation) (AddResult, error) {
	form := st.Form()
	if form.Get("name") == "" || form.Get("url") == "" {
		return AddResult{}, errors.New("add: name and url are required")
	}
	c.mu.Lock()
	mirrors := append([]string(nil), c.mirrors...)
	c.mu.Unlock()

	var lastErr error
	for i, base := range mirrors {
		res, err := c.postAdd(ctx, base, form)
		if err == nil {
			if i > 0 {
				c.mu.Lock()
				c.mirrors[0], c.mirrors[i] = c.mirrors[i], c.mirrors[0]
				c.mu.Unlock()
			}
			if !res.OK {
				return res, fmt.Errorf("radio-browser refused the station: %s", res.Message)
			}
			return res, nil
		}
		lastErr = err
		if !notSent(err) {
			break
		}
	}
	return AddResult{}, fmt.Errorf("add: %w", lastErr)
}

func (c *Client) postAdd(ctx context.Context, base string, form url.Values) (AddResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*perMirrorTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/add", strings.NewReader(form.Encode()))
	if err != nil {
		return AddResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.UA != "" {
		req.Header.Set("User-Agent", c.UA)
	}
	client := c.HTTP
	if u, perr := url.Parse(base); perr == nil && net.ParseIP(u.Hostname()) != nil {
		client = c.ipHTTP
		req.Host = ipMirrorServerName
	}
	resp, err := client.Do(req)
	if err != nil {
		return AddResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var res AddResult
	if jerr := json.Unmarshal(body, &res); jerr != nil {
		if resp.StatusCode != http.StatusOK {
			return AddResult{}, fmt.Errorf("mirror %s: %d", base, resp.StatusCode)
		}
		return AddResult{}, fmt.Errorf("decode %s: %w", base, jerr)
	}
	return res, nil
}

// notSent reports an error raised before the request left this machine.
func notSent(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// ByUUID returns one station, or ok=false when no mirror knows the UUID yet.
func (c *Client) ByUUID(ctx context.Context, uuid string) (Station, bool, error) {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return Station{}, false, errors.New("byuuid: empty uuid")
	}
	var out []Station
	if err := c.fetchJSON(ctx, "/stations/byuuid/"+url.PathEscape(uuid), &out); err != nil {
		return Station{}, false, err
	}
	if len(out) == 0 {
		return Station{}, false, nil
	}
	return out[0], true, nil
}

// ByName returns every station whose name contains name, checked or not and
// working or not, for duplicate checks. A two-letter country narrows it to
// that country; an empty one searches everywhere.
func (c *Client) ByName(ctx context.Context, name, country string, limit int) ([]Station, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("byname: empty name")
	}
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("name", name)
	if cc := strings.TrimSpace(country); len(cc) == 2 {
		q.Set("countrycode", strings.ToUpper(cc))
	}
	q.Set("limit", fmt.Sprint(limit))
	q.Set("order", "votes")
	q.Set("reverse", "true")
	var out []Station
	err := c.fetchJSON(ctx, "/stations/search?"+q.Encode(), &out)
	return out, err
}
