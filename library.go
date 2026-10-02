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
	Providers  []Provider   `json:"providers,omitempty"`
	WatchLink  string       `json:"watch_link,omitempty"`
	RT         int          `json:"rt"`   // Tomatometer 0-100, -1 unknown
	Meta       int          `json:"meta"` // Metascore 0-100, -1 unknown
	IMDb       float64      `json:"imdb"` // 0 unknown
	IMDbVotes  string       `json:"imdb_votes,omitempty"`
	Awards     string       `json:"awards,omitempty"`
	DetailAt   time.Time    `json:"detail_at"`
	ScoresAt   time.Time    `json:"scores_at"`
}

func (m *Movie) HasDetails() bool { return !m.DetailAt.IsZero() }

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
	Source  string         `json:"-"` // "live", "cache", "empty"
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

	// keep only movies that really are on a subscription service (TMDB's discover can lag)
	kept := make([]*Movie, 0, len(movies))
	for _, m := range movies {
		if m.HasDetails() && len(m.Providers) == 0 && len(env.Config.Providers) == 0 {
			continue
		}
		kept = append(kept, m)
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
	lib = &Library{Movies: kept, Genres: genres, Updated: env.Now(), Region: env.Config.Region, Days: env.Config.Days, Source: "live"}
	lib.fillGenres()
	if err := env.SaveLibrary(lib); err != nil {
		log.Printf("saving library: %v", err)
	}
	if firstErr != nil && warn == "" {
		warn = netHint(firstErr).Error()
	}
	return lib, warn, nil
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
}

func (f Filter) Active() int {
	n := 0
	for _, on := range []bool{f.Genre != "", f.Service != "", f.MinRT > 0, f.Cert > 0, f.Runtime > 0, f.Hide} {
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
	if f.Service != "" && !contains(m.ProviderNames(), f.Service) {
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
func (f Filter) Apply(all []*Movie) []*Movie {
	var out []*Movie
	for _, m := range all {
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
			if !a.Released.Equal(b.Released) {
				return a.Released.After(b.Released)
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
func servicesOf(m *Movie) []string { return m.ProviderNames() }

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
	return f
}

func (env *Env) SaveFilter(f Filter) {
	b, _ := json.MarshalIndent(f, "", "  ")
	os.WriteFile(env.statePath(), b, 0o644)
}
