package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// busyCatalog fakes a year of streaming releases where, as on the real TMDB, older films have
// had time to get popular and this week's haven't. 0.3 only ever kept the 120 most popular,
// so the newest releases never made the guide and NEWEST stuck on an old film.
type fakeFilm struct {
	id      int
	title   string
	digital time.Time
	pop     float64
	service bool
}

func busyCatalog(t *testing.T, now *time.Time, films map[int]*fakeFilm) *Env {
	mux := http.NewServeMux()
	mux.HandleFunc("/3/genre/movie/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"genres":[{"id":27,"name":"Horror"}]}`)
	})
	mux.HandleFunc("/3/discover/movie", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gte, _ := time.Parse("2006-01-02", q.Get("release_date.gte"))
		lte, _ := time.Parse("2006-01-02", q.Get("release_date.lte"))
		rent := q.Get("with_watch_monetization_types") != "flatrate"
		var ms []*fakeFilm
		for _, f := range films {
			if (f.service || rent) && !f.digital.Before(gte) && !f.digital.After(lte) {
				ms = append(ms, f)
			}
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].pop > ms[j].pop })
		page, _ := strconv.Atoi(q.Get("page"))
		pages := (len(ms) + 19) / 20
		var res []string
		for i := (page - 1) * 20; i < len(ms) && i < page*20; i++ {
			f := ms[i]
			res = append(res, fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"popularity":%v}`, f.id, f.title, f.digital.Format("2006-01-02"), f.pop))
		}
		fmt.Fprintf(w, `{"page":%d,"total_pages":%d,"results":[%s]}`, page, pages, strings.Join(res, ","))
	})
	mux.HandleFunc("/3/search/movie", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(r.URL.Query().Get("query"))
		var res []string
		for id := 1; id <= len(films)+10; id++ {
			if f := films[id]; f != nil && strings.Contains(strings.ToLower(f.title), q) {
				res = append(res, fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"popularity":%v}`, f.id, f.title, f.digital.Format("2006-01-02"), f.pop))
			}
		}
		fmt.Fprintf(w, `{"page":1,"total_pages":1,"results":[%s]}`, strings.Join(res, ","))
	})
	mux.HandleFunc("/3/movie/", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/3/movie/"))
		f := films[id]
		if f == nil {
			w.WriteHeader(404)
			return
		}
		prov := `"rent":[{"provider_id":2,"provider_name":"Apple TV"}]`
		if f.service {
			prov = `"flatrate":[{"provider_id":99,"provider_name":"Shudder"}]`
		}
		d := f.digital.Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(w, `{"id":%d,"title":%q,"runtime":90,"genres":[{"id":27,"name":"Horror"}],
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R","type":4,"release_date":%q}]}]},
			"watch/providers":{"results":{"US":{%s}}}}`, f.id, f.title, d, prov)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := defaultConfig()
	cfg.TMDBKey = "k3y"
	return &Env{DataDir: t.TempDir(), Config: cfg, HTTP: srv.Client(), TMDBBase: srv.URL + "/3", OMDbBase: srv.URL + "/omdb/",
		Now: func() time.Time { return *now }}
}

func newestTitle(lib *Library) string {
	v := Filter{Sort: SortNewest}.Apply(lib.Movies, nil)
	if len(v) == 0 {
		return ""
	}
	return v[0].Title
}

func TestNewReleasesReachTheGuide(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{}
	// 600 films over the past year, the older the more popular
	for i := 1; i <= 600; i++ {
		age := 380 - i*3/5 // 20 to 380 days old
		films[i] = &fakeFilm{id: i, title: fmt.Sprintf("Film %d", i), digital: now.AddDate(0, 0, -age), pop: float64(age + 50), service: true}
	}
	films[601] = &fakeFilm{id: 601, title: "Infirmary", digital: now.AddDate(0, 0, -10), pop: 40, service: true}
	films[602] = &fakeFilm{id: 602, title: "Brand New", digital: now.AddDate(0, 0, -1), pop: 5, service: true}
	env := busyCatalog(t, &now, films)

	lib, _, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Movies) != env.Config.MaxMovies {
		t.Errorf("guide has %d movies, want %d", len(lib.Movies), env.Config.MaxMovies)
	}
	if got := newestTitle(lib); got != "Brand New" {
		t.Errorf("newest = %q, want Brand New", got)
	}
	if byID(lib, 601) == nil {
		t.Errorf("Infirmary missing")
	}

	// a week later: a new release, and a year-old blockbuster that just reached a service
	now = now.AddDate(0, 0, 7)
	films[603] = &fakeFilm{id: 603, title: "Next Week", digital: now.AddDate(0, 0, -2), pop: 3, service: true}
	films[604] = &fakeFilm{id: 604, title: "Blockbuster", digital: now.AddDate(0, 0, -200), pop: 5000, service: true}
	films[605] = &fakeFilm{id: 605, title: "Rental", digital: now.AddDate(0, 0, -1), pop: 4}
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	b := byID(lib, 604)
	if b == nil || !b.Arrived() || !b.NewDate().Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("blockbuster arrival not noticed: %+v", b)
	}
	if got := newestTitle(lib); got != "Blockbuster" {
		t.Errorf("newest = %q, want Blockbuster (arrived today)", got)
	}
	if n := byID(lib, 603); n == nil || n.Arrived() {
		t.Errorf("Next Week should be listed under its release date: %+v", n)
	}
	if bn := byID(lib, 602); bn == nil || bn.Arrived() {
		t.Errorf("Brand New should keep its first date")
	}
	if r := byID(lib, 605); r == nil || r.Streaming() {
		t.Fatalf("a new rental should be listed: %+v", r)
	}

	// a week on, the rental reaches a subscription service
	now = now.AddDate(0, 0, 7)
	films[605].service = true
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if r := byID(lib, 605); r == nil || !r.Streaming() || !r.Arrived() || !r.OnSince.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("rental reaching a service not noticed: %+v", r)
	}

	// the cache keeps the arrival dates
	b2, _ := json.Marshal(lib)
	var back Library
	json.Unmarshal(b2, &back)
	if !back.Known[604].Equal(b.OnSince) {
		t.Errorf("known dates not saved")
	}
}

func TestUpgradeFrom030SeedsFromReleaseDates(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{
		1: {id: 1, title: "Old", digital: now.AddDate(0, 0, -90), pop: 100, service: true},
		2: {id: 2, title: "New", digital: now.AddDate(0, 0, -3), pop: 10, service: true},
	}
	env := busyCatalog(t, &now, films)
	old := &Library{Movies: []*Movie{{ID: 1, Title: "Old", RT: -1, Meta: -1}}, Region: "US", Days: 120, Genres: map[int]string{}}
	lib, _, err := env.Refresh(context.Background(), old, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range lib.Movies {
		if m.Arrived() {
			t.Errorf("%s marked as a new arrival on the first refresh", m.Title)
		}
	}
	if got := newestTitle(lib); got != "New" {
		t.Errorf("newest = %q", got)
	}
}

func TestSearchMovies(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{
		1: {id: 1, title: "The Thing", digital: time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC), pop: 60},
		2: {id: 2, title: "Thingamajig", digital: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), pop: 2, service: true},
		3: {id: 3, title: "Other", digital: now, pop: 1, service: true},
	}
	env := busyCatalog(t, &now, films)
	ms, err := env.SearchResults(context.Background(), "thing", func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Title != "The Thing" || ms[0].Year != 1982 {
		t.Fatalf("results %+v", ms)
	}
	if !ms[0].HasDetails() || ms[0].Runtime != 90 || ms[0].Streaming() || len(ms[0].Stores) != 1 {
		t.Errorf("details not read, or rent-only film shows a service: %+v", ms[0])
	}
	if len(ms[1].Providers) != 1 {
		t.Errorf("Thingamajig should list Shudder")
	}
}

func TestSearchFromTheShelf(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{
		1: {id: 1, title: "Alien", digital: time.Date(1979, 5, 25, 0, 0, 0, 0, time.UTC), pop: 60},
		2: {id: 2, title: "Aliens", digital: time.Date(1986, 7, 18, 0, 0, 0, 0, time.UTC), pop: 50, service: true},
		3: {id: 3, title: "New One", digital: now.AddDate(0, 0, -2), pop: 9, service: true},
	}
	env := busyCatalog(t, &now, films)
	u := NewUI(env, 640, 480)
	pump := func(until func() bool) {
		deadline := time.After(5 * time.Second)
		for !until() {
			select {
			case f := <-u.post:
				f()
			case <-deadline:
				t.Fatal("timed out")
			}
		}
	}
	u.Start()
	pump(func() bool { return !u.refreshing })
	if len(u.visible) != 1 || u.visible[0].Title != "New One" {
		t.Fatalf("guide %d", len(u.visible))
	}
	c := NewCanvas(640, 480)

	u.Key(BtnSelect)
	if u.scr != scrKeyboard {
		t.Fatal("SELECT opens the keyboard")
	}
	u.Key(BtnA) // A
	u.Key(BtnDown)
	u.Key(BtnRight) // L
	u.Key(BtnA)
	u.Key(BtnB) // delete
	u.Key(BtnA)
	if u.kb.text != "AL" {
		t.Fatalf("typed %q", u.kb.text)
	}
	u.Draw(c)
	u.Key(BtnStart)
	if u.scr != scrList || !u.search.on || !u.search.busy {
		t.Fatal("START searches")
	}
	u.Draw(c)
	pump(func() bool { return !u.search.busy })
	if len(u.visible) != 2 || u.visible[0].Title != "Alien" {
		t.Fatalf("results %d", len(u.visible))
	}
	u.Draw(c)
	u.Key(BtnY)
	if u.filter.Sort != SortRT {
		t.Error("Y shouldn't re-sort search results")
	}
	u.Key(BtnA)
	if u.scr != scrDetail || u.cur.Title != "Alien" || !u.cur.HasDetails() {
		t.Fatal("A opens a result's details")
	}
	u.Draw(c)
	u.Key(BtnB)
	u.Key(BtnB)
	if u.search.on || len(u.visible) != 1 {
		t.Fatal("B goes back to the guide")
	}
	// the recent search is remembered
	u2 := NewUI(env, 640, 480)
	u2.openKeyboard()
	if len(u2.search.recent) != 1 || u2.search.recent[0] != "AL" {
		t.Errorf("recent %v", u2.search.recent)
	}
	u2.Draw(c)
	// and the menu still refreshes the guide
	u.Key(BtnStart)
	u.filterRow = fRowRefresh
	u.Key(BtnA)
	if u.modal != modalNone || !u.refreshing {
		t.Error("REFRESH GUIDE in the menu")
	}
	pump(func() bool { return !u.refreshing })
}
