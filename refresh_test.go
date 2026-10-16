package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// busyCatalog fakes TMDB for a catalog of films. As on the real TMDB, older films have had
// time to get popular and this week's haven't, and discover matches any release entry of the
// asked-for type in the window, re-releases included.
type fakeFilm struct {
	id           int
	title        string
	digital      time.Time // first US digital release; zero: TMDB has none (yet)
	redigital    time.Time // a later US digital entry (4K re-release and the like)
	soon         time.Time // a scheduled US digital release, still to come
	theatrical   time.Time // first US theatrical release; zero: never in US theaters
	retheatrical time.Time // an anniversary showing
	premiere     time.Time // first release anywhere, when earlier than the US ones
	pop          float64
	service      bool // on a subscription service; otherwise to rent or buy
	buyOnly      bool // sold (pre-order) but not rented
	notHome      bool // can't be watched at home at all yet
}

// first is a film's first release of any kind (TMDB's primary release date).
func (f *fakeFilm) first() time.Time {
	t := f.premiere
	for _, d := range []time.Time{f.digital, f.theatrical} {
		if !d.IsZero() && (t.IsZero() || d.Before(t)) {
			t = d
		}
	}
	return t
}

var (
	mu          sync.Mutex
	detailReads = map[int]int{} // details requests per film id, across a test
)

func reads(id int) int {
	mu.Lock()
	defer mu.Unlock()
	return detailReads[id]
}

func busyCatalog(t *testing.T, now *time.Time, films map[int]*fakeFilm) *Env {
	mu.Lock()
	detailReads = map[int]int{}
	mu.Unlock()
	mux := http.NewServeMux()
	mux.HandleFunc("/3/genre/movie/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"genres":[{"id":27,"name":"Horror"}]}`)
	})
	mux.HandleFunc("/3/discover/movie", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		field, dates := "release_date", func(f *fakeFilm) []time.Time { return []time.Time{f.digital, f.redigital, f.soon} }
		switch q.Get("with_release_type") {
		case relTheatrical:
			dates = func(f *fakeFilm) []time.Time { return []time.Time{f.theatrical, f.retheatrical} }
		case "":
			field, dates = "primary_release_date", func(f *fakeFilm) []time.Time { return []time.Time{f.first()} }
		}
		gte, _ := time.Parse("2006-01-02", q.Get(field+".gte"))
		lte, _ := time.Parse("2006-01-02", q.Get(field+".lte"))
		rent := q.Get("with_watch_monetization_types") != "flatrate"
		var ms []*fakeFilm
		for _, f := range films {
			if !(f.service || rent) || f.notHome {
				continue
			}
			for _, d := range dates(f) {
				if !d.IsZero() && !d.Before(gte) && !d.After(lte) {
					ms = append(ms, f)
					break
				}
			}
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].pop > ms[j].pop })
		page, _ := strconv.Atoi(q.Get("page"))
		pages := (len(ms) + 19) / 20
		var res []string
		for i := (page - 1) * 20; i < len(ms) && i < page*20; i++ {
			f := ms[i]
			res = append(res, fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"popularity":%v}`, f.id, f.title, f.first().Format("2006-01-02"), f.pop))
		}
		fmt.Fprintf(w, `{"page":%d,"total_pages":%d,"results":[%s]}`, page, pages, strings.Join(res, ","))
	})
	mux.HandleFunc("/3/search/movie", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(r.URL.Query().Get("query"))
		var res []string
		for id := 1; id <= len(films)+10; id++ {
			if f := films[id]; f != nil && strings.Contains(strings.ToLower(f.title), q) {
				res = append(res, fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"popularity":%v}`, f.id, f.title, f.first().Format("2006-01-02"), f.pop))
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
		mu.Lock()
		detailReads[id]++
		mu.Unlock()
		prov := `"rent":[{"provider_id":2,"provider_name":"Apple TV"}],"buy":[{"provider_id":2,"provider_name":"Apple TV"}]`
		switch {
		case f.notHome:
			prov = ""
		case f.service:
			prov = `"flatrate":[{"provider_id":99,"provider_name":"Shudder"}]`
		case f.buyOnly:
			prov = `"buy":[{"provider_id":2,"provider_name":"Apple TV"}]`
		}
		var dates []string
		add := func(typ int, d time.Time) {
			if !d.IsZero() {
				dates = append(dates, fmt.Sprintf(`{"certification":"","type":%d,"release_date":%q}`, typ, d.Format("2006-01-02T15:04:05.000Z")))
			}
		}
		add(4, f.digital)
		add(4, f.redigital)
		add(4, f.soon)
		add(3, f.theatrical)
		add(3, f.retheatrical)
		fmt.Fprintf(w, `{"id":%d,"title":%q,"runtime":90,"release_date":%q,"genres":[{"id":27,"name":"Horror"}],
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[%s]}]},
			"watch/providers":{"results":{"US":{%s}}}}`, f.id, f.title, f.first().Format("2006-01-02"), strings.Join(dates, ","), prov)
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
	env.Config.MaxMovies = 60 // fewer slots than recent films, so popularity decides

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
	for _, m := range lib.Movies {
		if now.Sub(m.NewDate()) > 120*24*time.Hour {
			t.Errorf("%s came out %v, before the window", m.Title, m.NewDate())
		}
	}

	// a week later: a new release, a rental, and a 200-day-old blockbuster that's popular
	// enough to be discovered now. It isn't new, so it isn't listed.
	now = now.AddDate(0, 0, 7)
	films[603] = &fakeFilm{id: 603, title: "Next Week", digital: now.AddDate(0, 0, -2), pop: 3, service: true}
	films[604] = &fakeFilm{id: 604, title: "Blockbuster", digital: now.AddDate(0, 0, -200), redigital: now.AddDate(0, 0, -5), pop: 5000, service: true}
	films[605] = &fakeFilm{id: 605, title: "Rental", digital: now.AddDate(0, 0, -1), pop: 4}
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if byID(lib, 604) != nil {
		t.Errorf("a 200-day-old film with a 4K re-release isn't new")
	}
	if got := newestTitle(lib); got != "Rental" {
		t.Errorf("newest = %q, want Rental", got)
	}
	if r := byID(lib, 605); r == nil || r.Streaming() || !r.NewDate().Equal(r.Digital) {
		t.Fatalf("a new rental should be listed under its digital date: %+v", r)
	}

	// a week on, the rental reaches a subscription service: still dated by its digital release
	now = now.AddDate(0, 0, 7)
	films[605].service = true
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if r := byID(lib, 605); r == nil || !r.Streaming() || !r.NewDate().Equal(r.Digital) {
		t.Fatalf("rental on a service: %+v", r)
	}
}

// What 0.6 got wrong on Oct 16, 2026: NEWEST led with The Odyssey (digital release still to
// come, pre-orders open), then films from last year and catalog films with re-release dates,
// all stamped "arrived today" because 0.6 hadn't listed them before.
func TestReportedCases(t *testing.T) {
	now := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	films := map[int]*fakeFilm{
		1:  {id: 1, title: "The Odyssey", theatrical: d(2026, 7, 17), soon: d(2026, 11, 17), buyOnly: true, pop: 900},
		2:  {id: 2, title: "Marty Supreme", theatrical: d(2025, 12, 25), digital: d(2026, 2, 10), redigital: d(2026, 9, 1), service: true, pop: 800},
		3:  {id: 3, title: "Hamnet", theatrical: d(2025, 11, 26), digital: d(2026, 1, 20), retheatrical: d(2026, 8, 15), service: true, pop: 700},
		4:  {id: 4, title: "Wicked: For Good", theatrical: d(2025, 11, 21), digital: d(2026, 1, 13), redigital: d(2026, 9, 20), service: true, pop: 750},
		5:  {id: 5, title: "Avengers: Endgame", theatrical: d(2019, 4, 26), digital: d(2019, 7, 30), redigital: d(2026, 8, 20), service: true, pop: 1000},
		6:  {id: 6, title: "The Hunger Games", theatrical: d(2012, 3, 23), digital: d(2012, 8, 18), retheatrical: d(2026, 9, 20), service: true, pop: 600},
		7:  {id: 7, title: "Practical Magic 2", theatrical: d(2026, 9, 11), pop: 300},
		8:  {id: 8, title: "Runner", theatrical: d(2026, 9, 11), pop: 200},
		9:  {id: 9, title: "Buddy", digital: d(2026, 10, 14), pop: 150},
		10: {id: 10, title: "Still In Theaters", theatrical: d(2026, 10, 2), notHome: true, pop: 500},
		11: {id: 11, title: "Infirmary", digital: d(2026, 10, 2), service: true, pop: 90},
		12: {id: 12, title: "Summer Hit", theatrical: d(2026, 6, 20), digital: d(2026, 7, 29), service: true, pop: 400},
		13: {id: 13, title: "Festival Darling", premiere: d(2025, 1, 25), digital: d(2026, 9, 30), pop: 50},
	}
	env := busyCatalog(t, &now, films)
	lib, _, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2, 3, 4, 5, 6, 10} {
		if m := byID(lib, id); m != nil {
			t.Errorf("%s shouldn't be listed (new date %v)", m.Title, m.NewDate())
		}
	}
	for _, id := range []int{7, 8, 9, 11, 12, 13} {
		if byID(lib, id) == nil {
			t.Errorf("%s missing", films[id].title)
		}
	}
	var order []string
	for _, m := range (Filter{Sort: SortNewest}).Apply(lib.Movies, nil) {
		order = append(order, m.Title)
	}
	want := []string{"Practical Magic 2", "Runner", "Buddy", "Infirmary", "Festival Darling", "Summer Hit"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("NEWEST = %v, want %v", order, want)
	}
	if pm := byID(lib, 7); pm == nil || !pm.HomeAt.Equal(d(2026, 10, 16)) || !pm.Digital.IsZero() {
		t.Errorf("Practical Magic 2 at home %v", pm.HomeAt)
	}
	if reads(5) != 0 || reads(6) != 0 {
		t.Errorf("old films' details were read: Endgame %d, Hunger Games %d", reads(5), reads(6))
	}

	// next day: TMDB has added Practical Magic 2's digital date; last year's films aren't re-read
	now = now.AddDate(0, 0, 1)
	films[7].digital = d(2026, 10, 13)
	for _, m := range lib.Movies {
		m.DetailAt = time.Time{} // as if 3 days had passed for details
	}
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if pm := byID(lib, 7); pm == nil || !pm.HomeAt.Equal(d(2026, 10, 13)) {
		t.Errorf("Practical Magic 2 should now go by its digital date: %+v", pm)
	}
	if r := byID(lib, 8); r == nil || !r.HomeAt.Equal(d(2026, 10, 16)) {
		t.Errorf("Runner keeps the day it was first seen at home: %+v", r)
	}
	if reads(2) != 1 || reads(3) != 1 {
		t.Errorf("last year's films re-read: Marty %d, Hamnet %d", reads(2), reads(3))
	}
	if reads(1) != 2 {
		t.Errorf("The Odyssey should be checked again each refresh: %d", reads(1))
	}
}

// 0.6's cache stamped films "arrived today"; 0.7 ignores those dates and refreshes at once.
func TestUpgradeFrom060(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{
		1: {id: 1, title: "Old", digital: now.AddDate(0, 0, -90), pop: 100, service: true},
		2: {id: 2, title: "New", digital: now.AddDate(0, 0, -3), pop: 10, service: true},
	}
	env := busyCatalog(t, &now, films)
	cache := `{"region":"US","days":120,"updated":"2026-10-16T11:00:00Z","known":{"1":"2026-10-16T00:00:00Z"},
		"movies":[{"id":1,"title":"Old","on_since":"2026-10-16T00:00:00Z","released":"2026-07-18T00:00:00Z","detail_at":"2026-10-16T11:00:00Z","rt":-1,"meta":-1}]}`
	writeFile(t, env.DataDir+"/library.json", []byte(cache))
	old := env.LoadLibrary()
	if !env.NeedsRefresh(old) {
		t.Error("a 0.6 cache should refresh on launch")
	}
	lib, _, err := env.Refresh(context.Background(), old, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := newestTitle(lib); got != "New" {
		t.Errorf("newest = %q", got)
	}
	if o := byID(lib, 1); o == nil || !o.NewDate().Equal(o.Digital) || reads(1) != 1 {
		t.Errorf("Old should be re-read and dated by its digital release: %+v", o)
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

// Practical Magic 2's situation: in theaters a month ago, rentable since yesterday, but
// TMDB hasn't recorded a US digital date yet. 0.5 never listed films like that.
func TestRentableBeforeTMDBHasADigitalDate(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	films := map[int]*fakeFilm{}
	for i := 1; i <= 400; i++ { // a busy year of established, more popular releases
		age := 380 - i*9/10
		films[i] = &fakeFilm{id: i, title: fmt.Sprintf("Film %d", i), digital: now.AddDate(0, 0, -age), pop: float64(age + 50), service: i%2 == 0}
	}
	films[901] = &fakeFilm{id: 901, title: "Seen Before", digital: now.AddDate(0, 0, -5), pop: 30}
	env := busyCatalog(t, &now, films)
	lib, _, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}

	now = now.AddDate(0, 0, 1)
	sep11 := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	films[902] = &fakeFilm{id: 902, title: "Practical Magic 2", theatrical: sep11, pop: 45}
	films[903] = &fakeFilm{id: 903, title: "Still In Theaters", theatrical: sep11, pop: 60, notHome: true}
	films[904] = &fakeFilm{id: 904, title: "Festival Indie", digital: now.AddDate(0, 0, -1), pop: 1}
	films[905] = &fakeFilm{id: 905, title: "August Rental", theatrical: now.AddDate(0, 0, -80), digital: now.AddDate(0, 0, -50), pop: 2}
	lib, _, err = env.Refresh(context.Background(), lib, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	pm := byID(lib, 902)
	if pm == nil {
		t.Fatal("a film rentable before TMDB has its digital date should be listed")
	}
	if !pm.Released.Equal(sep11) || !pm.Digital.IsZero() || !pm.HomeAt.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Practical Magic 2: released %v digital %v at home %v", pm.Released, pm.Digital, pm.HomeAt)
	}
	if got := newestTitle(lib); got != "Practical Magic 2" {
		t.Errorf("newest = %q, want Practical Magic 2", got)
	}
	if byID(lib, 903) != nil {
		t.Error("a film that's only in theaters can't be watched at home")
	}
	if byID(lib, 904) == nil {
		t.Error("this week's digital release missing")
	}
	if ar := byID(lib, 905); ar != nil && !ar.HomeAt.Equal(ar.Digital) {
		t.Errorf("a rental found late keeps its digital date, not today: %+v", ar.HomeAt)
	}
}
