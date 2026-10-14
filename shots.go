package main

// Desktop-only demo scenes for screenshots (-shot). The movies here are
// invented, with made-up scores and generated artwork, so the screenshots
// never misstate a real film's numbers.

import (
	"image"
	"image/color"
	"math"
	"time"
)

type demoFilm struct {
	title, cert, tagline string
	year, rt, meta, run  int
	imdb, pop            float64
	genres, svc          []string
	hue                  float64
}

var demoFilms = []demoFilm{
	{"Neon Harbor", "PG-13", "The tide always brings back what you sent away.", 2026, 94, 81, 118, 7.8, 90, []string{"Thriller", "Mystery"}, []string{"Netflix"}, 0.55},
	{"The Last Arcade", "PG", "One quarter. One life. One high score.", 2026, 88, 74, 101, 7.4, 82, []string{"Comedy", "Family"}, []string{"Disney+"}, 0.85},
	{"Static Bloom", "R", "", 2026, 71, 63, 124, 6.9, 75, []string{"Science Fiction", "Drama"}, []string{"Max"}, 0.35},
	{"Paper Comets", "PG", "Every kid needs a launch window.", 2026, 97, 89, 96, 8.1, 60, []string{"Animation", "Family", "Adventure"}, []string{"Prime Video", "Hulu"}, 0.12},
	{"Midnight Freight", "R", "The 2:15 doesn't stop for anyone.", 2026, 64, 58, 109, 6.6, 70, []string{"Action", "Thriller"}, []string{"Peacock"}, 0.02},
	{"Grand Delta Motel", "PG-13", "", 2025, 42, 47, 112, 5.9, 55, []string{"Comedy"}, []string{"Paramount+"}, 0.92},
	{"A Quiet Frequency", "PG-13", "Somebody is still broadcasting.", 2026, 91, 84, 131, 7.6, 66, []string{"Drama", "Science Fiction"}, []string{"Apple TV+"}, 0.62},
	{"Ironwood", "R", "", 2026, 33, 39, 104, 5.2, 48, []string{"Horror"}, []string{"Netflix"}, 0.28},
	{"Sunday at the Drive-In", "G", "", 2026, 82, 70, 88, 7.1, 40, []string{"Family", "Comedy"}, []string{"Disney+"}, 0.08},
	{"Cold Open", "R", "Every show has to start somewhere.", 2025, 76, 68, 99, 6.8, 44, []string{"Comedy", "Drama"}, []string{"Hulu"}, 0.72},
	{"The Cartographer", "PG-13", "", 2026, -1, -1, 141, 0, 38, []string{"Adventure", "History"}, []string{"Prime Video"}, 0.45},
	{"Velvet Signal", "R", "", 2026, 58, 55, 115, 6.3, 35, []string{"Crime", "Thriller"}, []string{"Max"}, 0.78},
	{"Ghost Lights of Route 9", "PG-13", "", 2026, 69, 61, 97, 6.5, 30, []string{"Horror", "Mystery"}, []string{"Peacock", "Netflix"}, 0.18},
	{"Lunar Paperboy", "PG", "", 2026, 85, 72, 92, 7.2, 25, []string{"Animation", "Science Fiction"}, []string{"Netflix"}, 0.66},
}

func demoLibrary() *Library {
	lib := &Library{Genres: map[int]string{}, Source: "cache", Region: "US", Days: 120,
		Updated: time.Date(2026, 10, 1, 21, 40, 0, 0, time.Local)}
	names := []string{"Jordan Vale", "Marisol Quint", "Theo Barrow", "Ada Okafor", "Wes Calder", "Nina Park", "Ruben Holt", "Ivy Marchetti"}
	for i, f := range demoFilms {
		m := &Movie{ID: 9000 + i, Title: f.title, Year: f.year, Cert: f.cert, Tagline: f.tagline,
			RT: f.rt, Meta: f.meta, IMDb: f.imdb, Runtime: f.run, Popularity: f.pop, Genres: f.genres,
			VoteAvg: f.imdb - 0.2, VoteCount: 300, Poster: "/demo" + string(rune('a'+i)) + ".jpg",
			Released:  time.Date(2026, 9, 28-i*2, 0, 0, 0, 0, time.UTC),
			Directors: []string{names[(i+3)%len(names)]},
			Writers:   []string{names[(i+5)%len(names)]},
			DetailAt:  time.Now(), IMDbID: "tt0000000"}
		m.Overview = "When " + names[i%len(names)] + " finds an old tape in a thrift-store VCR, a quiet weekend turns into a chase across three states — and a reckoning with the friends who recorded it twenty years ago."
		for j := 0; j < 6; j++ {
			n := names[(i+j)%len(names)]
			m.Cast = append(m.Cast, CastMember{Name: n, Role: []string{"Sam", "Rae", "Detective Lowe", "Grandma June", "The Projectionist", "Kid"}[j]})
		}
		for k, s := range f.svc {
			m.Providers = append(m.Providers, Provider{ID: 100 + k, Name: s})
		}
		if f.title == "Midnight Freight" || f.title == "Grand Delta Motel" { // new to rent, not streaming yet
			m.Providers = nil
			m.Stores = []Provider{{ID: 2, Name: "Apple TV"}, {ID: 10, Name: "Amazon Video"}, {ID: 3, Name: "Google Play"}}
		}
		for k := 0; k < 5; k++ {
			m.Stills = append(m.Stills, "/still"+string(rune('a'+i))+string(rune('0'+k))+".jpg")
		}
		if i != 5 {
			m.Trailer = &Video{Key: "dQw4w9WgXcQ", Name: "Official Trailer", Type: "Trailer"}
		}
		if i == 0 {
			m.Awards = "Won 2 festival awards"
		}
		lib.Movies = append(lib.Movies, m)
	}
	return lib
}

// demoArt paints a poster or still: a sunset gradient, sun, grid and title.
func demoArt(u *UI, title string, hue float64, w, h int, still int) *image.RGBA {
	c := NewCanvas(w, h)
	top := hsv(hue, 0.75, 0.22)
	mid := hsv(math.Mod(hue+0.08, 1), 0.85, 0.85)
	horizon := h * 58 / 100
	if still >= 0 {
		horizon = h*(45+still*6)/100 + 0
	}
	for y := 0; y < h; y++ {
		col := top
		if y < horizon {
			col = mix(top, mid, math.Pow(float64(y)/float64(horizon), 1.6))
		} else {
			col = mix(rgb(0x14, 0x06, 0x22), rgb(0x05, 0x02, 0x0a), float64(y-horizon)/float64(h-horizon))
		}
		c.Fill(0, y, w, 1, col)
	}
	// sun with slats
	sr := w / 4
	if still >= 0 {
		sr = h / 4
	}
	scx := w / 2
	if still >= 0 {
		scx = w * (30 + still*12) / 100
	}
	for y := -sr; y <= 0; y++ {
		if (y/max(1, sr/8))%2 == 0 && y < -sr/3 || y >= -sr/3 && ((-y)/3)%2 == 0 {
			hw := int(math.Sqrt(float64(sr*sr - y*y)))
			c.Fill(scx-hw, horizon+y, 2*hw, 1, mix(rgb(0xff, 0xd0, 0x40), rgb(0xff, 0x40, 0x80), float64(-y)/float64(sr)))
		}
	}
	// perspective grid
	gc := hsv(math.Mod(hue+0.5, 1), 0.7, 0.9)
	for i := -10; i <= 10; i++ {
		c.Line(w/2, horizon, w/2+i*w/5, h, mix(rgb(0x14, 0x06, 0x22), gc, 0.6))
	}
	for k := 1; k < 8; k++ {
		y := horizon + int(float64(h-horizon)*math.Pow(float64(k)/8, 2))
		c.Fill(0, y, w, 1, mix(rgb(0x14, 0x06, 0x22), gc, 0.5))
	}
	if still < 0 {
		f := loadFont(titleTTF, float64(w)/9)
		y := h / 10
		for _, l := range f.Wrap(title, w-12) {
			c.TextCenter(f, w/2+1, y+1, l, rgb(0, 0, 0))
			c.TextCenter(f, w/2, y, l, rgb(0xff, 0xf2, 0xd8))
			y += f.height
		}
		sf := loadFont(bodyTTF, float64(w)/14)
		c.TextCenter(sf, w/2, h-sf.height-6, "COMING HOME", rgb(0xff, 0xf2, 0xd8))
	}
	return c.RGBA
}

func hsv(h, s, v float64) color.RGBA {
	i := math.Floor(h * 6)
	f := h*6 - i
	p, q, t := v*(1-s), v*(1-f*s), v*(1-(1-f)*s)
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return rgb(uint8(r*255), uint8(g*255), uint8(b*255))
}

func demoScene(u *UI, scene string) {
	u.env.Config.RadarrURL, u.env.Config.RadarrKey = "http://192.168.1.20:7878", "demo"
	u.lib = demoLibrary()
	u.frame = 3
	u.scr = scrList
	u.refilter(0)
	put := func(m *Movie, i int) {
		f := demoFilms[i]
		w, h := u.listPosterBox()
		u.imgs[u.imgKey(u.env.ImageURL("w185", m.Poster), w, h, true)] = demoArt(u, f.title, f.hue, w, h, -1)
		w, h = u.detailPosterBox()
		u.imgs[u.imgKey(u.env.ImageURL("w342", m.Poster), w, h, true)] = demoArt(u, f.title, f.hue, w, h, -1)
		tw, th := u.tvBox()
		for k, s := range m.Stills {
			u.imgs[u.imgKey(u.env.ImageURL("w780", s), tw*11/10, th*11/10, true)] = demoArt(u, "", math.Mod(f.hue+float64(k)*0.07, 1), tw*11/10, th*11/10, k)
		}
	}
	for i, m := range u.lib.Movies {
		if i < 3 || i == 6 || i == 3 {
			put(m, i)
		}
	}
	pick := func(title string) *Movie {
		for i, m := range u.visible {
			if m.Title == title {
				u.sel = i
				return m
			}
		}
		return u.visible[0]
	}
	if u.env.RadarrOn() {
		u.rad.lib = map[int]RadarrMovie{}
		for _, m := range u.lib.Movies {
			switch m.Title {
			case "Paper Comets", "Cold Open":
				u.rad.lib[m.ID] = RadarrMovie{ID: m.ID, TMDBID: m.ID, HasFile: true, Monitored: true}
			case "A Quiet Frequency":
				u.rad.lib[m.ID] = RadarrMovie{ID: m.ID, TMDBID: m.ID, Monitored: true}
				u.rad.queue[m.ID] = &RadarrQueue{Size: 4e9, SizeLeft: 1.5e9, TimeLeft: "00:12:30", State: "downloading"}
			}
		}
		u.rad.target = &RadarrTarget{Profile: RadarrProfile{ID: 4, Name: "HD-1080p"}, Folder: RadarrFolder{Path: "/movies", FreeSpace: 1_300_000_000_000}, Search: true}
	}
	switch scene {
	case "add":
		u.openDetail(pick("Neon Harbor"))
		u.modal = modalAdd
	case "detail-dl":
		u.openDetail(pick("A Quiet Frequency"))
	case "boot":
		u.scr = scrBoot
		u.frame = 11
		u.prog = Progress{Phase: "COUNTING TOMATOES", Done: 37, Total: 120}
	case "list":
		pick("Neon Harbor")
	case "rental":
		pick("Midnight Freight")
	case "loading":
		u.sel = 4
	case "filter":
		u.filter.Genre = "Family"
		u.filter.Cert = 3
		u.refilter(0)
		u.modal = modalFilter
		u.filterRow = fRowCert
	case "family":
		u.filter.Genre = "Family"
		u.filter.Sort = SortRT
		u.refilter(0)
	case "detail":
		u.openDetail(pick("Neon Harbor"))
	case "trailer":
		u.openTrailer(pick("A Quiet Frequency"))
		u.playFrames = 251
		u.stillStart = u.frame - 30
		u.still = 1
	case "trailer-cut":
		u.openTrailer(pick("Neon Harbor"))
		u.stillStart = u.frame - 4
		u.playFrames = 75
		u.still = 1
	case "keyboard", "search":
		u.search.recent = []string{"night shift", "the thing", "back to the future"}
		u.search.loaded = true
		if scene == "keyboard" {
			u.openKeyboard()
			u.kb.text = "NIGHT SH"
			u.kb.row, u.kb.col = 1, 7
			return
		}
		olds := []struct {
			title, cert string
			year, rt    int
			svc         string
			hue         float64
		}{{"Night Shift", "R", 1982, 63, "", 0.6}, {"Night Shift Diner", "PG-13", 1989, 78, "Tubi", 0.08},
			{"The Night Shift Club", "R", 2004, 41, "", 0.3}, {"Night Shift: A Hospital Story", "TV-14", 2011, -1, "Netflix", 0.75}}
		for i, o := range olds {
			m := *u.lib.Movies[i]
			m.ID, m.Title, m.Year, m.Cert, m.RT, m.Providers = 7000+i, o.title, o.year, o.cert, o.rt, nil
			m.Released = time.Date(o.year, 3, 1, 0, 0, 0, 0, time.UTC)
			m.Poster = "/night" + string(rune('a'+i)) + ".jpg"
			if o.svc != "" {
				m.Providers = []Provider{{ID: 1, Name: o.svc}}
			}
			w, h := u.listPosterBox()
			u.imgs[u.imgKey(u.env.ImageURL("w185", m.Poster), w, h, true)] = demoArt(u, o.title, o.hue, w, h, -1)
			u.search.results = append(u.search.results, &m)
		}
		u.rad.lib[7000] = RadarrMovie{ID: 7000, TMDBID: 7000, HasFile: true, Monitored: true}
		u.search.on, u.search.query = true, "night shift"
		u.refilter(0)
		u.sel = 0
	case "setup":
		u.scr = scrSetup
	case "bye":
		u.scr = scrBye
		u.frame = 7
	case "error":
		u.scr = scrBoot
		u.bootErr = "TMDB rejected the key — check tmdb_api_key in App/Rewind/data/config.json"
	}
}

// appIcon: the BE KIND, REWIND tape on a transparent background.
func appIcon(n int) *image.RGBA {
	c := NewCanvas(n, n)
	for i := range c.Pix {
		c.Pix[i] = 0
	}
	h := n * 62 / 100
	y := (n - h) / 2
	c.VHSTape(1, y, n-2, h, "", nil, 0.3, 0.45, colLabelY)
	// the label's two lines, hand-lettered small
	lx, ly, lw, lh := 1+(n-2)*8/100, y+h*9/100, (n-2)*84/100, h*30/100
	size := float64(n) * 0.12
	f := loadFont(bodyTTF, size)
	for f.Width("BE KIND, REWIND") > lw-3 && size > 4 {
		size -= 0.25
		f = loadFont(bodyTTF, size)
	}
	c.Text(f, lx+(lw-f.Width("BE KIND, REWIND"))/2, ly+(lh-f.height)/2, "BE KIND, REWIND", rgb(0x1a, 0x1a, 0x30))
	return c.RGBA
}
