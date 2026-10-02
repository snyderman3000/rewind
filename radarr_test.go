package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRadarr struct {
	mu    sync.Mutex
	added map[string]any
	have  map[int]bool
}

func newFakeRadarr(t *testing.T) (*Env, *fakeRadarr) {
	f := &fakeRadarr{have: map[int]bool{555: true}}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") != "rkey" {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/v3/movie", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			json.Unmarshal(b, &m)
			id := int(m["tmdbId"].(float64))
			if f.have[id] {
				w.WriteHeader(400)
				fmt.Fprint(w, `[{"propertyName":"TmdbId","errorMessage":"This movie has already been added","errorCode":"MovieExistsValidator"}]`)
				return
			}
			f.added, f.have[id] = m, true
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"id":77,"tmdbId":%d,"title":%q,"hasFile":false,"monitored":true}`, id, m["title"])
			return
		}
		fmt.Fprint(w, `[{"id":1,"tmdbId":555,"title":"Owned","hasFile":true,"monitored":true,"images":[{"coverType":"poster"}]},
			{"id":2,"tmdbId":101,"title":"Alpha","hasFile":false,"monitored":true},{"id":3,"tmdbId":0,"title":"No TMDB"}]`)
	}))
	mux.HandleFunc("/api/v3/movie/lookup/tmdb", auth(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("tmdbId")
		if id == "404" {
			w.WriteHeader(404)
			return
		}
		extra := ""
		if id == "555" {
			extra = `"id":1,"hasFile":true,"monitored":true,`
		}
		fmt.Fprintf(w, `{%s"title":"Neon Harbor","tmdbId":%s,"year":2026,"titleSlug":"neon-harbor-%s","images":[],"path":"/bad/Neon Harbor (2026)","folder":"Neon Harbor (2026)"}`, extra, id, id)
	}))
	mux.HandleFunc("/api/v3/qualityprofile", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1,"name":"Any"},{"id":4,"name":"HD-1080p"},{"id":6,"name":"Ultra-HD"}]`)
	}))
	mux.HandleFunc("/api/v3/rootfolder", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1,"path":"/gone","accessible":false},{"id":2,"path":"/movies","accessible":true,"freeSpace":1300000000000}]`)
	}))
	mux.HandleFunc("/api/v3/queue/details", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("movieId") == "2" {
			fmt.Fprint(w, `[{"size":1000,"sizeleft":250,"status":"downloading","trackedDownloadState":"downloading","timeleft":"01:05:09"}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := defaultConfig()
	// no scheme and a trailing slash, the way people type it
	cfg.RadarrURL, cfg.RadarrKey = strings.TrimPrefix(srv.URL, "http://")+"/", "rkey"
	env := &Env{DataDir: t.TempDir(), Config: cfg, HTTP: srv.Client(), Now: time.Now}
	return env, f
}

func TestRadarrLibraryAndQueue(t *testing.T) {
	env, _ := newFakeRadarr(t)
	ctx := context.Background()
	lib, err := env.RadarrLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) != 2 || !lib[555].HasFile || lib[101].HasFile || lib[101].ID != 2 {
		t.Fatalf("library %+v", lib)
	}
	q, err := env.RadarrQueueFor(ctx, 2)
	if err != nil || q == nil || q.Percent() != 75 || shortTimeLeft(q.TimeLeft) != "1H 05M" {
		t.Fatalf("queue %+v %v", q, err)
	}
	if q, _ := env.RadarrQueueFor(ctx, 1); q != nil {
		t.Errorf("no queue expected")
	}
}

func TestRadarrTargetAndAdd(t *testing.T) {
	env, f := newFakeRadarr(t)
	ctx := context.Background()
	tg, err := env.RadarrTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Profile.Name != "Any" || tg.Folder.Path != "/movies" || !tg.Search {
		t.Errorf("default target %+v", tg)
	}
	env.Config.RadarrProfile = "hd-1080p"
	off := false
	env.Config.RadarrSearch = &off
	if tg, err = env.RadarrTarget(ctx); err != nil || tg.Profile.ID != 4 || tg.Search {
		t.Fatalf("named profile %+v %v", tg, err)
	}
	env.Config.RadarrProfile = "4K Remux"
	if _, err := env.RadarrTarget(ctx); err == nil || !strings.Contains(err.Error(), "HD-1080p") {
		t.Errorf("bad profile should list the real ones: %v", err)
	}
	env.Config.RadarrProfile = ""
	env.Config.RadarrRoot = "/nope"
	if _, err := env.RadarrTarget(ctx); err == nil || !strings.Contains(err.Error(), "/nope") {
		t.Errorf("bad root: %v", err)
	}
	env.Config.RadarrRoot = ""
	env.Config.RadarrSearch = nil
	tg, _ = env.RadarrTarget(ctx)

	r, err := env.RadarrAdd(ctx, 9001, tg)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 77 || r.TMDBID != 9001 || r.HasFile {
		t.Errorf("added %+v", r)
	}
	a := f.added
	if a["qualityProfileId"].(float64) != 1 || a["rootFolderPath"] != "/movies" || a["monitored"] != true || a["minimumAvailability"] != "released" {
		t.Errorf("post body %+v", a)
	}
	if opts := a["addOptions"].(map[string]any); opts["searchForMovie"] != true {
		t.Errorf("addOptions %+v", opts)
	}
	if _, ok := a["path"]; ok || a["titleSlug"] != "neon-harbor-9001" {
		t.Errorf("should pass the lookup through without a path: %+v", a)
	}
	// adding again is not an error
	if r, err := env.RadarrAdd(ctx, 9001, tg); err != nil || r.TMDBID != 9001 {
		t.Errorf("re-add %+v %v", r, err)
	}
	// already owned: the lookup says so, nothing is posted
	f.added = nil
	if r, err := env.RadarrAdd(ctx, 555, tg); err != nil || !r.HasFile || f.added != nil {
		t.Errorf("owned %+v %v", r, err)
	}
}

func TestRadarrErrors(t *testing.T) {
	env, _ := newFakeRadarr(t)
	env.Config.RadarrKey = "wrong"
	if _, err := env.RadarrLibrary(context.Background()); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("bad key: %v", err)
	}
	env.Config.RadarrKey = "rkey"
	env.Config.RadarrURL += "radarr" // wrong URL base
	if _, err := env.RadarrLibrary(context.Background()); err == nil || !strings.Contains(err.Error(), "URL base") {
		t.Errorf("wrong base: %v", err)
	}
	env.Config.RadarrURL = "http://127.0.0.1:1"
	if _, err := env.RadarrLibrary(context.Background()); err == nil || !strings.Contains(err.Error(), "Radarr running") {
		t.Errorf("refused: %v", err)
	}
}

// The UI marks owned movies, filters them, and adds one through the dialog.
func TestRadarrUIFlow(t *testing.T) {
	env, f := newFakeRadarr(t)
	u := NewUI(env, 640, 480)
	u.lib = &Library{Genres: map[int]string{}, Movies: []*Movie{
		{ID: 101, Title: "Alpha", RT: 90}, {ID: 555, Title: "Owned", RT: 80}, {ID: 9001, Title: "Neon Harbor", RT: 70},
	}}
	u.scr = scrList
	u.refilter(0)
	pump := func(until func() bool) {
		deadline := time.After(5 * time.Second)
		for !until() {
			select {
			case fn := <-u.post:
				fn()
			case <-deadline:
				t.Fatal("timed out")
			}
		}
	}
	u.radarrSync()
	pump(func() bool { return !u.rad.syncing })
	if st, _ := u.radarrStatus(u.lib.Movies[1]); !strings.Contains(st, "DOWNLOADED") {
		t.Errorf("owned status %q", st)
	}
	u.filter.Owned = 1
	u.refilter(0)
	if len(u.visible) != 1 || u.visible[0].ID != 9001 {
		t.Fatalf("hide owned: %d", len(u.visible))
	}
	u.filter.Owned = 2
	u.refilter(0)
	if len(u.visible) != 2 {
		t.Fatalf("only owned: %d", len(u.visible))
	}
	u.filter.Owned = 0
	u.refilter(0)

	u.openDetail(u.lib.Movies[0]) // Alpha: wanted, downloading
	pump(func() bool { return u.rad.queue[101] != nil })
	if st, _ := u.radarrStatus(u.cur); !strings.Contains(st, "DOWNLOADING 75%") {
		t.Errorf("downloading status %q", st)
	}
	u.Key(BtnB)
	u.sel = 2
	u.Key(BtnA)
	if u.cur.ID != 9001 {
		t.Fatalf("cur %d", u.cur.ID)
	}
	u.Key(BtnX)
	if u.modal != modalAdd {
		t.Fatal("X opens add")
	}
	pump(func() bool { return !u.rad.checking })
	c := NewCanvas(640, 480)
	u.Draw(c)
	u.Key(BtnA)
	pump(func() bool { return !u.rad.adding })
	if u.modal != modalNone || f.added == nil {
		t.Fatalf("add didn't go through: err %q", u.rad.addErr)
	}
	if _, ok := u.inRadarr(u.cur); !ok {
		t.Error("not marked after adding")
	}
	u.Key(BtnX) // already there now: just a toast
	if u.modal != modalNone || !strings.Contains(u.toast, "ALREADY") {
		t.Errorf("modal %v toast %q", u.modal, u.toast)
	}
}

func TestShortTimeLeft(t *testing.T) {
	for in, want := range map[string]string{"00:12:30": "12M", "1.02:03:04": "26H 03M", "00:00:20": "<1M", "": "", "junk": ""} {
		if got := shortTimeLeft(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}
