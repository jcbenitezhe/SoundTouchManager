package tunein

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	guideIDRe = regexp.MustCompile(`^[a-z][0-9]{1,12}$`)
	pathIDRe  = regexp.MustCompile(`(?i)(?:^|[-/])([spt][0-9]{1,12})/?$`)
	tokenRe   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	digitsRe  = regexp.MustCompile(`^[0-9]{1,12}$`)
)

const maxShareHops = 5

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func idKind(id string) byte {
	if !guideIDRe.MatchString(id) {
		return 0
	}
	return id[0]
}

// IsPlayableID reports whether id is a station (s) or episode (t) id.
func IsPlayableID(id string) bool {
	k := idKind(normalizeID(id))
	return k == 's' || k == 't'
}

// Ref returns the canonical STM storage form of a playable id
// ("tunein:s345726"), or "" if id is not playable.
func Ref(id string) string {
	id = normalizeID(id)
	if !IsPlayableID(id) {
		return ""
	}
	return Scheme + id
}

// RefID returns the id inside a "tunein:<id>" reference.
func RefID(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if len(ref) <= len(Scheme) || !strings.EqualFold(ref[:len(Scheme)], Scheme) {
		return "", false
	}
	id := normalizeID(ref[len(Scheme):])
	return id, IsPlayableID(id)
}

// IsRef reports whether s is a playable "tunein:<id>" reference.
func IsRef(s string) bool {
	_, ok := RefID(s)
	return ok
}

// ParseID extracts a station, show or episode id from a bare id or from the
// first tunein.com link found in free text (as produced by "Share" in the
// TuneIn app). Short tun.in links need the network; see ResolveShareLink.
func ParseID(input string) (string, error) {
	in := normalizeID(input)
	if k := idKind(in); k == 's' || k == 'p' || k == 't' {
		return in, nil
	}
	u := firstTuneInURL(input)
	if u == nil || !isTuneInPageHost(u.Hostname()) {
		return "", ErrInvalidRef
	}
	return idFromPageURL(u)
}

func idFromPageURL(u *url.URL) (string, error) {
	for key, vals := range u.Query() {
		var kind byte
		switch strings.ToLower(key) {
		case "topicid":
			kind = 't'
		case "stationid":
			kind = 's'
		case "programid":
			kind = 'p'
		default:
			continue
		}
		if len(vals) != 1 {
			continue
		}
		v := normalizeID(vals[0])
		if digitsRe.MatchString(v) {
			v = string(kind) + v
		}
		if idKind(v) == kind {
			return v, nil
		}
	}
	// A topic id wins over the show id in the path, so check the query first.
	if m := pathIDRe.FindStringSubmatch(u.Path); m != nil {
		return normalizeID(m[1]), nil
	}
	return "", ErrInvalidRef
}

// firstTuneInURL returns the first TuneIn page or short link in text.
func firstTuneInURL(text string) *url.URL {
	for _, f := range strings.Fields(text) {
		f = strings.Trim(f, `"'<>()[],;`)
		low := strings.ToLower(f)
		if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
			if !strings.HasPrefix(low, "tunein.com/") && !strings.HasPrefix(low, "www.tunein.com/") &&
				!strings.HasPrefix(low, "tun.in/") {
				continue
			}
			f = "https://" + f
		}
		u, err := url.Parse(f)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if h := u.Hostname(); isTuneInPageHost(h) || isShortHost(h) {
			return u
		}
	}
	return nil
}

func isTuneInPageHost(h string) bool {
	h = strings.ToLower(h)
	return h == "tunein.com" || strings.HasSuffix(h, ".tunein.com")
}

func isShortHost(h string) bool {
	h = strings.ToLower(h)
	return h == "tun.in" || h == "www.tun.in"
}

// ResolveShareLink returns the id behind a shared TuneIn link or id. Short
// tun.in links are followed one redirect at a time, and only while they
// stay on tun.in or tunein.com.
func (c *Client) ResolveShareLink(ctx context.Context, input string) (string, error) {
	u := firstTuneInURL(input)
	if u == nil || !isShortHost(u.Hostname()) {
		return ParseID(input)
	}
	hc := &http.Client{
		Transport: c.HTTP.Transport,
		Timeout:   c.HTTP.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cur := u
	for range maxShareHops {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur.String(), nil)
		if err != nil {
			return "", ErrInvalidRef
		}
		req.Header.Set("User-Agent", c.UA)
		resp, err := hc.Do(req)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
			return "", ErrInvalidRef
		}
		next, err := cur.Parse(loc)
		if err != nil || (next.Scheme != "http" && next.Scheme != "https") {
			return "", ErrInvalidRef
		}
		h := next.Hostname()
		if !isShortHost(h) && !isTuneInPageHost(h) {
			return "", ErrInvalidRef
		}
		if isTuneInPageHost(h) {
			if id, err := idFromPageURL(next); err == nil {
				return id, nil
			}
		}
		cur = next
	}
	return "", errors.Join(ErrInvalidRef, errors.New("too many redirects"))
}

type browseRef struct {
	endpoint string
	params   url.Values
}

var refParams = map[string]bool{"c": true, "id": true, "filter": true, "offset": true, "pivot": true, "sid": true}

var ignoredParams = map[string]bool{"render": true, "locale": true, "formats": true, "partnerid": true, "serial": true}

// parseRef validates a relative browse reference ("Browse.ashx?c=music").
// It is the guard that keeps the backend from becoming an open proxy.
func parseRef(ref string) (browseRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return browseRef{endpoint: "Browse.ashx", params: url.Values{}}, nil
	}
	path, rawQuery, _ := strings.Cut(strings.TrimPrefix(ref, "/"), "?")
	var endpoint string
	switch strings.ToLower(path) {
	case "browse.ashx":
		endpoint = "Browse.ashx"
	case "tune.ashx":
		endpoint = "Tune.ashx"
	default:
		return browseRef{}, ErrInvalidRef
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return browseRef{}, ErrInvalidRef
	}
	params := url.Values{}
	for k, vals := range q {
		lk := strings.ToLower(k)
		if ignoredParams[lk] {
			continue
		}
		if !refParams[lk] || len(vals) != 1 {
			return browseRef{}, ErrInvalidRef
		}
		v := vals[0]
		if lk == "id" || lk == "sid" {
			v = normalizeID(v)
			if !guideIDRe.MatchString(v) {
				return browseRef{}, ErrInvalidRef
			}
		} else if !tokenRe.MatchString(v) {
			return browseRef{}, ErrInvalidRef
		}
		params.Set(lk, v)
	}
	if endpoint == "Tune.ashx" && (params.Get("c") != "pbrowse" || params.Get("id") == "") {
		return browseRef{}, ErrInvalidRef
	}
	return browseRef{endpoint: endpoint, params: params}, nil
}

// refFromURL turns an absolute directory URL from a TuneIn response into a
// relative reference, or "" if it points anywhere else.
func refFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Hostname(), Host) {
		return ""
	}
	r, err := parseRef(strings.TrimPrefix(u.Path, "/") + "?" + u.RawQuery)
	if err != nil {
		return ""
	}
	if len(r.params) == 0 {
		return r.endpoint
	}
	return r.endpoint + "?" + r.params.Encode()
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
