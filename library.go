package main

// The library is the guide's list of movies, cached on the SD card so Rewind
// opens instantly and works offline, then refreshed in the background.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type CastMember struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type Provider struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Video struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (v *Video) URL() string { return "https://youtu.be/" + v.Key }

type Movie struct {
	ID          int          `json:"id"`
	Title       string       `json:"title"`
	Year        int          `json:"year"`
	Released    time.Time    `json:"released"`               // for display: digital release, else theatrical, else premiere
	Digital     time.Time    `json:"digital"`                // first digital release in the region that has happened
	DigitalSoon time.Time    `json:"digital_soon,omitempty"` // scheduled digital release, when none has happened
	Theatrical  time.Time    `json:"theatrical,omitempty"`   // first theatrical release in the region
	Premiere    time.Time    `json:"premiere,omitempty"`     // first release anywhere, of any kind
	HomeAt      time.Time    `json:"home_at,omitempty"`      // when it could first be watched at home (see homeDate)
	Rent        bool         `json:"rent,omitempty"`         // a store rents it (not only sells it)
	Tagline     string       `json:"tagline,omitempty"`
	Overview    string       `json:"overview"`
	Poster      string       `json:"poster"`
	Backdrop    string       `json:"backdrop"`
	Stills      []string     `json:"stills,omitempty"`
	GenreIDs    []int        `json:"genre_ids"`
	Genres      []string     `json:"genres,omitempty"`
	Popularity  float64      `json:"popularity"`
	VoteAvg     float64      `json:"vote_avg"`
	VoteCount   int          `json:"vote_count"`
	Runtime     int          `json:"runtime"`
	Cert        string       `json:"cert"`
	IMDbID      string       `json:"imdb_id"`
	Directors   []string     `json:"directors,omitempty"`
	Writers     []string     `json:"writers,omitempty"`
	Cast        []CastMember `json:"cast,omitempty"`
	Trailer     *Video       `json:"trailer,omitempty"`
	Providers   []Provider   `json:"providers,omitempty"` // subscription, free and ad-supported services
	Stores      []Provider   `json:"stores,omitempty"`    // where it can be rented or bought
	WatchLink   string       `json:"watch_link,omitempty"`
	RT          int          `json:"rt"`   // Tomatometer 0-100, -1 unknown
	Meta        int          `json:"meta"` // Metascore 0-100, -1 unknown
	IMDb        float64      `json:"imdb"` // 0 unknown
	IMDbVotes   string       `json:"imdb_votes,omitempty"`
	Awards      string       `json:"awards,omitempty"`
	DetailAt    time.Time    `json:"detail_at"`
	DetailV     int          `json:"detail_v,omitempty"`
	ScoresAt    time.Time    `json:"scores_at"`
}

func (m *Movie) HasDetails() bool { return !m.DetailAt.IsZero() }

// NewDate is what the NEWEST sort uses: when the movie could first be watched at home.
func (m *Movie) NewDate() time.Time {
	if !m.HomeAt.IsZero() {
		return m.HomeAt
	}
	return m.Released
}

// Streaming: on a subscription (or free) service, rather than only to rent or buy.
func (m *Movie) Streaming() bool { return len(m.Providers) > 0 }

func (m *Movie) StoreNames() []string {
	var s []string
	for _, p := range m.Stores {
		s = append(s, p.Name)
	}
	return s
}

func (m *Movie) ProviderNames() []string {
	var s []string
	for _, p := range m.Providers {
		s = append(s, p.Name)
	}
	return s
}

type Library struct {
	Movies  []*Movie       `json:"movies"`
	Genres  map[int]string `json:"genres"`
	Updated time.Time      `json:"updated"`
	Region  string         `json:"region"`
	Days    int            `json:"days"`
	// HomeSeen: when each film with no digital date on TMDB was first seen available at home,
	// which stands in for its digital release date until TMDB has one.
	HomeSeen map[int]time.Time `json:"home_seen,omitempty"`
	// Skip: candidates found not to be recent home releases (re-releases of older films),
	// with when, so their details aren't fetched again every refresh.
	Skip    map[int]time.Time `json:"skip,omitempty"`
	Version int               `json:"v,omitempty"`
	Source  string            `json:"-"` // "live", "cache", "empty"
}

func (env *Env) libraryPath() string { return filepath.Join(env.DataDir, "library.json") }

func (env *Env) LoadLibrary() *Library {
	lib := &Library{Genres: map[int]string{}, Source: "empty"}
	b, err := os.ReadFile(env.libraryPath())
	if err != nil {
		return lib
	}
	if err := json.Unmarshal(b, lib); err != nil {
		log.Printf("library cache unreadable: %v", err)
		return &Library{Genres: map[int]string{}, Source: "empty"}
	}
	if lib.Genres == nil {
		lib.Genres = map[int]string{}
	}
	lib.Source = "cache"
	lib.fillGenres()
	return lib
}

func (env *Env) SaveLibrary(lib *Library) error {
	b, err := json.Marshal(lib)
	if err != nil {
		return err
	}
	tmp := env.libraryPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, env.libraryPath())
}

// NeedsRefresh: the cache is old, empty, or was made for other settings.
func (env *Env) NeedsRefresh(lib *Library) bool {
	return len(lib.Movies) == 0 || lib.Version < libVersion || lib.Region != env.Config.Region || lib.Days != env.Config.Days ||
		env.Now().Sub(lib.Updated) > time.Duration(env.Config.RefreshHours)*time.Hour
}

func (lib *Library) clone() *Library {
	b, _ := json.Marshal(lib)
	c := &Library{}
	json.Unmarshal(b, c)
	if c.Genres == nil {
		c.Genres = map[int]string{}
	}
	c.Source = lib.Source
	return c
}

func (lib *Library) fillGenres() {
	for _, m := range lib.Movies {
		if len(m.Genres) == len(m.GenreIDs) && len(m.Genres) > 0 {
			continue
		}
		m.Genres = m.Genres[:0]
		for _, id := range m.GenreIDs {
			if n := lib.Genres[id]; n != "" {
				m.Genres = append(m.Genres, n)
			}
		}
	}
}

// Progress of a refresh, for the UI.
type Progress struct {
	Phase string
	Done  int
	Total int
}

// detailVersion goes up when Details reads something new, so cached details are re-read.
const detailVersion = 2

// libVersion goes up when the guide is chosen differently, so the next launch refreshes.
const libVersion = 7

// detailStale: details (cast, services, trailer) are re-read every few days, since services change.
func (env *Env) detailStale(m *Movie) bool {
	return !m.HasDetails() || m.DetailV < detailVersion || env.Now().Sub(m.DetailAt) > 3*24*time.Hour
}

// scoresStale: new releases' scores move fast at first, then settle.
func (env *Env) scoresStale(m *Movie) bool {
	if env.Config.OMDbKey == "" || m.IMDbID == "" {
		return false
	}
	if m.ScoresAt.IsZero() {
		return true
	}
	age := env.Now().Sub(m.ScoresAt)
	if env.Now().Sub(m.Released) < 45*24*time.Hour {
		return age > 24*time.Hour
	}
	return age > 5*24*time.Hour
}

// Refresh rebuilds the library from TMDB and OMDb, reusing cached details where fresh.
// Warnings (e.g. OMDb limit) are returned in warn; err means nothing usable came back.
func (env *Env) Refresh(ctx context.Context, old *Library, prog func(Progress)) (lib *Library, warn string, err error) {
	old = old.clone() // the UI keeps reading the old one while this runs
	prog(Progress{Phase: "SCANNING GENRES"})
	genres := old.Genres
	if len(genres) == 0 || old.Region != env.Config.Region {
		g, err := env.Genres(ctx)
		if err != nil {
			return nil, "", err
		}
		genres = g
	}
	now := env.Now()
	skip := map[int]time.Time{}
	if old.Version == libVersion && old.Region == env.Config.Region {
		for id, t := range old.Skip {
			if now.Sub(t) < 7*24*time.Hour {
				skip[id] = t
			}
		}
	}
	prog(Progress{Phase: "FINDING NEW RELEASES"})
	found, err := env.Discover(ctx, skip)
	if err != nil {
		return nil, "", err
	}
	prev := map[int]*Movie{}
	for _, m := range old.Movies {
		prev[m.ID] = m
	}
	movies := make([]*Movie, 0, len(found))
	for _, f := range found {
		if _, ok := skip[f.ID]; ok {
			continue
		}
		if p := prev[f.ID]; p != nil {
			// keep cached details, take the fresh popularity
			p.Popularity = f.Popularity
			if p.Poster == "" {
				p.Poster = f.Poster
			}
			movies = append(movies, p)
		} else {
			movies = append(movies, f)
		}
	}

	// details, 4 at a time
	var todo []*Movie
	for _, m := range movies {
		if env.detailStale(m) {
			todo = append(todo, m)
		}
	}
	firstErr := env.parallel(ctx, todo, "READING LABELS", prog, env.Details)
	if firstErr != nil && len(todo) > 0 && countDetails(movies) == 0 {
		return nil, "", firstErr
	}

	// keep the films whose first home release is recent, in the order found, up to MaxMovies
	seen := map[int]time.Time{}
	if old.Version == libVersion && old.Region == env.Config.Region {
		for id, t := range old.HomeSeen {
			if now.Sub(t) < time.Duration(env.Config.Days+30)*24*time.Hour {
				seen[id] = t
			}
		}
	}
	seeding := old.Version != libVersion || len(old.Movies) == 0
	kept := make([]*Movie, 0, env.Config.MaxMovies)
	why := map[string]int{}
	for _, m := range movies {
		if len(kept) >= env.Config.MaxMovies {
			why["over max_movies"]++
			continue
		}
		at, reason, final := env.homeDate(m, seen, seeding)
		if reason != "" {
			why[reason]++
			if final {
				skip[m.ID] = now
			}
			continue
		}
		m.HomeAt = at
		if m.Digital.IsZero() && m.HasDetails() {
			if _, ok := seen[m.ID]; !ok {
				seen[m.ID] = at
			}
		}
		kept = append(kept, m)
	}
	if len(why) > 0 {
		log.Printf("refresh: left out %v", why)
	}

	// scores
	todo = todo[:0]
	for _, m := range kept {
		if env.scoresStale(m) {
			todo = append(todo, m)
		}
	}
	if serr := env.parallel(ctx, todo, "COUNTING TOMATOES", prog, env.Scores); serr != nil {
		warn = netHint(serr).Error()
	}
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	lib = &Library{Movies: kept, Genres: genres, Updated: env.Now(), Region: env.Config.Region, Days: env.Config.Days,
		HomeSeen: seen, Skip: skip, Version: libVersion, Source: "live"}
	lib.fillGenres()
	logNewest(found, kept)
	if err := env.SaveLibrary(lib); err != nil {
		log.Printf("saving library: %v", err)
	}
	if firstErr != nil && warn == "" {
		warn = netHint(firstErr).Error()
	}
	return lib, warn, nil
}

// homeDate decides whether m is a recent home release, and when it became one: its first US
// digital release when TMDB has one, otherwise (TMDB's digital dates lag) the day Rewind first
// saw it available at home. reason says why a film was left out; final means it never will
// qualify (it's an older film), so its details needn't be fetched again soon.
func (env *Env) homeDate(m *Movie, seen map[int]time.Time, seeding bool) (at time.Time, reason string, final bool) {
	now := env.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	window := time.Duration(env.Config.Days) * 24 * time.Hour
	if !m.HasDetails() {
		return m.Released, "", false // couldn't read details: trust discover for now
	}
	if !m.Premiere.IsZero() && now.Sub(m.Premiere) > maxAge {
		return at, "first released over 2 years ago", true
	}
	if len(m.Providers) == 0 && len(m.Stores) == 0 && len(env.Config.Providers) == 0 {
		return at, "not available at home yet", false
	}
	if !m.Digital.IsZero() {
		if now.Sub(m.Digital) > window {
			return at, "digital release older than the window", true
		}
		return m.Digital, "", false
	}
	if len(m.Providers) == 0 && !m.DigitalSoon.IsZero() {
		return at, "digital release still to come (pre-order)", false
	}
	if len(m.Providers) == 0 && !m.Rent {
		return at, "for sale only, with no digital date (likely a pre-order)", false
	}
	base := m.Theatrical
	if base.IsZero() {
		base = m.Premiere
	}
	if base.IsZero() || now.Sub(base) > window {
		return at, "no recent release", true
	}
	t, ok := seen[m.ID]
	switch {
	case ok:
	case seeding && today.Sub(base) > 60*24*time.Hour:
		// first refresh after installing: a film in theaters months ago has probably been at
		// home for a while, so don't put it at the top as if it arrived today
		t = base
	default:
		t = today
	}
	if t.Before(base) {
		t = base
	}
	return t, "", false
}

// logNewest notes what a refresh found, so rewind.log shows whether the guide moved on.
func logNewest(found, kept []*Movie) {
	v := Filter{Sort: SortNewest}.Apply(kept, nil)
	if len(v) == 0 {
		log.Printf("refresh: %d candidates, none qualify", len(found))
		return
	}
	stream := 0
	for _, m := range kept {
		if m.Streaming() {
			stream++
		}
	}
	var top []string
	for i, m := range v {
		if i == 5 {
			break
		}
		top = append(top, fmt.Sprintf("%q %s", m.Title, m.NewDate().Format("01-02")))
	}
	log.Printf("refresh: %d candidates, %d kept (%d streaming, %d rent/buy); newest %s", len(found), len(kept), stream, len(kept)-stream, strings.Join(top, ", "))
}

func countDetails(ms []*Movie) int {
	n := 0
	for _, m := range ms {
		if m.HasDetails() {
			n++
		}
	}
	return n
}

// parallel runs fn over movies with a few workers, reporting progress. It stops early on
// errors the user has to fix (bad key, daily limit) and returns the first error seen.
func (env *Env) parallel(ctx context.Context, ms []*Movie, phase string, prog func(Progress), fn func(context.Context, *Movie) error) error {
	if len(ms) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var first error
	done := 0
	prog(Progress{Phase: phase, Total: len(ms)})
	jobs := make(chan *Movie)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				err := fn(ctx, m)
				mu.Lock()
				done++
				if err != nil && ctx.Err() == nil {
					log.Printf("%s %d %q: %v", phase, m.ID, m.Title, err)
					if first == nil {
						first = err
					}
					if ae, ok := err.(*APIError); ok && (ae.Status == 401 || ae.Status == 429) {
						cancel()
					}
				}
				prog(Progress{Phase: phase, Done: done, Total: len(ms)}) // under the lock: in order, one at a time
				mu.Unlock()
			}
		}()
	}
	for _, m := range ms {
		select {
		case jobs <- m:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return first
}

// ---------- filtering and sorting ----------

type SortKey int

const (
	SortRT SortKey = iota
	SortNewest
	SortPopular
	SortIMDb
	SortMeta
	SortTitle
	SortShortest
	numSorts
)

var sortNames = []string{"TOMATOMETER", "NEWEST", "POPULAR", "IMDB RATING", "METASCORE", "TITLE A-Z", "SHORTEST"}

var minRTSteps = []int{0, 60, 75, 90}
var certSteps = []string{"ANY", "G", "UP TO PG", "UP TO PG-13", "R ONLY"}
var runtimeSteps = []string{"ANY", "UNDER 100 MIN", "UNDER 2 HRS", "2 HRS +"}

// Filter is the user's current view; it is saved between launches.
type Filter struct {
	Sort    SortKey `json:"sort"`
	Genre   string  `json:"genre"`   // "" = all
	Service string  `json:"service"` // "" = all
	MinRT   int     `json:"min_rt"`  // index into minRTSteps
	Cert    int     `json:"cert"`    // index into certSteps
	Runtime int     `json:"runtime"` // index into runtimeSteps
	Hide    bool    `json:"hide_unscored"`
	Owned   int     `json:"radarr"` // index into ownedSteps
	Avail   int     `json:"avail"`  // index into availSteps
}

var availSteps = []string{"STREAMING + RENTAL", "STREAMING ONLY", "RENT / BUY ONLY"}

var ownedSteps = []string{"SHOW ALL", "HIDE WHAT I HAVE", "ONLY WHAT I HAVE"}

func (f Filter) Active() int {
	n := 0
	for _, on := range []bool{f.Genre != "", f.Service != "", f.MinRT > 0, f.Cert > 0, f.Runtime > 0, f.Hide, f.Owned > 0, f.Avail > 0} {
		if on {
			n++
		}
	}
	return n
}

var certRank = map[string]int{"G": 1, "PG": 2, "PG-13": 3, "R": 4, "NC-17": 5, "NR": 6, "UNRATED": 6}

func (f Filter) Match(m *Movie) bool {
	if f.Genre != "" && !contains(m.Genres, f.Genre) {
		return false
	}
	if f.Service != "" && !contains(servicesOf(m), f.Service) {
		return false
	}
	if (f.Avail == 1 && !m.Streaming()) || (f.Avail == 2 && m.Streaming()) {
		return false
	}
	if f.MinRT > 0 && m.RT < minRTSteps[f.MinRT] {
		return false
	}
	if f.Hide && m.RT < 0 {
		return false
	}
	if f.Cert > 0 {
		r := certRank[strings.ToUpper(m.Cert)]
		switch certSteps[f.Cert] {
		case "G":
			if r != 1 {
				return false
			}
		case "UP TO PG":
			if r < 1 || r > 2 {
				return false
			}
		case "UP TO PG-13":
			if r < 1 || r > 3 {
				return false
			}
		case "R ONLY":
			if r != 4 {
				return false
			}
		}
	}
	if f.Runtime > 0 {
		rt := m.Runtime
		if rt == 0 {
			return false
		}
		switch f.Runtime {
		case 1:
			if rt >= 100 {
				return false
			}
		case 2:
			if rt >= 120 {
				return false
			}
		case 3:
			if rt < 120 {
				return false
			}
		}
	}
	return true
}

// Apply filters and sorts. Movies without the sort's score go last.
// owned reports whether a movie is already in Radarr (nil when Radarr isn't set up).
func (f Filter) Apply(all []*Movie, owned func(*Movie) bool) []*Movie {
	var out []*Movie
	for _, m := range all {
		if owned != nil && f.Owned > 0 && owned(m) != (f.Owned == 2) {
			continue
		}
		if f.Match(m) {
			out = append(out, m)
		}
	}
	less := func(a, b *Movie) bool {
		switch f.Sort {
		case SortRT:
			if a.RT != b.RT {
				return a.RT > b.RT
			}
			return a.Popularity > b.Popularity
		case SortNewest:
			if da, db := a.NewDate(), b.NewDate(); !da.Equal(db) {
				return da.After(db)
			}
		case SortIMDb:
			if a.IMDb != b.IMDb {
				return a.IMDb > b.IMDb
			}
		case SortMeta:
			if a.Meta != b.Meta {
				return a.Meta > b.Meta
			}
		case SortTitle:
			ta, tb := sortTitle(a.Title), sortTitle(b.Title)
			if ta != tb {
				return ta < tb
			}
		case SortShortest:
			ra, rb := a.Runtime, b.Runtime
			if ra == 0 {
				ra = 1 << 20
			}
			if rb == 0 {
				rb = 1 << 20
			}
			if ra != rb {
				return ra < rb
			}
		}
		return a.Popularity > b.Popularity
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func sortTitle(t string) string {
	t = strings.ToLower(t)
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(t, a) {
			return t[len(a):]
		}
	}
	return t
}

// Facets lists the genres and services present, most common first.
func Facets(ms []*Movie, key func(*Movie) []string) []string {
	n := map[string]int{}
	for _, m := range ms {
		for _, k := range key(m) {
			n[k]++
		}
	}
	var out []string
	for k := range n {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if n[out[i]] != n[out[j]] {
			return n[out[i]] > n[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func genresOf(m *Movie) []string   { return m.Genres }
func servicesOf(m *Movie) []string { return append(m.ProviderNames(), m.StoreNames()...) }

func (env *Env) statePath() string { return filepath.Join(env.DataDir, "state.json") }

func (env *Env) LoadFilter() Filter {
	var f Filter
	if b, err := os.ReadFile(env.statePath()); err == nil {
		json.Unmarshal(b, &f)
	}
	if f.Sort < 0 || f.Sort >= numSorts {
		f.Sort = 0
	}
	if f.MinRT < 0 || f.MinRT >= len(minRTSteps) {
		f.MinRT = 0
	}
	if f.Cert < 0 || f.Cert >= len(certSteps) {
		f.Cert = 0
	}
	if f.Runtime < 0 || f.Runtime >= len(runtimeSteps) {
		f.Runtime = 0
	}
	if f.Owned < 0 || f.Owned >= len(ownedSteps) {
		f.Owned = 0
	}
	if f.Avail < 0 || f.Avail >= len(availSteps) {
		f.Avail = 0
	}
	return f
}

func (env *Env) SaveFilter(f Filter) {
	b, _ := json.MarshalIndent(f, "", "  ")
	os.WriteFile(env.statePath(), b, 0o644)
}

// SearchResults looks movies up by title and reads their details and scores, for the
// search shelf. Results aren't kept in the guide.
func (env *Env) SearchResults(ctx context.Context, query string, prog func(Progress)) ([]*Movie, error) {
	prog(Progress{Phase: "SEARCHING"})
	ms, err := env.SearchMovies(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := env.parallel(ctx, ms, "READING LABELS", prog, env.Details); err != nil && countDetails(ms) == 0 {
		return nil, err
	}
	var todo []*Movie
	for _, m := range ms {
		if env.scoresStale(m) {
			todo = append(todo, m)
		}
	}
	env.parallel(ctx, todo, "COUNTING TOMATOES", prog, env.Scores)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	log.Printf("search %q: %d results", query, len(ms))
	return ms, nil
}
