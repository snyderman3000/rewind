package main

// TMDB client: which movies recently came out digitally and are on a
// subscription streaming service now, plus each movie's details.
// "This product uses the TMDB API but is not endorsed or certified by TMDB."
// Streaming availability inside TMDB comes from JustWatch.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (env *Env) tmdbBase() string {
	if env.TMDBBase != "" {
		return env.TMDBBase
	}
	return "https://api.themoviedb.org/3"
}

func (env *Env) imageBase() string {
	if env.ImageBase != "" {
		return env.ImageBase
	}
	return "https://image.tmdb.org/t/p/"
}

// ImageURL builds a TMDB image URL ("w185", "/abc.jpg").
func (env *Env) ImageURL(size, path string) string {
	if path == "" {
		return ""
	}
	return env.imageBase() + size + path
}

// APIError is a problem the user can fix (bad key, rate limit) rather than a network blip.
type APIError struct {
	Service string
	Status  int
	Msg     string
}

func (e *APIError) Error() string { return e.Msg }

var errNoKey = errors.New("no TMDB key")

// v4 read access tokens are long JWTs (they contain dots); v3 keys are 32 hex chars.
func isBearer(key string) bool { return len(key) > 60 && strings.Count(key, ".") == 2 }

func (env *Env) tmdbGet(ctx context.Context, path string, q url.Values, v any) error {
	key := env.Config.TMDBKey
	if key == "" {
		return errNoKey
	}
	if q == nil {
		q = url.Values{}
	}
	if !isBearer(key) {
		q.Set("api_key", key)
	}
	u := env.tmdbBase() + path + "?" + q.Encode()
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if isBearer(key) {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := env.HTTP.Do(req)
		if err != nil {
			if attempt < 2 && ctx.Err() == nil {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 6<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == 200:
			return json.Unmarshal(body, v)
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			if attempt < 3 {
				wait := time.Duration(attempt+1) * 2 * time.Second
				if s, _ := strconv.Atoi(resp.Header.Get("Retry-After")); s > 0 && s < 30 {
					wait = time.Duration(s) * time.Second
				}
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			return &APIError{"TMDB", resp.StatusCode, "TMDB is busy (" + resp.Status + ") — try again in a minute"}
		case resp.StatusCode == 401:
			return &APIError{"TMDB", 401, "TMDB rejected the key — check tmdb_api_key in App/Rewind/data/config.json"}
		case resp.StatusCode == 404:
			return &APIError{"TMDB", 404, "not found on TMDB"}
		default:
			var e struct {
				Msg string `json:"status_message"`
			}
			json.Unmarshal(body, &e)
			if e.Msg == "" {
				e.Msg = resp.Status
			}
			return &APIError{"TMDB", resp.StatusCode, "TMDB: " + e.Msg}
		}
	}
}

type discoverPage struct {
	Page         int          `json:"page"`
	TotalPages   int          `json:"total_pages"`
	TotalResults int          `json:"total_results"`
	Results      []tmdbResult `json:"results"`
}

// discoverQuery: digital releases from the last `days` days that are on a subscription
// service in the region, most popular first.
func (env *Env) discoverQuery(page, days int) url.Values {
	cfg := env.Config
	now := env.Now()
	q := url.Values{}
	q.Set("include_adult", "false")
	q.Set("include_video", "false")
	q.Set("language", cfg.Language)
	q.Set("region", cfg.Region)
	q.Set("watch_region", cfg.Region)
	q.Set("with_watch_monetization_types", "flatrate")
	q.Set("with_release_type", "4") // digital
	q.Set("release_date.gte", now.AddDate(0, 0, -days).Format("2006-01-02"))
	q.Set("release_date.lte", now.Format("2006-01-02"))
	q.Set("sort_by", "popularity.desc")
	q.Set("page", strconv.Itoa(page))
	if len(cfg.Providers) > 0 {
		var ids []string
		for _, p := range cfg.Providers {
			ids = append(ids, strconv.Itoa(p))
		}
		q.Set("with_watch_providers", strings.Join(ids, "|"))
	}
	return q
}

// A discover pass fills the guide up to upTo movies from releases of the last days days.
type discoverPass struct {
	name string
	days int
	upTo int
}

// discoverPasses splits the guide between three windows. Taking only the most popular films
// of the whole window (as 0.3 did) let older, established films crowd out the newest ones, and
// left out big films that reached a subscription service months after their rental release.
// So: the last two weeks first, then the configured window, then the past year.
func (env *Env) discoverPasses() []discoverPass {
	cfg := env.Config
	n := cfg.MaxMovies
	recent := min(14, cfg.Days)
	ps := []discoverPass{{"LAST 14 DAYS", recent, max(1, n/3)}}
	if cfg.Days > recent {
		ps = append(ps, discoverPass{fmt.Sprintf("LAST %d DAYS", cfg.Days), cfg.Days, max(1, n*4/5)})
	}
	if cfg.Days < 365 {
		ps = append(ps, discoverPass{"LAST YEAR", 365, n})
	}
	ps[len(ps)-1].upTo = n
	return ps
}

// Discover returns up to MaxMovies basic entries.
func (env *Env) Discover(ctx context.Context) ([]*Movie, error) {
	var out []*Movie
	seen := map[int]bool{}
	for _, pass := range env.discoverPasses() {
		before := len(out)
		for page := 1; page <= 10 && len(out) < pass.upTo; page++ {
			var dp discoverPage
			if err := env.tmdbGet(ctx, "/discover/movie", env.discoverQuery(page, pass.days), &dp); err != nil {
				if len(out) > 0 {
					log.Printf("discover %s page %d: %v", pass.name, page, err)
					break // keep what we have
				}
				return nil, err
			}
			for _, r := range dp.Results {
				if seen[r.ID] || r.Adult || r.Title == "" {
					continue
				}
				seen[r.ID] = true
				out = append(out, movieFromResult(r))
				if len(out) >= pass.upTo {
					break
				}
			}
			if page >= dp.TotalPages {
				break
			}
		}
		log.Printf("discover %s: +%d", pass.name, len(out)-before)
	}
	return out, nil
}

type tmdbResult struct {
	ID         int     `json:"id"`
	Title      string  `json:"title"`
	Release    string  `json:"release_date"`
	Poster     string  `json:"poster_path"`
	Backdrop   string  `json:"backdrop_path"`
	Overview   string  `json:"overview"`
	GenreIDs   []int   `json:"genre_ids"`
	Popularity float64 `json:"popularity"`
	VoteAvg    float64 `json:"vote_average"`
	VoteCount  int     `json:"vote_count"`
	Adult      bool    `json:"adult"`
}

func movieFromResult(r tmdbResult) *Movie {
	m := &Movie{ID: r.ID, Title: r.Title, Poster: r.Poster, Backdrop: r.Backdrop,
		Overview: r.Overview, GenreIDs: r.GenreIDs, Popularity: r.Popularity,
		VoteAvg: r.VoteAvg, VoteCount: r.VoteCount, RT: -1, Meta: -1}
	if t, err := time.Parse("2006-01-02", r.Release); err == nil {
		m.Year = t.Year()
		m.Released = t
	}
	return m
}

// SearchMovies finds movies of any age by title, best matches first, and reads their details.
func (env *Env) SearchMovies(ctx context.Context, query string) ([]*Movie, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	q.Set("language", env.Config.Language)
	q.Set("page", "1")
	var dp discoverPage
	if err := env.tmdbGet(ctx, "/search/movie", q, &dp); err != nil {
		return nil, err
	}
	var out []*Movie
	for _, r := range dp.Results {
		if r.Adult || r.Title == "" {
			continue
		}
		out = append(out, movieFromResult(r))
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}

// Genres returns TMDB's id → name table for movies.
func (env *Env) Genres(ctx context.Context) (map[int]string, error) {
	var g struct {
		Genres []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
	}
	q := url.Values{}
	q.Set("language", env.Config.Language)
	if err := env.tmdbGet(ctx, "/genre/movie/list", q, &g); err != nil {
		return nil, err
	}
	m := map[int]string{}
	for _, x := range g.Genres {
		m[x.ID] = x.Name
	}
	return m, nil
}

type tmdbDetails struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Tagline  string `json:"tagline"`
	Overview string `json:"overview"`
	Runtime  int    `json:"runtime"`
	IMDbID   string `json:"imdb_id"`
	Release  string `json:"release_date"`
	Poster   string `json:"poster_path"`
	Backdrop string `json:"backdrop_path"`
	Genres   []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	Popularity float64 `json:"popularity"`
	VoteAvg    float64 `json:"vote_average"`
	VoteCount  int     `json:"vote_count"`
	Credits    struct {
		Cast []struct {
			Name      string `json:"name"`
			Character string `json:"character"`
			Order     int    `json:"order"`
		} `json:"cast"`
		Crew []struct {
			Name string `json:"name"`
			Job  string `json:"job"`
		} `json:"crew"`
	} `json:"credits"`
	Videos struct {
		Results []tmdbVideo `json:"results"`
	} `json:"videos"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Cert string `json:"certification"`
				Type int    `json:"type"`
				Date string `json:"release_date"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	Providers struct {
		Results map[string]struct {
			Link     string         `json:"link"`
			Flatrate []tmdbProvider `json:"flatrate"`
			Free     []tmdbProvider `json:"free"`
			Ads      []tmdbProvider `json:"ads"`
		} `json:"results"`
	} `json:"watch/providers"`
	Images struct {
		Backdrops []struct {
			Path  string  `json:"file_path"`
			Lang  *string `json:"iso_639_1"`
			Votes float64 `json:"vote_average"`
			Width int     `json:"width"`
		} `json:"backdrops"`
	} `json:"images"`
}

type tmdbVideo struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Site      string `json:"site"`
	Type      string `json:"type"`
	Official  bool   `json:"official"`
	Lang      string `json:"iso_639_1"`
	Published string `json:"published_at"`
}

type tmdbProvider struct {
	ID       int    `json:"provider_id"`
	Name     string `json:"provider_name"`
	Priority int    `json:"display_priority"`
}

// Details fills in runtime, cast, rating, trailer, services and stills.
func (env *Env) Details(ctx context.Context, m *Movie) error {
	q := url.Values{}
	q.Set("language", env.Config.Language)
	q.Set("append_to_response", "credits,videos,release_dates,watch/providers,images")
	lang := strings.SplitN(env.Config.Language, "-", 2)[0]
	q.Set("include_image_language", "null,"+lang+",en")
	q.Set("include_video_language", lang+",en,null")
	var d tmdbDetails
	if err := env.tmdbGet(ctx, "/movie/"+strconv.Itoa(m.ID), q, &d); err != nil {
		return err
	}
	applyDetails(m, &d, env.Config.Region)
	m.DetailAt = env.Now()
	return nil
}

func applyDetails(m *Movie, d *tmdbDetails, region string) {
	if d.Title != "" {
		m.Title = d.Title
	}
	m.Tagline = strings.TrimSpace(d.Tagline)
	if d.Overview != "" {
		m.Overview = d.Overview
	}
	m.Runtime = d.Runtime
	m.IMDbID = d.IMDbID
	if d.Poster != "" {
		m.Poster = d.Poster
	}
	if d.Backdrop != "" {
		m.Backdrop = d.Backdrop
	}
	if d.Popularity > 0 {
		m.Popularity = d.Popularity
	}
	m.VoteAvg, m.VoteCount = d.VoteAvg, d.VoteCount
	m.GenreIDs = m.GenreIDs[:0]
	m.Genres = m.Genres[:0]
	for _, g := range d.Genres {
		m.GenreIDs = append(m.GenreIDs, g.ID)
		m.Genres = append(m.Genres, g.Name)
	}
	if t, err := time.Parse("2006-01-02", d.Release); err == nil {
		m.Year = t.Year()
	}
	// cast in billing order
	cast := d.Credits.Cast
	sort.SliceStable(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
	m.Cast = m.Cast[:0]
	for i, c := range cast {
		if i == 12 {
			break
		}
		m.Cast = append(m.Cast, CastMember{Name: c.Name, Role: c.Character})
	}
	m.Directors = m.Directors[:0]
	m.Writers = m.Writers[:0]
	for _, c := range d.Credits.Crew {
		switch c.Job {
		case "Director":
			if !contains(m.Directors, c.Name) {
				m.Directors = append(m.Directors, c.Name)
			}
		case "Screenplay", "Writer":
			if !contains(m.Writers, c.Name) && len(m.Writers) < 3 {
				m.Writers = append(m.Writers, c.Name)
			}
		}
	}
	m.Cert, m.Digital = regionalRelease(d, region)
	if !m.Digital.IsZero() {
		m.Released = m.Digital
	}
	m.Trailer = pickTrailer(d.Videos.Results)
	m.Providers = m.Providers[:0]
	m.WatchLink = ""
	if r, ok := d.Providers.Results[region]; ok {
		m.WatchLink = r.Link
		seen := map[string]bool{}
		for _, list := range [][]tmdbProvider{r.Flatrate, r.Free, r.Ads} {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
			for _, p := range list {
				name := cleanProvider(p.Name)
				if name != "" && !seen[name] { // "Netflix" and "Netflix with Ads" are one service here
					seen[name] = true
					m.Providers = append(m.Providers, Provider{ID: p.ID, Name: name})
				}
			}
		}
	}
	// stills for the trailer reel: textless backdrops first, best voted
	bd := d.Images.Backdrops
	sort.SliceStable(bd, func(i, j int) bool {
		ni, nj := bd[i].Lang == nil, bd[j].Lang == nil
		if ni != nj {
			return ni
		}
		return bd[i].Votes > bd[j].Votes
	})
	m.Stills = m.Stills[:0]
	if m.Backdrop != "" {
		m.Stills = append(m.Stills, m.Backdrop)
	}
	for _, b := range bd {
		if len(m.Stills) >= 8 {
			break
		}
		if !contains(m.Stills, b.Path) {
			m.Stills = append(m.Stills, b.Path)
		}
	}
}

// regionalRelease picks the age rating and digital release date for the region.
func regionalRelease(d *tmdbDetails, region string) (cert string, digital time.Time) {
	for _, r := range d.ReleaseDates.Results {
		if r.Country != region {
			continue
		}
		// prefer the theatrical rating, then any
		for _, pass := range []func(int) bool{
			func(t int) bool { return t == 3 || t == 2 },
			func(t int) bool { return true },
		} {
			for _, x := range r.Dates {
				if cert == "" && pass(x.Type) && strings.TrimSpace(x.Cert) != "" {
					cert = strings.TrimSpace(x.Cert)
				}
			}
		}
		for _, x := range r.Dates {
			if x.Type != 4 {
				continue
			}
			t, err := time.Parse(time.RFC3339, x.Date)
			if err != nil {
				t, err = time.Parse("2006-01-02T15:04:05.000Z", x.Date)
			}
			if err == nil && (digital.IsZero() || t.Before(digital)) {
				digital = t
			}
		}
	}
	return cert, digital
}

// pickTrailer prefers an official YouTube trailer in English, newest first.
func pickTrailer(vs []tmdbVideo) *Video {
	score := func(v tmdbVideo) int {
		s := 0
		switch v.Type {
		case "Trailer":
			s += 100
		case "Teaser":
			s += 50
		case "Clip":
			s += 10
		default:
			return -1
		}
		if v.Official {
			s += 20
		}
		if v.Lang == "en" {
			s += 10
		}
		n := strings.ToLower(v.Name)
		if strings.Contains(n, "official trailer") {
			s += 5
		}
		return s
	}
	var best *tmdbVideo
	bs := -1
	for i := range vs {
		v := vs[i]
		if v.Site != "YouTube" || v.Key == "" {
			continue
		}
		s := score(v)
		if s < 0 {
			continue
		}
		if s > bs || (s == bs && v.Published > best.Published) {
			best, bs = &vs[i], s
		}
	}
	if best == nil {
		return nil
	}
	return &Video{Key: best.Key, Name: best.Name, Type: best.Type}
}

// cleanProvider shortens JustWatch's long service names for the small screen.
func cleanProvider(s string) string {
	r := strings.NewReplacer(
		"Amazon Prime Video with Ads", "Prime Video",
		"Amazon Prime Video", "Prime Video",
		"Netflix Standard with Ads", "Netflix",
		"Netflix basic with Ads", "Netflix",
		"Disney Plus", "Disney+",
		"Apple TV Plus", "Apple TV+",
		"Paramount Plus Premium", "Paramount+",
		"Paramount Plus Essential", "Paramount+",
		"Paramount Plus", "Paramount+",
		"Peacock Premium Plus", "Peacock",
		"Peacock Premium", "Peacock",
		"HBO Max Amazon Channel", "HBO Max",
	)
	s = r.Replace(s)
	for _, suf := range []string{" Amazon Channel", " Apple TV Channel", " Roku Premium Channel", " Standard with Ads", " with Ads"} {
		s = strings.TrimSuffix(s, suf)
	}
	return strings.TrimSpace(s)
}

func contains[T comparable](xs []T, v T) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func netHint(err error) error {
	var ae *APIError
	if errors.As(err, &ae) {
		return err
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "certificate has expired") || strings.Contains(s, "not yet valid"):
		return fmt.Errorf("secure connection failed — the clock looks wrong. Sync the time in Tweaks → System → Date and time")
	case strings.Contains(s, "no such host") || strings.Contains(s, "network is unreachable") || strings.Contains(s, "dial"):
		return fmt.Errorf("no network — turn Wi-Fi on in Settings → Network")
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return fmt.Errorf("the connection timed out — check Wi-Fi and try again")
	}
	return err
}
