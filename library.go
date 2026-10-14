package main

// The library is the guide's list of movies, cached on the SD card so Rewind
// opens instantly and works offline, then refreshed in the background.

import (
	"context"
	"encoding/json"
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
	ID         int          `json:"id"`
	Title      string       `json:"title"`
	Year       int          `json:"year"`
	Released   time.Time    `json:"released"` // digital release in the region when known
	Digital    time.Time    `json:"digital"`
	Tagline    string       `json:"tagline,omitempty"`
	Overview   string       `json:"overview"`
	Poster     string       `json:"poster"`
	Backdrop   string       `json:"backdrop"`
	Stills     []string     `json:"stills,omitempty"`
	GenreIDs   []int        `json:"genre_ids"`
	Genres     []string     `json:"genres,omitempty"`
	Popularity float64      `json:"popularity"`
	VoteAvg    float64      `json:"vote_avg"`
	VoteCount  int          `json:"vote_count"`
	Runtime    int          `json:"runtime"`
	Cert       string       `json:"cert"`
	IMDbID     string       `json:"imdb_id"`
	Directors  []string     `json:"directors,omitempty"`
	Writers    []string     `json:"writers,omitempty"`
	Cast       []CastMember `json:"cast,omitempty"`
	Trailer    *Video       `json:"trailer,omitempty"`
	Providers  []Provider   `json:"providers,omitempty"` // subscription, free and ad-supported services
	Stores     []Provider   `json:"stores,omitempty"`    // where it can be rented or bought
	WatchLink  string       `json:"watch_link,omitempty"`
	RT         int          `json:"rt"`   // Tomatometer 0-100, -1 unknown
	Meta       int          `json:"meta"` // Metascore 0-100, -1 unknown
	IMDb       float64      `json:"imdb"` // 0 unknown
	IMDbVotes  string       `json:"imdb_votes,omitempty"`
	Awards     string       `json:"awards,omitempty"`
	OnSince    time.Time    `json:"on_since,omitempty"` // when Rewind first saw it on a subscription service
	DetailAt   time.Time    `json:"detail_at"`
	ScoresAt   time.Time    `json:"scores_at"`
}

func (m *Movie) HasDetails() bool { return !m.DetailAt.IsZero() }

// NewDate is what the NEWEST sort uses: the day the movie turned up on a service when
// Rewind saw that happen, otherwise its digital release.
func (m *Movie) NewDate() time.Time {
	if m.OnSince.After(m.Released) {
		return m.OnSince
	}
	return m.Released
}

// Arrived reports whether the movie reached a service well after its digital release.
func (m *Movie) Arrived() bool {
	return !m.OnSince.IsZero() && m.OnSince.Sub(m.Released) > 3*24*time.Hour
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
	// Known: every movie the guide has carried, with the day it first appeared, so a film that
	// drops out of the guide and comes back isn't mistaken for a new arrival.
	Known  map[int]time.Time `json:"known,omitempty"`
	Source string            `json:"-"` // "live", "cache", "empty"
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
	return len(lib.Movies) == 0 || lib.Region != env.Config.Region || lib.Days != env.Config.Days ||
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

// detailStale: details (cast, services, trailer) are re-read every few days, since services change.
func (env *Env) detailStale(m *Movie) bool {
	return !m.HasDetails() || env.Now().Sub(m.DetailAt) > 3*24*time.Hour
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
	prog(Progress{Phase: "FINDING NEW RELEASES"})
	found, err := env.Discover(ctx)
	if err != nil {
		return nil, "", err
	}
	prev := map[int]*Movie{}
	for _, m := range old.Movies {
		prev[m.ID] = m
	}
	movies := make([]*Movie, 0, len(found))
	for _, f := range found {
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

	// which movies were only for rent last time, to notice them reaching a service
	rentOnly := map[int]bool{}
	for _, m := range old.Movies {
		if m.HasDetails() && !m.Streaming() {
			rentOnly[m.ID] = true
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

	// keep only movies that really can be watched somewhere (TMDB's discover can lag)
	kept := make([]*Movie, 0, len(movies))
	for _, m := range movies {
		if m.HasDetails() && len(m.Providers) == 0 && len(m.Stores) == 0 && len(env.Config.Providers) == 0 {
			continue
		}
		kept = append(kept, m)
	}

	known := env.markArrivals(old, kept, rentOnly)

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
	lib = &Library{Movies: kept, Genres: genres, Updated: env.Now(), Region: env.Config.Region, Days: env.Config.Days, Known: known, Source: "live"}
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

// markArrivals sets OnSince on each movie. A movie the guide has carried before keeps its
// date, unless it was only for rent and is now on a service: then it arrived today. A new one released digitally in the last two weeks gets its release date; an older
// one has just reached a subscription service (or only now got popular enough to be listed,
// which Rewind can't tell apart), so it gets today. The very first refresh, and the first
// after upgrading from 0.3, has nothing to compare with, so it uses release dates throughout.
func (env *Env) markArrivals(old *Library, ms []*Movie, rentOnly map[int]bool) map[int]time.Time {
	now := env.Now()
	known := map[int]time.Time{}
	if old.Region == env.Config.Region {
		for id, t := range old.Known {
			if now.Sub(t) < 400*24*time.Hour {
				known[id] = t
			}
		}
	}
	seeding := len(known) == 0
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, m := range ms {
		if t, ok := known[m.ID]; ok {
			if rentOnly[m.ID] && m.Streaming() && m.HasDetails() {
				t = today
				known[m.ID] = t
			}
			m.OnSince = t
			continue
		}
		t := m.Released
		if !seeding && (t.IsZero() || now.Sub(t) > 14*24*time.Hour) {
			t = today
		}
		if t.IsZero() {
			t = today
		}
		m.OnSince = t
		known[m.ID] = t
	}
	return known
}

// logNewest notes what a refresh found, so rewind.log shows whether the guide moved on.
func logNewest(found, kept []*Movie) {
	var newest *Movie
	for _, m := range kept {
		if newest == nil || m.NewDate().After(newest.NewDate()) {
			newest = m
		}
	}
	if newest == nil {
		log.Printf("refresh: %d found, none available to watch", len(found))
		return
	}
	stream := 0
	for _, m := range kept {
		if m.Streaming() {
			stream++
		}
	}
	log.Printf("refresh: %d found, %d kept (%d streaming, %d rent/buy), newest %q (%s)", len(found), len(kept), stream, len(kept)-stream, newest.Title, newest.NewDate().Format("2006-01-02"))
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
