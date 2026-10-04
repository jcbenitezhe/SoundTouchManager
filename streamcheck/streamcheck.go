// Package streamcheck tells whether a URL is a playable internet radio stream
// before it is submitted to radio-browser: it follows playlists (.pls, .m3u)
// and redirects, reads the first bytes of audio, and reports codec, bitrate,
// the ICY station headers, and query parameters that look like expiring
// access tokens.
package streamcheck

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Result is the outcome of one Check. Error is a stable code (see the Err*
// constants) so a UI can translate it; Detail carries the raw cause.
type Result struct {
	OK          bool     `json:"ok"`
	URL         string   `json:"url"`
	ResolvedURL string   `json:"resolvedUrl"`
	Codec       string   `json:"codec"`
	Bitrate     int      `json:"bitrate"`
	ContentType string   `json:"contentType"`
	HLS         bool     `json:"hls"`
	IcyName     string   `json:"icyName"`
	IcyGenre    string   `json:"icyGenre"`
	IcyURL      string   `json:"icyUrl"`
	Error       string   `json:"error"`
	Detail      string   `json:"detail"`
	TokenParams []string `json:"tokenParams"`
}

// Error codes.
const (
	ErrBadURL      = "bad_url"
	ErrPrivateHost = "private_host"
	ErrUnreachable = "unreachable"
	ErrHTTPStatus  = "http_status"
	ErrWebPage     = "web_page"
	ErrEmptyList   = "empty_playlist"
	ErrNoAudio     = "no_audio"
	ErrTooManyHops = "too_many_hops"
)

const (
	userAgent  = "SoundTouch-Manager/1.0 (+https://jcbenitezhe.github.io/SoundTouchManager)"
	maxHops    = 4
	sniffBytes = 16 * 1024
	minAudio   = 4 * 1024
)

// Checker holds the HTTP client; the zero value is not usable, use New.
type Checker struct {
	HTTP *http.Client
	// AllowPrivate lets tests reach httptest servers on loopback.
	AllowPrivate bool
}

// New returns a Checker with a 15 s overall budget per request.
func New() *Checker {
	return &Checker{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Check probes rawURL. It never returns an error: every failure is in Result.
func (c *Checker) Check(ctx context.Context, rawURL string) Result {
	rawURL = strings.TrimSpace(rawURL)
	res := Result{URL: rawURL, TokenParams: ExpiringParams(rawURL)}
	cur := rawURL
	for hop := 0; hop < maxHops; hop++ {
		u, err := url.Parse(cur)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fail(res, ErrBadURL, cur)
		}
		if !c.AllowPrivate && isPrivateHost(ctx, u.Hostname()) {
			return fail(res, ErrPrivateHost, u.Hostname())
		}
		f, err := c.fetch(ctx, cur)
		if err != nil {
			return fail(res, ErrUnreachable, err.Error())
		}
		res.ResolvedURL = f.finalURL
		if f.status < 200 || f.status > 299 {
			return fail(res, ErrHTTPStatus, strconv.Itoa(f.status))
		}
		res.ContentType = f.contentType
		mergeIcy(&res, f.header)

		switch kind := classify(f); kind {
		case kindHLS:
			res.HLS = true
			res.Codec, res.Bitrate = hlsInfo(f.body)
			if res.Codec == "" {
				res.Codec = "HLS"
			}
			if !bytes.Contains(f.body, []byte("#EXTINF")) && !bytes.Contains(f.body, []byte("#EXT-X-STREAM-INF")) {
				return fail(res, ErrEmptyList, "")
			}
			res.OK = true
			return res
		case kindPLS, kindM3U:
			next := firstEntry(kind, f.body, f.finalURL)
			if next == "" {
				return fail(res, ErrEmptyList, "")
			}
			cur = next
			continue
		case kindHTML:
			return fail(res, ErrWebPage, f.contentType)
		default:
			codec := codecOf(f.contentType, f.body)
			if codec == "" || len(f.body) < minAudio {
				return fail(res, ErrNoAudio, fmt.Sprintf("%s, %d bytes", f.contentType, len(f.body)))
			}
			res.Codec = codec
			if res.Bitrate == 0 {
				res.Bitrate = icyBitrate(f.header)
			}
			if res.Bitrate == 0 && codec == "MP3" {
				res.Bitrate = mp3Bitrate(f.body)
			}
			res.OK = true
			return res
		}
	}
	return fail(res, ErrTooManyHops, "")
}

func fail(r Result, code, detail string) Result {
	r.OK = false
	r.Error = code
	r.Detail = detail
	return r
}

type fetched struct {
	status      int
	finalURL    string
	contentType string
	header      http.Header
	body        []byte
}

func (c *Checker) fetch(ctx context.Context, rawURL string) (fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fetched{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Icy-MetaData", "0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if msg := err.Error(); strings.Contains(msg, `malformed HTTP response "ICY`) || strings.Contains(msg, `malformed HTTP version "ICY"`) {
			return c.fetchICY(ctx, rawURL)
		}
		return fetched{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, sniffBytes))
	return fetched{
		status:      resp.StatusCode,
		finalURL:    resp.Request.URL.String(),
		contentType: resp.Header.Get("Content-Type"),
		header:      resp.Header,
		body:        body,
	}, nil
}

// fetchICY reads a SHOUTcast v1 server, which answers "ICY 200 OK" instead of
// an HTTP status line and so is rejected by net/http.
func (c *Checker) fetchICY(ctx context.Context, rawURL string) (fetched, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" {
		return fetched{}, errors.New("ICY server over https is not supported")
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return fetched{}, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	p := u.RequestURI()
	fmt.Fprintf(conn, "GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: %s\r\nIcy-MetaData: 0\r\n\r\n", p, u.Host, userAgent)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return fetched{}, err
	}
	fields := strings.Fields(status)
	if len(fields) < 2 {
		return fetched{}, fmt.Errorf("bad status line %q", status)
	}
	code, _ := strconv.Atoi(fields[1])
	h := http.Header{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return fetched{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	body, _ := io.ReadAll(io.LimitReader(br, sniffBytes))
	return fetched{status: code, finalURL: rawURL, contentType: h.Get("Content-Type"), header: h, body: body}, nil
}

type kind int

const (
	kindAudio kind = iota
	kindHLS
	kindPLS
	kindM3U
	kindHTML
)

func classify(f fetched) kind {
	ct := strings.ToLower(f.contentType)
	ext := strings.ToLower(path.Ext(urlPath(f.finalURL)))
	head := bytes.TrimSpace(f.body)
	if len(head) > 512 {
		head = head[:512]
	}
	switch {
	case bytes.HasPrefix(head, []byte("#EXTM3U")) && bytes.Contains(f.body, []byte("#EXT-X-")):
		return kindHLS
	case strings.Contains(ct, "mpegurl") && ext == ".m3u8":
		return kindHLS
	case bytes.HasPrefix(bytes.ToLower(head), []byte("[playlist]")) || strings.Contains(ct, "scpls") || ext == ".pls":
		return kindPLS
	case bytes.HasPrefix(head, []byte("#EXTM3U")) || strings.Contains(ct, "mpegurl") || ext == ".m3u":
		return kindM3U
	case strings.Contains(ct, "text/html") || bytes.HasPrefix(bytes.ToLower(head), []byte("<!doctype html")) || bytes.HasPrefix(bytes.ToLower(head), []byte("<html")):
		return kindHTML
	}
	return kindAudio
}

func urlPath(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Path
	}
	return raw
}

// firstEntry returns the first stream URL of a .pls or .m3u, resolved against
// the playlist's own URL.
func firstEntry(k kind, body []byte, base string) string {
	bu, _ := url.Parse(base)
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k == kindPLS {
			key, val, ok := strings.Cut(line, "=")
			if !ok || !strings.HasPrefix(strings.ToLower(key), "file") {
				continue
			}
			line = strings.TrimSpace(val)
		} else if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, err := url.Parse(line)
		if err != nil {
			continue
		}
		if bu != nil {
			ref = bu.ResolveReference(ref)
		}
		if ref.Scheme == "http" || ref.Scheme == "https" {
			return ref.String()
		}
	}
	return ""
}

// hlsInfo reads codec and bandwidth from the first variant of a master
// playlist; a media playlist carries neither.
func hlsInfo(body []byte) (codec string, kbps int) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		attrs := line[len("#EXT-X-STREAM-INF:"):]
		if i := strings.Index(attrs, "BANDWIDTH="); i >= 0 {
			v := attrs[i+len("BANDWIDTH="):]
			if j := strings.IndexAny(v, ",\n"); j >= 0 {
				v = v[:j]
			}
			if n, err := strconv.Atoi(v); err == nil {
				kbps = n / 1000
			}
		}
		if i := strings.Index(attrs, `CODECS="`); i >= 0 {
			v := attrs[i+len(`CODECS="`):]
			if j := strings.Index(v, `"`); j >= 0 {
				v = v[:j]
			}
			switch {
			case strings.Contains(v, "mp4a.40.5"), strings.Contains(v, "mp4a.40.29"):
				codec = "AAC+"
			case strings.Contains(v, "mp4a"):
				codec = "AAC"
			case strings.Contains(v, "mp3"), strings.Contains(v, "mp4a.40.34"):
				codec = "MP3"
			}
		}
		return codec, kbps
	}
	return "", 0
}

// codecOf names the audio codec from the Content-Type, falling back to the
// first bytes for servers that send application/octet-stream or nothing.
func codecOf(contentType string, body []byte) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "aacp"):
		return "AAC+"
	case strings.Contains(ct, "aac"), strings.Contains(ct, "mp4a"):
		return "AAC"
	case strings.Contains(ct, "mpeg"), strings.Contains(ct, "mp3"):
		if len(body) > 0 && isADTS(body) {
			return "AAC"
		}
		return "MP3"
	case strings.Contains(ct, "ogg"), strings.Contains(ct, "opus"):
		if bytes.Contains(body, []byte("OpusHead")) {
			return "OPUS"
		}
		return "OGG"
	case strings.Contains(ct, "flac"):
		return "FLAC"
	}
	switch {
	case bytes.HasPrefix(body, []byte("OggS")):
		if bytes.Contains(body, []byte("OpusHead")) {
			return "OPUS"
		}
		return "OGG"
	case bytes.HasPrefix(body, []byte("fLaC")):
		return "FLAC"
	case bytes.HasPrefix(body, []byte("ID3")):
		return "MP3"
	case isADTS(body):
		return "AAC"
	case mp3Bitrate(body) > 0:
		return "MP3"
	}
	return ""
}

// isADTS finds an AAC ADTS frame header (sync word with layer bits 00).
func isADTS(b []byte) bool {
	for i := 0; i+1 < len(b) && i < 4096; i++ {
		if b[i] == 0xFF && b[i+1]&0xF6 == 0xF0 {
			return true
		}
	}
	return false
}

var mp3Rates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mp3RatesV2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}

// mp3Bitrate reads the bitrate of the first MPEG audio layer III frame whose
// successor sits where the frame length says, so random 0xFF bytes in an ID3
// tag do not count.
func mp3Bitrate(b []byte) int {
	sampleRates := [4]int{44100, 48000, 32000, 0}
	for i := 0; i+4 < len(b); i++ {
		if b[i] != 0xFF || b[i+1]&0xE0 != 0xE0 {
			continue
		}
		version := (b[i+1] >> 3) & 0x03 // 3 = MPEG1, 2 = MPEG2, 0 = MPEG2.5
		layer := (b[i+1] >> 1) & 0x03   // 1 = layer III
		if layer != 1 || version == 1 {
			continue
		}
		idx := b[i+2] >> 4
		srIdx := (b[i+2] >> 2) & 0x03
		pad := int((b[i+2] >> 1) & 0x01)
		rate := mp3Rates[idx]
		sr := sampleRates[srIdx]
		mult := 144
		if version != 3 {
			rate = mp3RatesV2[idx]
			sr /= 2
			if version == 0 {
				sr /= 2
			}
			mult = 72
		}
		if rate == 0 || sr == 0 {
			continue
		}
		n := mult*rate*1000/sr + pad
		if j := i + n; j+1 < len(b) && b[j] == 0xFF && b[j+1]&0xE0 == 0xE0 {
			return rate
		}
	}
	return 0
}

func icyBitrate(h http.Header) int {
	v := h.Get("icy-br")
	if v == "" {
		v = h.Get("ice-audio-info")
		if i := strings.Index(v, "bitrate="); i >= 0 {
			v = v[i+len("bitrate="):]
		} else {
			v = ""
		}
	}
	if i := strings.IndexAny(v, ",;"); i >= 0 {
		v = v[:i]
	}
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	if n > 0 && n < 2000 {
		return n
	}
	return 0
}

func mergeIcy(r *Result, h http.Header) {
	if v := strings.TrimSpace(h.Get("icy-name")); v != "" {
		r.IcyName = v
	}
	if v := strings.TrimSpace(h.Get("icy-genre")); v != "" {
		r.IcyGenre = v
	}
	if v := strings.TrimSpace(h.Get("icy-url")); v != "" {
		r.IcyURL = v
	}
}

// isPrivateHost reports a host that resolves to loopback, link-local, or a
// private range: such a URL only plays on the submitter's own network.
func isPrivateHost(ctx context.Context, host string) bool {
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
			return true
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return false
		}
		ips = ips[:0]
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}
