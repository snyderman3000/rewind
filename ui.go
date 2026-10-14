package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strings"
	"time"
)

type screen int

const (
	scrBoot screen = iota
	scrSetup
	scrList
	scrDetail
	scrTrailer
	scrKeyboard
	scrBye
)

type modalKind int

const (
	modalNone modalKind = iota
	modalMsg
	modalFilter
	modalAdd
)

const stillFrames = 70 // 3.5 s per still in the trailer reel

type UI struct {
	env  *Env
	W, H int
	s    float64

	fLogo, fBig, fBody, fSmall, fTiny, fLCD, fLCDBig, fOSD *Font

	scr     screen
	lib     *Library
	filter  Filter
	visible []*Movie
	sel     int
	scroll  int
	moved   time.Time // last list movement, to hold off poster downloads while scrolling

	refreshing bool
	prog       Progress
	bootErr    string
	cancel     context.CancelFunc

	imgs     map[string]*image.RGBA
	imgOrder []string
	imgBusy  map[string]bool
	imgErr   map[string]bool
	imgSlots chan struct{}

	cur        *Movie
	detailScrl int

	still      int
	stillStart int
	paused     bool
	playFrames int

	modal     modalKind
	msgTitle  string
	msgBody   []string
	msgCol    color.RGBA
	filterRow int

	toast      string
	toastUntil time.Time
	omdbHinted bool

	rad radarrUI

	kb     keyboardState
	search searchState

	frame int
	quit  bool
	post  chan func()
}

func NewUI(env *Env, w, h int) *UI {
	s := float64(h) / 480
	if sw := float64(w) / 640; sw < s {
		s = sw
	}
	u := &UI{env: env, W: w, H: h, s: s, post: make(chan func(), 128),
		imgs: map[string]*image.RGBA{}, imgBusy: map[string]bool{}, imgErr: map[string]bool{},
		imgSlots: make(chan struct{}, 2), rad: radarrUI{queue: map[int]*RadarrQueue{}, queueAt: map[int]time.Time{}}, lib: &Library{Genres: map[int]string{}, Source: "empty"}}
	u.fLogo = loadFont(titleTTF, 23*s)
	u.fBig = loadFont(titleTTF, 19*s)
	u.fBody = loadFont(bodyTTF, 18*s)
	u.fSmall = loadFont(bodyTTF, 15*s)
	u.fTiny = loadFont(bodyTTF, 13*s)
	u.fLCD = loadFont(lcdTTF, 24*s)
	u.fLCDBig = loadFont(lcdTTF, 40*s)
	u.fOSD = loadFont(lcdTTF, 30*s)
	return u
}

func (u *UI) px(v int) int { return int(math.Round(float64(v) * u.s)) }

// ---------- lifecycle ----------

func (u *UI) Start() {
	defer func() {
		if p := u.env.Config.Problem; p != "" {
			u.message("CONFIG ERROR", colMagenta, p)
		}
	}()
	u.filter = u.env.LoadFilter()
	u.lib = u.env.LoadLibrary()
	u.env.PruneImageCache(60 << 20)
	u.radarrSync()
	if u.env.Config.TMDBKey == "" {
		u.scr = scrSetup
		if len(u.lib.Movies) > 0 {
			u.scr = scrList
			u.refilter(0)
			u.showToast("NO TMDB KEY — SHOWING THE SAVED GUIDE")
		}
		return
	}
	if len(u.lib.Movies) > 0 {
		u.scr = scrList
		u.refilter(0)
		if u.env.NeedsRefresh(u.lib) {
			u.refresh()
		}
	} else {
		u.scr = scrBoot
		u.refresh()
	}
	u.hintOMDb()
}

func (u *UI) hintOMDb() {
	if u.env.Config.OMDbKey == "" && !u.omdbHinted && u.scr == scrList {
		u.omdbHinted = true
		u.showToast("ADD AN OMDB KEY TO SEE ROTTEN TOMATOES SCORES")
	}
}

func (u *UI) saveState() { u.env.SaveFilter(u.filter) }

// refresh rebuilds the guide in the background.
func (u *UI) refresh() {
	if u.refreshing {
		return
	}
	if u.env.Config.TMDBKey == "" {
		u.scr = scrSetup
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.refreshing = true
	u.bootErr = ""
	u.prog = Progress{Phase: "CONNECTING"}
	old := u.lib
	go func() {
		lib, warn, err := u.env.Refresh(ctx, old, func(p Progress) {
			select {
			case u.post <- func() { u.prog = p }:
			default:
			}
		})
		u.post <- func() {
			cancel()
			u.refreshing = false
			if err != nil {
				log.Printf("refresh failed: %v", err)
				msg := netHint(err).Error()
				if u.scr == scrBoot {
					u.bootErr = msg
					return
				}
				if len(u.lib.Movies) > 0 {
					u.showToast(strings.ToUpper(shortErr(msg)) + " — SHOWING SAVED GUIDE")
				} else {
					u.message("TRACKING ERROR", colMagenta, msg)
				}
				return
			}
			var keep int
			if m := u.selected(); m != nil {
				keep = m.ID
			}
			u.lib = lib
			if u.scr == scrBoot {
				u.scr = scrList
			}
			if u.cur != nil {
				for _, m := range lib.Movies {
					if m.ID == u.cur.ID {
						u.cur = m
					}
				}
			}
			u.refilter(keep)
			switch {
			case warn != "":
				u.showToast(strings.ToUpper(shortErr(warn)))
			case len(lib.Movies) == 0:
				u.message("BLANK TAPE", colAmber, "TMDB didn't list any new digital releases on streaming services for your region. Try a bigger \"days\" value in App/Rewind/data/config.json.")
			default:
				u.showToast(fmt.Sprintf("GUIDE UPDATED · %d TAPES", len(lib.Movies)))
				u.hintOMDb()
				u.radarrSync()
			}
		}
	}()
}

func shortErr(s string) string {
	if i := strings.Index(s, " — check"); i > 0 {
		return s[:i]
	}
	return s
}

// refilter recomputes the visible list, keeping the movie with id keep selected if possible.
func (u *UI) refilter(keep int) {
	if u.search.on {
		u.visible = u.search.results // search results are shown as found, unfiltered
	} else {
		u.visible = u.filter.Apply(u.lib.Movies, u.ownedFn())
	}
	u.sel = 0
	for i, m := range u.visible {
		if m.ID == keep {
			u.sel = i
		}
	}
	u.moved = time.Now()
}

func (u *UI) selected() *Movie {
	if u.sel < 0 || u.sel >= len(u.visible) {
		return nil
	}
	return u.visible[u.sel]
}

func (u *UI) showToast(s string) {
	u.toast = s
	u.toastUntil = time.Now().Add(3500 * time.Millisecond)
}

func (u *UI) message(title string, col color.RGBA, body string) {
	u.modal = modalMsg
	u.msgTitle = title
	u.msgCol = col
	u.msgBody = strings.Split(body, "\n")
}

// Tick runs every frame: debounced poster downloads and the trailer reel clock.
func (u *UI) Tick() {
	switch u.scr {
	case scrList:
		if time.Since(u.moved) > 180*time.Millisecond {
			for d := 0; d <= 3; d++ {
				if i := u.sel + d; i < len(u.visible) {
					u.listPoster(u.visible[i], true)
				}
				if d > 0 && d < 2 && u.sel-d >= 0 {
					u.listPoster(u.visible[u.sel-d], true)
				}
			}
		}
	case scrTrailer:
		if !u.paused {
			u.playFrames++
			if n := len(u.cur.Stills); n > 1 && u.frame-u.stillStart >= stillFrames {
				u.nextStill(1)
			}
		}
	}
}

func (u *UI) Animating() bool {
	toast := u.toast != "" && time.Now().Before(u.toastUntil.Add(100*time.Millisecond))
	switch {
	case u.scr == scrBoot, u.scr == scrBye, toast:
		return true
	case u.scr == scrTrailer:
		return !u.paused || u.frame-u.stillStart < 10 || u.anyBusy()
	case (u.rad.adding || u.rad.checking) && u.modal == modalAdd:
		return true
	case u.refreshing && u.frame%5 == 0:
		return true
	case u.anyBusy() && u.frame%2 == 0:
		return true
	}
	return false
}

func (u *UI) anyBusy() bool {
	for _, b := range u.imgBusy {
		if b {
			return true
		}
	}
	return false
}

// ---------- images ----------

func (u *UI) imgKey(url string, w, h int, cover bool) string {
	return fmt.Sprintf("%s|%dx%d|%v", url, w, h, cover)
}

// image returns a cached picture, or starts fetching it when fetch is true.
func (u *UI) image(url string, w, h int, cover, fetch bool) (*image.RGBA, bool) {
	if url == "" {
		return nil, true
	}
	k := u.imgKey(url, w, h, cover)
	if img := u.imgs[k]; img != nil {
		return img, false
	}
	if u.imgErr[k] {
		return nil, true
	}
	if !fetch || u.imgBusy[k] {
		return nil, false
	}
	u.imgBusy[k] = true
	go func() {
		u.imgSlots <- struct{}{}
		img, err := u.env.Image(url, w, h, cover)
		<-u.imgSlots
		u.post <- func() {
			delete(u.imgBusy, k)
			if err != nil {
				u.imgErr[k] = true
				return
			}
			u.imgs[k] = img
			u.imgOrder = append(u.imgOrder, k)
			for len(u.imgOrder) > 50 { // keep memory in check on a 128 MB handheld
				delete(u.imgs, u.imgOrder[0])
				u.imgOrder = u.imgOrder[1:]
			}
		}
	}()
	return nil, false
}

func (u *UI) listPoster(m *Movie, fetch bool) (*image.RGBA, bool) {
	w, h := u.listPosterBox()
	return u.image(u.env.ImageURL("w185", m.Poster), w, h, true, fetch)
}

func (u *UI) stillImage(path string, fetch bool) (*image.RGBA, bool) {
	w, h := u.tvBox()
	return u.image(u.env.ImageURL("w780", path), w*11/10, h*11/10, true, fetch)
}

// ---------- input ----------

func (u *UI) Key(k int) {
	if k == BtnMenu {
		u.quit = true
		return
	}
	switch u.modal {
	case modalMsg:
		if k == BtnA || k == BtnB || k == BtnStart {
			u.modal = modalNone
		}
		return
	case modalFilter:
		u.keyFilter(k)
		return
	case modalAdd:
		u.keyAdd(k)
		return
	}
	switch u.scr {
	case scrBoot:
		switch k {
		case BtnA:
			if u.bootErr != "" {
				u.refresh()
			}
		case BtnB:
			u.quit = true
		}
	case scrSetup:
		switch k {
		case BtnA, BtnStart:
			u.env.Config = loadConfig(u.env.DataDir + "/config.json")
			if p := u.env.Config.Problem; p != "" {
				u.message("CONFIG ERROR", colMagenta, p)
				return
			}
			if u.env.Config.TMDBKey == "" {
				u.showToast("STILL NO TMDB KEY IN CONFIG.JSON")
				return
			}
			u.scr = scrBoot
			u.refresh()
		case BtnB:
			u.quit = true
		}
	case scrList:
		u.keyList(k)
	case scrDetail:
		u.keyDetail(k)
	case scrTrailer:
		u.keyTrailer(k)
	case scrKeyboard:
		u.keyKeyboard(k)
	}
}

func (u *UI) keyList(k int) {
	if u.search.on && u.keySearchList(k) {
		return
	}
	n := len(u.visible)
	move := func(to int) {
		if n == 0 {
			return
		}
		u.sel = to
		u.moved = time.Now()
	}
	switch k {
	case BtnUp:
		move((u.sel - 1 + n) % max(1, n))
	case BtnDown:
		move((u.sel + 1) % max(1, n))
	case BtnLeft:
		move(max(0, u.sel-u.rowsPerPage()))
	case BtnRight:
		move(min(n-1, u.sel+u.rowsPerPage()))
	case BtnL1, BtnR1:
		u.cycleFacet(&u.filter.Genre, Facets(u.lib.Movies, genresOf), k == BtnR1)
		u.showToast("GENRE: " + strings.ToUpper(orAll(u.filter.Genre, "ALL GENRES")))
	case BtnL2, BtnR2:
		u.cycleFacet(&u.filter.Service, Facets(u.lib.Movies, servicesOf), k == BtnR2)
		u.showToast("SERVICE: " + strings.ToUpper(orAll(u.filter.Service, "ALL SERVICES")))
	case BtnY:
		u.filter.Sort = (u.filter.Sort + 1) % numSorts
		u.refilter(0)
		u.showToast("SORTED BY " + sortNames[u.filter.Sort])
	case BtnStart:
		u.modal = modalFilter
	case BtnX:
		if m := u.selected(); m != nil {
			u.cur = m
			u.openAdd(m)
		}
	case BtnSelect:
		u.openKeyboard()
	case BtnA:
		if m := u.selected(); m != nil {
			u.openDetail(m)
		}
	case BtnB:
		u.quit = true
	}
}

func orAll(s, all string) string {
	if s == "" {
		return all
	}
	return s
}

// cycleFacet steps a genre/service filter through "all" and every value present.
func (u *UI) cycleFacet(v *string, facets []string, fwd bool) {
	opts := append([]string{""}, facets...)
	i := 0
	for j, o := range opts {
		if o == *v {
			i = j
		}
	}
	if fwd {
		i = (i + 1) % len(opts)
	} else {
		i = (i - 1 + len(opts)) % len(opts)
	}
	*v = opts[i]
	keep := 0
	if m := u.selected(); m != nil {
		keep = m.ID
	}
	u.refilter(keep)
}

func (u *UI) openDetail(m *Movie) {
	u.cur = m
	u.scr = scrDetail
	u.detailScrl = 0
	w, h := u.detailPosterBox()
	u.image(u.env.ImageURL("w342", m.Poster), w, h, true, true)
	u.fetchQueue(m)
}

func (u *UI) stepMovie(d int) {
	n := len(u.visible)
	if n == 0 {
		return
	}
	u.sel = (u.sel + d + n) % n
	m := u.visible[u.sel]
	if u.scr == scrTrailer {
		u.openTrailer(m)
	} else {
		u.openDetail(m)
	}
}

func (u *UI) keyDetail(k int) {
	switch k {
	case BtnB:
		u.scr = scrList
		u.moved = time.Now()
	case BtnA, BtnStart:
		u.openTrailer(u.cur)
	case BtnX:
		u.openAdd(u.cur)
	case BtnY:
		u.radarrSync()
		u.rad.queueAt[u.cur.ID] = time.Time{}
		u.fetchQueue(u.cur)
	case BtnUp:
		if u.detailScrl > 0 {
			u.detailScrl--
		}
	case BtnDown:
		u.detailScrl++
	case BtnL1, BtnLeft:
		u.stepMovie(-1)
	case BtnR1, BtnRight:
		u.stepMovie(1)
	}
}

func (u *UI) openTrailer(m *Movie) {
	u.cur = m
	u.scr = scrTrailer
	u.still = 0
	u.stillStart = u.frame
	u.paused = false
	u.playFrames = 0
	for i, s := range m.Stills {
		u.stillImage(s, i < 3) // first few now, the rest as the reel plays
	}
}

func (u *UI) nextStill(d int) {
	n := len(u.cur.Stills)
	if n == 0 {
		return
	}
	u.still = (u.still + d + n) % n
	u.stillStart = u.frame
	u.stillImage(u.cur.Stills[(u.still+1)%n], true)
	u.stillImage(u.cur.Stills[(u.still+2)%n], true)
}

func (u *UI) keyTrailer(k int) {
	switch k {
	case BtnB:
		u.scr = scrDetail
	case BtnA, BtnStart:
		u.paused = !u.paused
		if !u.paused {
			u.stillStart = u.frame
		}
	case BtnX:
		u.openAdd(u.cur)
	case BtnLeft:
		u.nextStill(-1)
	case BtnRight:
		u.nextStill(1)
	case BtnL1:
		u.stepMovie(-1)
	case BtnR1:
		u.stepMovie(1)
	}
}

// filter panel rows
const (
	fRowSort = iota
	fRowGenre
	fRowService
	fRowRT
	fRowCert
	fRowRuntime
	fRowUnscored
	fRowOwned
	fRowReset
	fRowRefresh
	fRows
)

func (u *UI) keyFilter(k int) {
	switch k {
	case BtnB, BtnX, BtnStart:
		u.modal = modalNone
		u.saveState()
		return
	case BtnUp:
		u.filterRow = (u.filterRow - 1 + fRows) % fRows
		return
	case BtnDown:
		u.filterRow = (u.filterRow + 1) % fRows
		return
	}
	d := 0
	switch k {
	case BtnLeft:
		d = -1
	case BtnRight, BtnA:
		d = 1
	default:
		return
	}
	step := func(v, n int) int { return (v + d + n) % n }
	f := &u.filter
	switch u.filterRow {
	case fRowSort:
		f.Sort = SortKey(step(int(f.Sort), int(numSorts)))
	case fRowGenre:
		u.cycleFacet(&f.Genre, Facets(u.lib.Movies, genresOf), d > 0)
	case fRowService:
		u.cycleFacet(&f.Service, Facets(u.lib.Movies, servicesOf), d > 0)
	case fRowRT:
		f.MinRT = step(f.MinRT, len(minRTSteps))
	case fRowCert:
		f.Cert = step(f.Cert, len(certSteps))
	case fRowRuntime:
		f.Runtime = step(f.Runtime, len(runtimeSteps))
	case fRowUnscored:
		f.Hide = !f.Hide
	case fRowOwned:
		if !u.env.RadarrOn() {
			return
		}
		f.Owned = step(f.Owned, len(ownedSteps))
	case fRowReset:
		if k == BtnA {
			*f = Filter{Sort: f.Sort}
		}
	case fRowRefresh:
		if k == BtnA {
			u.modal = modalNone
			u.saveState()
			if u.refreshing {
				u.showToast("ALREADY REWINDING…")
			} else {
				u.refresh()
			}
		}
		return
	}
	keep := 0
	if m := u.selected(); m != nil {
		keep = m.ID
	}
	u.refilter(keep)
}

func (u *UI) filterValue(row int) string {
	f := u.filter
	switch row {
	case fRowSort:
		return sortNames[f.Sort]
	case fRowGenre:
		if f.Genre == "" {
			return "ALL GENRES"
		}
		return strings.ToUpper(f.Genre)
	case fRowService:
		if f.Service == "" {
			return "ALL SERVICES"
		}
		return strings.ToUpper(f.Service)
	case fRowRT:
		switch f.MinRT {
		case 0:
			return "ANY SCORE"
		case 1:
			return "60%+  FRESH"
		}
		return fmt.Sprintf("%d%%+", minRTSteps[f.MinRT])
	case fRowCert:
		return certSteps[f.Cert]
	case fRowRuntime:
		return runtimeSteps[f.Runtime]
	case fRowOwned:
		if !u.env.RadarrOn() {
			return "SET UP RADARR FIRST"
		}
		return ownedSteps[f.Owned]
	case fRowUnscored:
		if f.Hide {
			return "HIDE"
		}
		return "SHOW"
	}
	if row == fRowRefresh {
		if u.refreshing {
			return "REWINDING…"
		}
		if !u.lib.Updated.IsZero() {
			return "PRESS A · UPDATED " + strings.ToUpper(u.lib.Updated.Local().Format("Jan 2 3:04PM"))
		}
	}
	return "PRESS A"
}

var filterLabels = []string{"SORT BY", "GENRE", "SERVICE", "TOMATOMETER", "RATED", "RUNTIME", "NO RT SCORE", "MY LIBRARY", "RESET FILTERS", "REFRESH GUIDE"}

// ---------- device status ----------

func wifiUp() bool {
	b, err := os.ReadFile("/sys/class/net/wlan0/operstate")
	return err == nil && strings.TrimSpace(string(b)) == "up"
}

func battery() int {
	b, err := os.ReadFile("/tmp/percBat")
	if err != nil {
		return -1
	}
	var v int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &v); err != nil || v < 0 || v > 100 {
		return -1
	}
	return v
}
