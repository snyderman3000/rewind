package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPIs serves TMDB- and OMDb-shaped responses for three movies:
// 101 has everything, 102 has no RT score, 103 is no longer on any service.
func fakeAPIs(t *testing.T, omdbErr string) (*Env, *int32, *int32) {
	var details, omdb int32
	mux := http.NewServeMux()
	mux.HandleFunc("/3/genre/movie/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"genres":[{"id":28,"name":"Action"},{"id":18,"name":"Drama"},{"id":10751,"name":"Family"}]}`)
	})
	mux.HandleFunc("/3/discover/movie", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api_key") != "k3y" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"status_code":7,"status_message":"Invalid API key: You must be granted a valid key."}`)
			return
		}
		field := "release_date"
		switch q.Get("with_release_type") {
		case relDigital, relTheatrical:
		case "":
			field = "primary_release_date"
		default:
			t.Errorf("release type %q", q.Get("with_release_type"))
		}
		for k, v := range map[string]string{"watch_region": "US", field + ".lte": "2026-10-01"} {
			if q.Get(k) != v {
				t.Errorf("discover %s = %q, want %q", k, q.Get(k), v)
			}
		}
		if mt := q.Get("with_watch_monetization_types"); mt != streamOnly && mt != anyWay {
			t.Errorf("monetization types %q", mt)
		}
		switch q.Get(field + ".gte") {
		case "2026-09-17", "2026-06-03", "2025-10-01": // last 14 days, the 120-day window, last year
		default:
			t.Errorf("discover %s.gte = %q", field, q.Get(field+".gte"))
		}
		if q.Get("page") == "1" {
			fmt.Fprint(w, `{"page":1,"total_pages":2,"results":[
				{"id":101,"title":"Alpha","release_date":"2026-07-04","poster_path":"/a.jpg","genre_ids":[28,18],"popularity":50,"vote_average":7.1,"vote_count":200},
				{"id":102,"title":"Beta","release_date":"2026-05-01","poster_path":"/b.jpg","genre_ids":[10751],"popularity":80}]}`)
			return
		}
		fmt.Fprint(w, `{"page":2,"total_pages":2,"results":[
			{"id":103,"title":"Gamma","release_date":"2026-08-01","genre_ids":[18],"popularity":10},
			{"id":101,"title":"Alpha dup","release_date":"2026-07-04","genre_ids":[28],"popularity":50},
			{"id":104,"title":"Delta","release_date":"2026-08-02","genre_ids":[18],"popularity":9}]}`)
	})
	mux.HandleFunc("/3/movie/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&details, 1)
		if !strings.Contains(r.URL.Query().Get("append_to_response"), "watch/providers") {
			t.Errorf("details missing watch/providers append")
		}
		id := strings.TrimPrefix(r.URL.Path, "/3/movie/")
		switch id {
		case "101":
			fmt.Fprint(w, `{"id":101,"title":"Alpha","tagline":"It begins.","overview":"A story.","runtime":118,"imdb_id":"tt101",
			"release_date":"2026-04-10","poster_path":"/a2.jpg","backdrop_path":"/bd.jpg",
			"genres":[{"id":28,"name":"Action"},{"id":18,"name":"Drama"}],"vote_average":7.3,"vote_count":250,
			"credits":{"cast":[{"name":"Second","character":"B","order":1},{"name":"First","character":"A","order":0}],
			           "crew":[{"name":"Dee Rector","job":"Director"},{"name":"Wri Ter","job":"Screenplay"},{"name":"Dee Rector","job":"Director"}]},
			"videos":{"results":[
				{"key":"teaser1","name":"Teaser","site":"YouTube","type":"Teaser","official":true,"iso_639_1":"en","published_at":"2026-01-01T00:00:00.000Z"},
				{"key":"vim","name":"Official Trailer","site":"Vimeo","type":"Trailer","official":true,"iso_639_1":"en"},
				{"key":"trail1","name":"Official Trailer","site":"YouTube","type":"Trailer","official":true,"iso_639_1":"en","published_at":"2026-02-01T00:00:00.000Z"},
				{"key":"trail0","name":"Trailer 1","site":"YouTube","type":"Trailer","official":true,"iso_639_1":"en","published_at":"2026-01-15T00:00:00.000Z"}]},
			"release_dates":{"results":[
				{"iso_3166_1":"GB","release_dates":[{"certification":"15","type":3,"release_date":"2026-04-01T00:00:00.000Z"}]},
				{"iso_3166_1":"US","release_dates":[
					{"certification":"","type":4,"release_date":"2026-07-04T00:00:00.000Z"},
					{"certification":"PG-13","type":3,"release_date":"2026-04-10T00:00:00.000Z"}]}]},
			"watch/providers":{"results":{"US":{"link":"https://www.themoviedb.org/movie/101/watch?locale=US",
				"flatrate":[{"provider_id":9,"provider_name":"Amazon Prime Video","display_priority":2},{"provider_id":8,"provider_name":"Netflix","display_priority":1}],
				"ads":[{"provider_id":2100,"provider_name":"Amazon Prime Video with Ads","display_priority":5}],
				"rent":[{"provider_id":2,"provider_name":"Apple TV","display_priority":3}]}}},
			"images":{"backdrops":[{"file_path":"/en.jpg","iso_639_1":"en","vote_average":9},{"file_path":"/nolang.jpg","iso_639_1":null,"vote_average":5}]}}`)
		case "102":
			fmt.Fprint(w, `{"id":102,"title":"Beta","runtime":95,"imdb_id":"tt102","release_date":"2026-03-01",
			"genres":[{"id":10751,"name":"Family"}],
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG","type":3,"release_date":"2026-03-01T00:00:00.000Z"},{"type":4,"release_date":"2026-05-01T00:00:00.000Z"}]}]},
			"watch/providers":{"results":{"US":{"flatrate":[{"provider_id":337,"provider_name":"Disney Plus","display_priority":1}]}}}}`)
		case "103":
			fmt.Fprint(w, `{"id":103,"title":"Gamma","imdb_id":"tt103","watch/providers":{"results":{"US":{"rent":[{"provider_id":2,"provider_name":"Apple TV"}]}}}}`)
		case "104":
			fmt.Fprint(w, `{"id":104,"title":"Delta","imdb_id":"tt104","watch/providers":{"results":{}}}`)
		default:
			w.WriteHeader(404)
		}
	})
	mux.HandleFunc("/omdb/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&omdb, 1)
		if omdbErr != "" {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"Response":"False","Error":%q}`, omdbErr)
			return
		}
		switch r.URL.Query().Get("i") {
		case "tt101":
			fmt.Fprint(w, `{"Response":"True","Rated":"PG-13","Metascore":"77","imdbRating":"7.6","imdbVotes":"12,345","Awards":"N/A",
			"Ratings":[{"Source":"Internet Movie Database","Value":"7.6/10"},{"Source":"Rotten Tomatoes","Value":"91%"},{"Source":"Metacritic","Value":"77/100"}]}`)
		case "tt102":
			fmt.Fprint(w, `{"Response":"True","Metascore":"N/A","imdbRating":"6.1","Ratings":[{"Source":"Internet Movie Database","Value":"6.1/10"}]}`)
		default:
			fmt.Fprint(w, `{"Response":"False","Error":"Incorrect IMDb ID."}`)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := defaultConfig()
	cfg.TMDBKey, cfg.OMDbKey = "k3y", "omdbk"
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	env := &Env{DataDir: t.TempDir(), Config: cfg, HTTP: srv.Client(), TMDBBase: srv.URL + "/3", OMDbBase: srv.URL + "/omdb/",
		Now: func() time.Time { return now }}
	return env, &details, &omdb
}

func byID(lib *Library, id int) *Movie {
	for _, m := range lib.Movies {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func TestRefreshBuildsGuide(t *testing.T) {
	env, details, omdb := fakeAPIs(t, "")
	var last Progress
	lib, warn, err := env.Refresh(context.Background(), env.LoadLibrary(), func(p Progress) { last = p })
	if err != nil || warn != "" {
		t.Fatalf("refresh: %v / %q", err, warn)
	}
	if last.Phase != "COUNTING TOMATOES" || last.Done != last.Total {
		t.Errorf("last progress %+v", last)
	}
	if len(lib.Movies) != 3 {
		t.Fatalf("want Alpha, Beta and Gamma (rent-only), got %d movies", len(lib.Movies))
	}
	if g := byID(lib, 103); g.Streaming() || len(g.Stores) != 1 || g.Stores[0].Name != "Apple TV" {
		t.Errorf("Gamma should be rent-only at Apple TV: %+v %+v", g.Providers, g.Stores)
	}
	if v := (Filter{Avail: 2}).Apply(lib.Movies, nil); len(v) != 1 || v[0].ID != 103 {
		t.Errorf("RENT / BUY ONLY filter: %d", len(v))
	}
	if v := (Filter{Avail: 1}).Apply(lib.Movies, nil); len(v) != 2 {
		t.Errorf("STREAMING ONLY filter: %d", len(v))
	}
	if v := (Filter{Service: "Apple TV"}).Apply(lib.Movies, nil); len(v) != 2 {
		t.Errorf("service filter should match rental stores: %d", len(v))
	}
	a := byID(lib, 101)
	if a.RT != 91 || a.Meta != 77 || a.IMDb != 7.6 {
		t.Errorf("scores %d %d %v", a.RT, a.Meta, a.IMDb)
	}
	if a.Cert != "PG-13" {
		t.Errorf("cert %q", a.Cert)
	}
	if !a.Released.Equal(time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("digital date %v", a.Released)
	}
	if a.Trailer == nil || a.Trailer.Key != "trail1" {
		t.Errorf("trailer %+v", a.Trailer)
	}
	if got := strings.Join(a.ProviderNames(), ","); got != "Netflix,Prime Video" {
		t.Errorf("providers %q", got)
	}
	if a.Cast[0].Name != "First" || len(a.Directors) != 1 || a.Writers[0] != "Wri Ter" {
		t.Errorf("credits %+v %v %v", a.Cast, a.Directors, a.Writers)
	}
	if strings.Join(a.Stills, ",") != "/bd.jpg,/nolang.jpg,/en.jpg" {
		t.Errorf("stills %v", a.Stills)
	}
	if a.Poster != "/a2.jpg" || a.Runtime != 118 || a.Year != 2026 {
		t.Errorf("basics %q %d %d", a.Poster, a.Runtime, a.Year)
	}
	b := byID(lib, 102)
	if b.RT != -1 || b.Meta != -1 || b.IMDb != 6.1 || b.Cert != "PG" || b.Genres[0] != "Family" || b.ProviderNames()[0] != "Disney+" {
		t.Errorf("beta %+v", b)
	}
	if atomic.LoadInt32(details) != 4 || atomic.LoadInt32(omdb) != 3 {
		t.Errorf("requests: details %d omdb %d", *details, atomic.LoadInt32(omdb))
	}

	// cached: a second refresh re-reads the list but not fresh details or scores
	lib2 := env.LoadLibrary()
	if lib2.Source != "cache" || len(lib2.Movies) != 3 || byID(lib2, 101).RT != 91 {
		t.Fatalf("cache didn't round-trip")
	}
	if env.NeedsRefresh(lib2) {
		t.Errorf("fresh cache wants a refresh")
	}
	if _, _, err := env.Refresh(context.Background(), lib2, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	// Delta (104) can't be watched anywhere, so it was dropped and is looked up again
	if atomic.LoadInt32(details) != 5 || atomic.LoadInt32(omdb) != 3 {
		t.Errorf("second refresh requests: details %d omdb %d", *details, atomic.LoadInt32(omdb))
	}
}

func TestRefreshBadKey(t *testing.T) {
	env, _, _ := fakeAPIs(t, "")
	env.Config.TMDBKey = "nope"
	_, _, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err == nil || !strings.Contains(err.Error(), "rejected the key") {
		t.Fatalf("want key error, got %v", err)
	}
}

func TestOMDbLimitIsAWarning(t *testing.T) {
	env, _, omdb := fakeAPIs(t, "Request limit reached!")
	lib, warn, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "daily limit") || len(lib.Movies) != 3 || byID(lib, 101).RT != -1 {
		t.Errorf("warn %q, %d movies", warn, len(lib.Movies))
	}
	if atomic.LoadInt32(omdb) > 4 {
		t.Errorf("kept calling OMDb after the limit: %d", atomic.LoadInt32(omdb))
	}
}

func TestNoOMDbKey(t *testing.T) {
	env, _, omdb := fakeAPIs(t, "")
	env.Config.OMDbKey = ""
	lib, warn, err := env.Refresh(context.Background(), env.LoadLibrary(), func(Progress) {})
	if err != nil || warn != "" || atomic.LoadInt32(omdb) != 0 || byID(lib, 101).Cert != "PG-13" {
		t.Fatalf("err %v warn %q omdb %d", err, warn, atomic.LoadInt32(omdb))
	}
}

func TestBearerToken(t *testing.T) {
	tok := "eyJhbGciOiJIUzI1NiJ9." + strings.Repeat("x", 80) + ".sig"
	if !isBearer(tok) || isBearer("0123456789abcdef0123456789abcdef") {
		t.Fatal("isBearer")
	}
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization") + "|" + r.URL.Query().Get("api_key")
		fmt.Fprint(w, `{"genres":[]}`)
	}))
	defer srv.Close()
	env := &Env{Config: Config{TMDBKey: tok, Language: "en-US"}, HTTP: srv.Client(), TMDBBase: srv.URL, Now: time.Now}
	env.Genres(context.Background())
	if got != "Bearer "+tok+"|" {
		t.Errorf("auth sent as %q", got)
	}
}

func TestFilterAndSort(t *testing.T) {
	ms := []*Movie{
		{ID: 1, Title: "The Zebra", RT: 80, Cert: "PG", Runtime: 90, Genres: []string{"Family"}, Popularity: 1, Providers: []Provider{{8, "Netflix"}}},
		{ID: 2, Title: "Apple", RT: -1, Cert: "R", Runtime: 130, Genres: []string{"Drama"}, Popularity: 9},
		{ID: 3, Title: "Mango", RT: 95, Cert: "PG-13", Runtime: 110, Genres: []string{"Drama", "Family"}, Popularity: 5, Providers: []Provider{{9, "Prime Video"}}},
		{ID: 4, Title: "A Kiwi", RT: 40, Cert: "G", Runtime: 0, Genres: []string{"Family"}, Popularity: 3},
	}
	ids := func(f Filter) string {
		var s []string
		for _, m := range f.Apply(ms, nil) {
			s = append(s, fmt.Sprint(m.ID))
		}
		return strings.Join(s, "")
	}
	cases := []struct {
		f    Filter
		want string
	}{
		{Filter{Sort: SortRT}, "3142"},
		{Filter{Sort: SortTitle}, "2431"},
		{Filter{Sort: SortPopular}, "2341"},
		{Filter{Sort: SortShortest}, "1324"},
		{Filter{Sort: SortRT, Genre: "Family"}, "314"},
		{Filter{Sort: SortRT, MinRT: 1}, "31"},
		{Filter{Sort: SortRT, Hide: true}, "314"},
		{Filter{Sort: SortRT, Cert: 2}, "14"},  // up to PG
		{Filter{Sort: SortRT, Cert: 3}, "314"}, // up to PG-13
		{Filter{Sort: SortRT, Cert: 4}, "2"},   // R only
		{Filter{Sort: SortRT, Runtime: 1}, "1"},
		{Filter{Sort: SortRT, Runtime: 3}, "2"},
		{Filter{Sort: SortRT, Service: "Netflix"}, "1"},
	}
	for _, c := range cases {
		if got := ids(c.f); got != c.want {
			t.Errorf("%+v: got %s want %s", c.f, got, c.want)
		}
	}
	if g := Facets(ms, genresOf); strings.Join(g, ",") != "Family,Drama" {
		t.Errorf("facets %v", g)
	}
}

func TestFilterStateRoundTrip(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	f := Filter{Sort: SortIMDb, Genre: "Horror", MinRT: 2, Cert: 3, Hide: true}
	env.SaveFilter(f)
	if got := env.LoadFilter(); got != f {
		t.Errorf("got %+v", got)
	}
	b, _ := json.Marshal(Filter{Sort: 99, MinRT: 9})
	writeFile(t, env.statePath(), b)
	if got := env.LoadFilter(); got.Sort != 0 || got.MinRT != 0 {
		t.Errorf("bad state not clamped: %+v", got)
	}
}

func TestCleanProvider(t *testing.T) {
	for in, want := range map[string]string{
		"Amazon Prime Video":        "Prime Video",
		"Netflix Standard with Ads": "Netflix",
		"Paramount Plus Premium":    "Paramount+",
		"Starz Amazon Channel":      "Starz",
		"AMC+ Roku Premium Channel": "AMC+",
		"Disney Plus":               "Disney+",
		"Hulu":                      "Hulu",
	} {
		if got := cleanProvider(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// The UI boots against the fake APIs, lands on the list and loads a poster.
func TestUIStartsAndLoadsPoster(t *testing.T) {
	env, _, _ := fakeAPIs(t, "")
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := NewCanvas(185, 278)
		c.Fill(0, 0, 185, 278, colTomato)
		jpeg.Encode(w, c, nil)
	}))
	defer img.Close()
	env.ImageBase = img.URL + "/"
	u := NewUI(env, 640, 480)
	u.Start()
	if u.scr != scrBoot || !u.refreshing {
		t.Fatalf("scr %v refreshing %v", u.scr, u.refreshing)
	}
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
	pump(func() bool { return !u.refreshing })
	if u.scr != scrList || len(u.visible) != 3 || u.visible[0].Title != "Alpha" {
		t.Fatalf("scr %v visible %d", u.scr, len(u.visible))
	}
	u.moved = time.Now().Add(-time.Second)
	u.Tick()
	pump(func() bool { p, _ := u.listPoster(u.visible[0], false); return p != nil })
	p, _ := u.listPoster(u.visible[0], false)
	if w, h := u.listPosterBox(); p.Bounds().Dx() != w || p.Bounds().Dy() != h {
		t.Errorf("poster %v", p.Bounds())
	}
	c := NewCanvas(640, 480)
	u.Draw(c)
	// a second launch opens straight from the cache without a refresh
	u2 := NewUI(env, 640, 480)
	u2.Start()
	if u2.scr != scrList || u2.refreshing || len(u2.visible) != 3 {
		t.Errorf("cached start: scr %v refreshing %v", u2.scr, u2.refreshing)
	}
}

func TestConfigProblems(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/config.json"
	writeFile(t, p, []byte("{\n  \"tmdb_api_key\": \"abc\",\n  \"refresh_hours\": 6\n  \"radarr_url\": \"http://x:7878\"\n}\n"))
	cfg := loadConfig(p)
	if !strings.Contains(cfg.Problem, "missing comma at the end of line 3") || !strings.Contains(cfg.Problem, `"refresh_hours": 6`) {
		t.Errorf("problem %q", cfg.Problem)
	}
	writeFile(t, p, []byte(`{"days": "90"}`))
	if cfg := loadConfig(p); !strings.Contains(cfg.Problem, `"days"`) {
		t.Errorf("type problem %q", cfg.Problem)
	}
	writeFile(t, p, []byte(`{"tmdb_api_key": "abc", "radarr_url": "x"}`))
	if cfg := loadConfig(p); cfg.Problem != "" || cfg.TMDBKey != "abc" {
		t.Errorf("good config %+v", cfg)
	}
}
