// Adding a station to radio-browser through its API (POST /json/add) instead
// of its web form. radio-browser has no accounts, so every guard against a bad
// or duplicate entry lives here: the backend re-runs the whole precheck on
// submit and refuses on any blocking problem, whatever the UI showed.
package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jcbenitezhe/SoundTouchManager/radiobrowser"
	"github.com/jcbenitezhe/SoundTouchManager/streamcheck"
)

var streamChecker = streamcheck.New()

// placeholderName matches names people type to try the form out. The
// directory is public and has no delete, so a "test" entry stays for everyone.
var placeholderName = regexp.MustCompile(`(?i)^\s*(test(ing)?|prueba(s)?|probe|demo|example|ejemplo|beispiel|asdf+|qwerty|foo|bar|x+|a+|[0-9]+)[\s\d._-]*$`)

// RadioStationStatus is a radio-browser entry with its check state
// (radiobrowser.StatusWorking, StatusBroken, StatusUnknown).
type RadioStationStatus struct {
	radiobrowser.Station
	Status string `json:"status"`
}

func withStatus(in []radiobrowser.Station) []RadioStationStatus {
	out := make([]RadioStationStatus, 0, len(in))
	for _, s := range in {
		out = append(out, RadioStationStatus{Station: s, Status: radiobrowser.StatusOf(s)})
	}
	return out
}

// RadioLookupForAdd lists every radio-browser entry whose name contains query,
// broken and unchecked ones included, working ones first. country is an
// ISO 3166-1 alpha-2 code, or empty for every country.
func (a *App) RadioLookupForAdd(query, country string) ([]RadioStationStatus, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []RadioStationStatus{}, nil
	}
	ctx, cancel := a.radioCtx()
	defer cancel()
	st, err := radioClient.ByName(ctx, query, country, 30)
	out := withStatus(radiobrowser.EnrichSiblingLogos(nonNilStations(st)))
	rank := map[string]int{radiobrowser.StatusWorking: 0, radiobrowser.StatusUnknown: 1, radiobrowser.StatusBroken: 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Status] < rank[out[j].Status] })
	return out, err
}

// RadioCheckStream probes a stream URL the way the precheck does.
func (a *App) RadioCheckStream(streamURL string) streamcheck.Result {
	ctx, cancel := a.radioCtx()
	defer cancel()
	return streamChecker.Check(ctx, streamURL)
}

// RadioSubmitDraft is the add-station form.
type RadioSubmitDraft struct {
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

func (d RadioSubmitDraft) station() radiobrowser.NewStation {
	return radiobrowser.NewStation{
		Name: d.Name, URL: d.URL, Homepage: d.Homepage, Favicon: d.Favicon,
		CountryCode: d.CountryCode, ISO31662: d.ISO31662, State: d.State,
		LanguageCodes: d.LanguageCodes, Language: d.Language, Tags: normalizeTags(d.Tags),
	}
}

func normalizeTags(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

// Precheck problem codes. Blocking ones stop a submit; the rest are warnings.
const (
	problemNameMissing   = "name_missing"
	problemURLMissing    = "url_missing"
	problemStreamFailed  = "stream_failed"
	problemExpiringToken = "expiring_token"
	problemDuplicateURL  = "duplicate_url"
	problemAlreadySent   = "already_submitted"
	problemBadCountry    = "bad_countrycode"
	problemPlaceholder   = "name_placeholder"
	warnSimilarName      = "similar_name"
	warnNoHomepage       = "no_homepage"
	warnNoFavicon        = "no_favicon"
	problemNoCountry     = "country_missing"
	warnDirectoryDown    = "directory_unreachable"
)

// RadioSubmitPrecheck is everything the confirmation screen shows: the exact
// fields that would be sent, the stream check, and the duplicates found.
type RadioSubmitPrecheck struct {
	Fields      map[string]string    `json:"fields"`
	Stream      streamcheck.Result   `json:"stream"`
	SameURL     []RadioStationStatus `json:"sameUrl"`
	SimilarName []RadioStationStatus `json:"similarName"`
	Problems    []string             `json:"problems"`
	Warnings    []string             `json:"warnings"`
	CanSubmit   bool                 `json:"canSubmit"`
}

// submittedURLs remembers what this app instance already added, so a double
// tap or a retry after a slow reply cannot create the station twice.
var submittedURLs = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// RadioPrecheckSubmit runs every check a submit runs, without submitting.
func (a *App) RadioPrecheckSubmit(d RadioSubmitDraft) RadioSubmitPrecheck {
	ctx, cancel := context.WithTimeout(a.ctxOrBackground(), 40*time.Second)
	defer cancel()
	return precheck(ctx, d)
}

func precheck(ctx context.Context, d RadioSubmitDraft) RadioSubmitPrecheck {
	st := d.station()
	form := st.Form()
	pc := RadioSubmitPrecheck{
		Fields:      map[string]string{},
		SameURL:     []RadioStationStatus{},
		SimilarName: []RadioStationStatus{},
		Problems:    []string{},
		Warnings:    []string{},
	}
	for k := range form {
		pc.Fields[k] = form.Get(k)
	}
	name, streamURL := form.Get("name"), form.Get("url")
	if name == "" {
		pc.Problems = append(pc.Problems, problemNameMissing)
	} else if placeholderName.MatchString(name) {
		pc.Problems = append(pc.Problems, problemPlaceholder)
	}
	if streamURL == "" {
		pc.Problems = append(pc.Problems, problemURLMissing)
	}
	// The API docs call countrycode optional, but the server refuses an add
	// without one ("countrycode does not have exactly 2 chars").
	if cc := form.Get("countrycode"); cc == "" {
		pc.Problems = append(pc.Problems, problemNoCountry)
	} else if !isCountryCode(cc) {
		pc.Problems = append(pc.Problems, problemBadCountry)
	}

	if streamURL != "" {
		submittedURLs.Lock()
		_, sent := submittedURLs.m[streamURL]
		submittedURLs.Unlock()
		if sent {
			pc.Problems = append(pc.Problems, problemAlreadySent)
		}

		pc.Stream = streamChecker.Check(ctx, streamURL)
		if !pc.Stream.OK {
			pc.Problems = append(pc.Problems, problemStreamFailed)
		}
		if len(pc.Stream.TokenParams) > 0 {
			pc.Problems = append(pc.Problems, problemExpiringToken)
		}

		same, err := radioClient.ByURL(ctx, streamURL)
		if err != nil {
			pc.Warnings = append(pc.Warnings, warnDirectoryDown)
		}
		if pc.Stream.ResolvedURL != "" && pc.Stream.ResolvedURL != streamURL {
			if more, err := radioClient.ByURL(ctx, pc.Stream.ResolvedURL); err == nil {
				same = append(same, more...)
			}
		}
		pc.SameURL = withStatus(dedupeStations(same))
		if len(pc.SameURL) > 0 {
			pc.Problems = append(pc.Problems, problemDuplicateURL)
		}
	}
	if name != "" {
		similar, err := radioClient.ByName(ctx, name, "", 20)
		if err != nil && !contains(pc.Warnings, warnDirectoryDown) {
			pc.Warnings = append(pc.Warnings, warnDirectoryDown)
		}
		pc.SimilarName = withStatus(similar)
		if len(similar) > 0 {
			pc.Warnings = append(pc.Warnings, warnSimilarName)
		}
	}
	if form.Get("homepage") == "" {
		pc.Warnings = append(pc.Warnings, warnNoHomepage)
	}
	if form.Get("favicon") == "" {
		pc.Warnings = append(pc.Warnings, warnNoFavicon)
	}
	pc.CanSubmit = len(pc.Problems) == 0
	return pc
}

func dedupeStations(in []radiobrowser.Station) []radiobrowser.Station {
	seen := map[string]bool{}
	out := make([]radiobrowser.Station, 0, len(in))
	for _, s := range in {
		if !seen[s.StationUUID] {
			seen[s.StationUUID] = true
			out = append(out, s)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func isCountryCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// RadioSubmitResult is the reply to a submit.
type RadioSubmitResult struct {
	UUID     string              `json:"uuid"`
	Message  string              `json:"message"`
	Precheck RadioSubmitPrecheck `json:"precheck"`
}

// RadioSubmitStation adds the station after the user confirmed the exact
// fields. The precheck runs again here, so a stale or skipped confirmation
// screen cannot submit a broken, tokenised, or duplicate entry.
func (a *App) RadioSubmitStation(d RadioSubmitDraft, confirmed bool) (RadioSubmitResult, error) {
	if !confirmed {
		return RadioSubmitResult{}, fmt.Errorf("STM_NOT_CONFIRMED: the submission was not confirmed")
	}
	ctx, cancel := context.WithTimeout(a.ctxOrBackground(), 60*time.Second)
	defer cancel()
	pc := precheck(ctx, d)
	if !pc.CanSubmit {
		return RadioSubmitResult{Precheck: pc}, fmt.Errorf("STM_PRECHECK_FAILED: %s", strings.Join(pc.Problems, ","))
	}
	st := d.station()
	streamURL := st.Form().Get("url")

	submittedURLs.Lock()
	if _, sent := submittedURLs.m[streamURL]; sent {
		submittedURLs.Unlock()
		return RadioSubmitResult{Precheck: pc}, fmt.Errorf("STM_PRECHECK_FAILED: %s", problemAlreadySent)
	}
	submittedURLs.m[streamURL] = ""
	submittedURLs.Unlock()

	res, err := radioClient.Add(ctx, st)
	if err != nil {
		if res.Message != "" {
			// Refused outright: nothing was created, a corrected retry is fine.
			submittedURLs.Lock()
			delete(submittedURLs.m, streamURL)
			submittedURLs.Unlock()
		}
		return RadioSubmitResult{Message: res.Message, Precheck: pc}, err
	}
	submittedURLs.Lock()
	submittedURLs.m[streamURL] = res.UUID
	submittedURLs.Unlock()
	return RadioSubmitResult{UUID: res.UUID, Message: res.Message, Precheck: pc}, nil
}

// RadioStationLookup is a station fetched by UUID; Found is false while the
// new entry has not reached the mirror yet.
type RadioStationLookup struct {
	Found   bool               `json:"found"`
	Station RadioStationStatus `json:"station"`
}

// RadioStationByUUID fetches one station for the post-submit status view.
func (a *App) RadioStationByUUID(uuid string) (RadioStationLookup, error) {
	ctx, cancel := a.radioCtx()
	defer cancel()
	st, ok, err := radioClient.ByUUID(ctx, uuid)
	if err != nil || !ok {
		return RadioStationLookup{}, err
	}
	return RadioStationLookup{Found: true, Station: RadioStationStatus{Station: st, Status: radiobrowser.StatusOf(st)}}, nil
}

func (a *App) ctxOrBackground() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
