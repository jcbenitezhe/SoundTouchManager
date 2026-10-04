// Package tunein is a small client for the public TuneIn (RadioTime) OPML
// directory: browse, search, station metadata and stream resolution.
//
// Stations are identified by their TuneIn guide id (s12345 for stations,
// p12345 for shows, t12345 for podcast episodes). STM persists only that id,
// written as "tunein:<id>". Stream URLs returned by Tune.ashx are temporary
// and may carry access tokens, so they are resolved at play time, never
// cached here, and must be passed through Redact before they reach a log.
package tunein

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Host is the only host this package talks to for directory data.
const Host = "opml.radiotime.com"

// Scheme prefixes a guide id in STM storage ("tunein:s345726").
const Scheme = "tunein:"

const (
	defaultTimeout  = 10 * time.Second
	cacheTTL        = 10 * time.Minute
	maxCacheEntries = 256
	maxBodyBytes    = 4 << 20
	maxQueryRunes   = 200
	formats         = "mp3,aac,hls"
)

var (
	// ErrUnavailable means TuneIn could not be reached or answered with an
	// HTTP error.
	ErrUnavailable = errors.New("tunein: service unavailable")
	// ErrBadResponse means TuneIn answered with something that is not the
	// expected JSON.
	ErrBadResponse = errors.New("tunein: unexpected response")
	// ErrNotFound means TuneIn reported the item as unknown.
	ErrNotFound = errors.New("tunein: not found")
	// ErrNoCompatibleStream means the item has no stream STM can play.
	ErrNoCompatibleStream = errors.New("tunein: no compatible stream")
	// ErrInvalidRef means the id, link or browse reference was rejected
	// before any request was made.
	ErrInvalidRef = errors.New("tunein: invalid reference")
)

// Client queries the TuneIn directory. The zero value is not usable; call
// New. A Client is safe for concurrent use.
type Client struct {
	HTTP   *http.Client
	Locale string
	UA     string

	base string
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	page    *Page
	info    *Info
	expires time.Time
}

// New returns a Client with sane defaults.
func New() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: defaultTimeout},
		UA:   "SoundTouch-Manager/1.0 (+https://jcbenitezhe.github.io/SoundTouchManager)",
		base: "https://" + Host,
		now:  time.Now,
	}
}

// Root returns the top-level directory.
func (c *Client) Root(ctx context.Context) (*Page, error) {
	return c.Browse(ctx, "")
}

// Browse opens a directory reference as returned in Item.Ref. Only
// Browse.ashx and podcast listings (Tune.ashx?c=pbrowse) are accepted.
func (c *Client) Browse(ctx context.Context, ref string) (*Page, error) {
	r, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	return c.page(ctx, r.endpoint, r.params)
}

// Search runs a free-text directory search.
func (c *Client) Search(ctx context.Context, query string) (*Page, error) {
	query = truncate(strings.TrimSpace(query), maxQueryRunes)
	if query == "" {
		return &Page{}, nil
	}
	return c.page(ctx, "Search.ashx", url.Values{"query": {query}})
}

// Describe returns metadata for a station, show or episode id.
func (c *Client) Describe(ctx context.Context, id string) (*Info, error) {
	id = normalizeID(id)
	if !guideIDRe.MatchString(id) {
		return nil, ErrInvalidRef
	}
	params := url.Values{"id": {id}}
	key := c.cacheKey("Describe.ashx", params)
	if e, ok := c.cached(key); ok && e.info != nil {
		return e.info, nil
	}
	env, err := c.getJSON(ctx, "Describe.ashx", params)
	if err != nil {
		return nil, err
	}
	var raw []rawDescribe
	if err := unmarshalBody(env.Body, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	info := raw[0].info(id)
	c.store(key, cacheEntry{info: info})
	return info, nil
}

// ResolveStream asks TuneIn for a playable stream of a station (s) or
// episode (t). The result is never cached: the URL may expire and may carry
// an access token. Callers must not persist Stream.URL.
func (c *Client) ResolveStream(ctx context.Context, id string) (*Stream, error) {
	id = normalizeID(id)
	if !IsPlayableID(id) {
		return nil, ErrInvalidRef
	}
	env, err := c.getJSON(ctx, "Tune.ashx", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	var raw []rawStream
	if err := unmarshalBody(env.Body, &raw); err != nil {
		return nil, err
	}
	st, err := pickStream(raw)
	if err != nil {
		return nil, err
	}
	st.ID = id
	st.Finite = idKind(id) == 't'
	return st, nil
}

func (c *Client) page(ctx context.Context, endpoint string, params url.Values) (*Page, error) {
	key := c.cacheKey(endpoint, params)
	if e, ok := c.cached(key); ok && e.page != nil {
		return e.page, nil
	}
	env, err := c.getJSON(ctx, endpoint, params)
	if err != nil {
		return nil, err
	}
	var raw []rawOutline
	if err := unmarshalBody(env.Body, &raw); err != nil {
		return nil, err
	}
	p := &Page{Title: strings.TrimSpace(env.Head.Title), Items: normalizeOutlines(raw)}
	c.store(key, cacheEntry{page: p})
	return p, nil
}

type envelope struct {
	Head struct {
		Title  string  `json:"title"`
		Status flexInt `json:"status"`
		Fault  string  `json:"fault"`
	} `json:"head"`
	Body json.RawMessage `json:"body"`
}

func (c *Client) getJSON(ctx context.Context, endpoint string, params url.Values) (*envelope, error) {
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("render", "json")
	// TuneIn judges compatibility by the formats the client claims; without
	// them every HLS-only station is listed as "Not Supported".
	q.Set("formats", formats)
	if c.Locale != "" {
		q.Set("locale", c.Locale)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/"+endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRef, err)
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadResponse, err)
	}
	if env.Head.Status != 0 && env.Head.Status != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d %s", ErrNotFound, env.Head.Status, truncate(env.Head.Fault, 120))
	}
	return &env, nil
}

func unmarshalBody(body json.RawMessage, v any) error {
	if len(body) == 0 || string(body) == "null" {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%w: %w", ErrBadResponse, err)
	}
	return nil
}

func (c *Client) cacheKey(endpoint string, params url.Values) string {
	return c.Locale + "|" + endpoint + "?" + params.Encode()
}

func (c *Client) cached(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || !c.now().Before(e.expires) {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *Client) store(key string, e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.cache == nil {
		c.cache = make(map[string]cacheEntry)
	}
	if len(c.cache) >= maxCacheEntries {
		for k, old := range c.cache {
			if !now.Before(old.expires) {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxCacheEntries {
			clear(c.cache)
		}
	}
	e.expires = now.Add(cacheTTL)
	c.cache[key] = e
}
