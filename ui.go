package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
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
	u.visible = u.filter.Apply(u.lib.Movies, u.ownedFn())
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

func (u *UI) listPosterBox() (int, int)   { return u.px(112), u.px(168) }
func (u *UI) detailPosterBox() (int, int) { return u.px(164), u.px(246) }
func (u *UI) tvBox() (int, int)           { return u.px(400), u.px(225) }

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
	}
}

func (u *UI) rowH() int        { return u.px(30) }
func (u *UI) listTop() int     { return u.headerH() + u.px(46) }
func (u *UI) rowsPerPage() int { return max(1, (u.H-u.px(32)-u.listTop()-u.px(6))/u.rowH()) }

func (u *UI) keyList(k int) {
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
	case BtnX, BtnStart:
		u.modal = modalFilter
	case BtnSelect:
		if u.refreshing {
			u.showToast("ALREADY REWINDING…")
		} else {
			u.refresh()
		}
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
	return "PRESS A"
}

var filterLabels = []string{"SORT BY", "GENRE", "SERVICE", "TOMATOMETER", "RATED", "RUNTIME", "NO RT SCORE", "MY LIBRARY", "RESET FILTERS"}

// ---------- drawing ----------

func (u *UI) Draw(c *Canvas) {
	c.Fill(0, 0, c.W, c.H, colBG)
	switch u.scr {
	case scrBoot:
		u.drawBoot(c)
	case scrSetup:
		u.drawHeader(c)
		u.drawSetup(c)
	case scrList:
		u.drawHeader(c)
		u.drawList(c)
	case scrDetail:
		u.drawHeader(c)
		u.drawDetail(c)
	case scrTrailer:
		u.drawHeader(c)
		u.drawTrailer(c)
	case scrBye:
		u.drawBye(c)
	}
	switch u.modal {
	case modalMsg:
		u.drawMsg(c)
	case modalFilter:
		u.drawFilter(c)
	case modalAdd:
		u.drawAdd(c)
	}
	if u.toast != "" && time.Now().Before(u.toastUntil) && u.scr != scrBye {
		t := u.fSmall.Ellipsize(u.toast, c.W-u.px(60))
		w := u.fSmall.Width(t) + u.px(24)
		h := u.fSmall.height + u.px(10)
		x, y := (c.W-w)/2, c.H-u.px(34)-h-u.px(8)
		c.Fill(x, y, w, h, colPanel2)
		c.Border(x, y, w, h, 1, colCyan)
		c.Text(u.fSmall, x+u.px(12), y+u.px(5), t, colCyan)
	}
	c.Scanlines(0, 0, c.W, c.H, 0.12)
}

func (u *UI) angle() float64 { return float64(u.frame) * 0.35 }

func (u *UI) drawStripes(c *Canvas, y int) {
	t := u.px(2)
	c.Fill(0, y, c.W, t, colAmber)
	c.Fill(0, y+t, c.W, t, colMagenta)
	c.Fill(0, y+2*t, c.W, t, colCyan)
}

func (u *UI) headerH() int { return u.px(44) }

func (u *UI) drawHeader(c *Canvas) {
	h := u.headerH()
	c.Fill(0, 0, c.W, h, colPanel)
	x := c.TextGlow(u.fLogo, u.px(14), u.px(9), "REWIND", colAmber)
	c.Text(u.fTiny, x+u.px(10), u.px(19), "// NEW ON STREAMING", colCyan)
	right := c.W - u.px(14)
	clock := time.Now().Format("15:04")
	cw := u.fLCD.Width(clock)
	c.TextGlow(u.fLCD, right-cw, u.px(9), clock, colAmber)
	right -= cw + u.px(14)
	up := wifiUp()
	for i := 0; i < 3; i++ {
		bh := u.px(6 + i*5)
		col := colCyan
		if !up {
			col = colGrid
		}
		c.Fill(right-u.px(22)+i*u.px(8), u.px(30)-bh, u.px(5), bh, col)
	}
	right -= u.px(34)
	if b := battery(); b >= 0 {
		s := fmt.Sprintf("%d%%", b)
		bw := u.px(30)
		bx := right - bw
		col := colPaperDim
		if b <= 15 {
			col = colRed
		}
		c.Border(bx, u.px(15), bw, u.px(14), 1, col)
		c.Fill(bx+bw, u.px(19), u.px(3), u.px(6), col)
		c.Fill(bx+u.px(2), u.px(17), (bw-u.px(4))*b/100, u.px(10), mix(colBG, col, 0.7))
		c.TextRight(u.fTiny, bx-u.px(6), u.px(15), s, col)
	}
	u.drawStripes(c, h)
}

func (u *UI) footer(c *Canvas, items [][2]string, right string, rightCol color.RGBA) {
	fh := u.px(32)
	y := c.H - fh
	c.Fill(0, y, c.W, fh, colPanel)
	c.Fill(0, y, c.W, 1, colGrid)
	x := u.px(12)
	for _, it := range items {
		col := colAmber
		switch it[0] {
		case "B":
			col = colPaperDim
		case "X", "SEL":
			col = colMagenta
		case "Y", "L1", "R1", "L2", "R2", "◀▶", "L/R", "L1·R1":
			col = colCyan
		}
		x = c.Button(u.fTiny, x, y+u.px(8), it[0], it[1], col)
	}
	if right != "" {
		c.TextRight(u.fTiny, c.W-u.px(12), y+u.px(9), right, rightCol)
	}
}

func (u *UI) refreshLabel() string {
	p := u.prog
	s := "REWINDING · " + p.Phase
	if p.Total > 0 {
		s = fmt.Sprintf("REWINDING · %s %d/%d", p.Phase, p.Done, p.Total)
	}
	return s + strings.Repeat("·", u.frame/5%4)
}

func runtimeStr(m int) string {
	if m <= 0 {
		return ""
	}
	if m < 60 {
		return fmt.Sprintf("%dM", m)
	}
	return fmt.Sprintf("%dH %02dM", m/60, m%60)
}

func (m *Movie) metaLine() string {
	var parts []string
	if m.Year > 0 {
		parts = append(parts, fmt.Sprint(m.Year))
	}
	if m.Cert != "" {
		parts = append(parts, m.Cert)
	}
	if r := runtimeStr(m.Runtime); r != "" {
		parts = append(parts, r)
	}
	return strings.Join(parts, " · ")
}

func rtText(rt int) string {
	if rt < 0 {
		return "--"
	}
	return fmt.Sprintf("%d%%", rt)
}

// drawControls: the strip under the header showing what the list is showing.
func (u *UI) drawControls(c *Canvas) {
	y := u.headerH() + u.px(13)
	x := u.px(12)
	h := u.fTiny.height + u.px(6)
	seg := func(btn, label, val string, col color.RGBA) {
		if btn != "" {
			x = c.Chip(u.fTiny, x, y+u.px(1), btn, colAmberDim, false) - u.px(2)
		}
		x = c.Text(u.fTiny, x, y+u.px(4), label, colPaperDim) + u.px(4)
		w := u.fSmall.Width(val) + u.px(14)
		c.Fill(x, y, w, h+u.px(2), mix(colBG, col, 0.16))
		c.Border(x, y, w, h+u.px(2), 1, col)
		c.Text(u.fSmall, x+u.px(7), y+u.px(2), val, col)
		x += w + u.px(10)
	}
	genre := strings.ToUpper(orAll(u.filter.Genre, "ALL"))
	genre = u.fSmall.Ellipsize(genre, u.px(120))
	seg("L1·R1", "GENRE", genre, colAmber)
	seg("Y", "SORT", sortNames[u.filter.Sort], colCyan)
	extra := u.filter.Active()
	if u.filter.Genre != "" {
		extra--
	}
	fl := "NONE"
	if extra > 0 {
		fl = fmt.Sprintf("%d ON", extra)
	}
	seg("X", "FILTERS", fl, colMagenta)
	count := fmt.Sprintf("%03d", len(u.visible))
	r := c.W - u.px(14)
	c.TextRight(u.fTiny, r, y+u.px(4), "TAPES", colPaperDim)
	c.TextGlow(u.fLCD, r-u.fTiny.Width("TAPES")-u.px(6)-u.fLCD.Width(count), y-u.px(3), count, colAmber)
}

func (u *UI) drawList(c *Canvas) {
	u.drawControls(c)
	top := u.listTop()
	lw := c.W * 57 / 100
	rows := u.rowsPerPage()
	rh := u.rowH()
	n := len(u.visible)
	if u.sel < u.scroll {
		u.scroll = u.sel
	}
	if u.sel >= u.scroll+rows {
		u.scroll = u.sel - rows + 1
	}
	if u.scroll > max(0, n-rows) {
		u.scroll = max(0, n-rows)
	}
	sc := u.scroll
	c.Fill(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), colPanel)
	c.Corners(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), u.px(10), colAmberDim)
	if n == 0 {
		cx := (lw + u.px(8)) / 2
		if len(u.lib.Movies) == 0 {
			c.TextCenter(u.fBody, cx, top+u.px(60), "BLANK TAPE", colPaperDim)
			msg := "PRESS SELECT TO LOAD THE GUIDE"
			if u.refreshing {
				msg = "LOADING THE GUIDE…"
			}
			c.TextCenter(u.fTiny, cx, top+u.px(90), msg, colAmberDim)
		} else {
			c.TextCenter(u.fBody, cx, top+u.px(60), "NOTHING MATCHES", colPaperDim)
			c.TextCenter(u.fTiny, cx, top+u.px(90), "X TO LOOSEN THE FILTERS", colAmberDim)
		}
	}
	for i := 0; i < rows && sc+i < n; i++ {
		m := u.visible[sc+i]
		y := top + i*rh
		isSel := sc+i == u.sel
		fg, dim := colPaper, colPaperDim
		if isSel {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colAmber)
			c.Fill(u.px(10), y, u.px(4), rh-u.px(2), colCyan)
			fg, dim = colBG, colAmberDk
		} else if i%2 == 1 {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colPanel2)
		}
		x := u.px(20)
		ts := u.px(15)
		if isSel {
			c.Circle(x+ts/2, y+rh/2, ts/2+u.px(2), colBG, true, 0)
		}
		c.Tomato(x, y+(rh-ts)/2-u.px(1), ts, m.RT)
		x += ts + u.px(6)
		rcol := rtColor(m.RT)
		if isSel {
			rcol = colBG
		}
		c.Text(u.fLCD, x, y+u.px(2), rtText(m.RT), rcol)
		x += u.fLCD.Width("100%") + u.px(8)
		rx := lw - u.px(18)
		if m.Year > 0 {
			ys := fmt.Sprint(m.Year)
			rx -= u.fTiny.Width(ys)
			c.Text(u.fTiny, rx, y+u.px(8), ys, dim)
			rx -= u.px(8)
		}
		if r, ok := u.inRadarr(m); ok {
			mk, col := "○", colCyan
			if r.HasFile {
				mk, col = "●", colGreen
			}
			if isSel {
				col = colBG
			}
			rx -= u.fSmall.Width(mk)
			c.Text(u.fSmall, rx, y+u.px(5), mk, col)
			rx -= u.px(6)
		}
		if m.Trailer != nil {
			rx -= u.px(10)
			col := colCyanDim
			if isSel {
				col = colAmberDk
			}
			c.PlayIcon(rx, y+u.px(10), u.px(9), col)
			rx -= u.px(8)
		}
		c.Text(u.fBody, x, y+u.px(3), u.fBody.Ellipsize(m.Title, rx-x-u.px(4)), fg)
	}
	if n > rows {
		tx := lw - u.px(6)
		th := rows * rh
		c.Fill(tx, top, u.px(3), th, colGrid)
		bh := max(u.px(12), th*rows/n)
		by := top + (th-bh)*sc/max(1, n-rows)
		c.Fill(tx, by, u.px(3), bh, colAmber)
	}

	// right panel: the tape box
	if m := u.selected(); m != nil {
		u.drawSidePanel(c, m, lw+u.px(10), top-u.px(4), c.W-lw-u.px(22))
	}

	right, rcol := "", colPaperDim
	switch {
	case u.refreshing:
		right, rcol = u.refreshLabel(), colAmber
	case !u.lib.Updated.IsZero():
		right = "GUIDE " + strings.ToUpper(u.lib.Updated.Local().Format("Jan 2 15:04"))
	}
	u.footer(c, [][2]string{{"A", "INFO"}, {"Y", "SORT"}, {"X", "FILTER"}, {"SEL", "REFRESH"}}, right, rcol)
}

func (u *UI) drawSidePanel(c *Canvas, m *Movie, x, y, w int) {
	pw, ph := u.listPosterBox()
	c.Fill(x, y, pw+u.px(8), ph+u.px(8), colPanel2)
	c.Border(x, y, pw+u.px(8), ph+u.px(8), 1, colGrid)
	img, failed := u.listPoster(m, false)
	if img != nil {
		c.Poster(img, x+u.px(4), y+u.px(4), pw, ph)
	} else {
		u.noPoster(c, m, x+u.px(4), y+u.px(4), pw, ph, failed)
	}
	// score column
	sx := x + pw + u.px(16)
	sw := x + w - sx
	sy := y + u.px(2)
	c.Text(u.fTiny, sx, sy, "TOMATOMETER", colPaperDim)
	sy += u.fTiny.height + u.px(4)
	ts := u.px(24)
	c.Tomato(sx, sy+u.px(6), ts, m.RT)
	c.TextGlow(u.fLCDBig, sx+ts+u.px(6), sy-u.px(4), rtText(m.RT), rtColor(m.RT))
	sy += u.fLCDBig.height - u.px(4)
	vu := 0.0
	if m.RT >= 0 {
		vu = float64(m.RT) / 100
	}
	c.VU(sx, sy, sw, u.px(8), 10, vu)
	sy += u.px(16)
	kv := func(k, v string, col color.RGBA) {
		if v == "" {
			return
		}
		c.Text(u.fTiny, sx, sy+u.px(1), k, colPaperDim)
		c.Text(u.fSmall, sx+u.px(48), sy, u.fSmall.Ellipsize(v, sw-u.px(48)), col)
		sy += u.fSmall.height + u.px(3)
	}
	imdb, meta := "--", "--"
	if m.IMDb > 0 {
		imdb = fmt.Sprintf("%.1f", m.IMDb)
	}
	if m.Meta >= 0 {
		meta = fmt.Sprint(m.Meta)
	}
	kv("IMDB", imdb, colPaper)
	kv("META", meta, colPaper)
	sy += u.px(4)
	c.Text(u.fTiny, sx, sy, "STREAMING ON", colPaperDim)
	sy += u.fTiny.height + u.px(2)
	names := m.ProviderNames()
	if len(names) == 0 && !m.HasDetails() {
		names = []string{"…"}
	}
	maxSvc := 3
	if _, ok := u.inRadarr(m); ok {
		maxSvc = 2
	}
	for i, p := range names {
		if i == maxSvc {
			c.Text(u.fTiny, sx, sy, fmt.Sprintf("+%d MORE", len(names)-maxSvc), colCyanDim)
			sy += u.fTiny.height + u.px(1)
			break
		}
		c.Text(u.fSmall, sx, sy, u.fSmall.Ellipsize(strings.ToUpper(p), sw), colCyan)
		sy += u.fSmall.height + u.px(1)
	}
	if r, ok := u.inRadarr(m); ok {
		sy += u.px(3)
		if r.HasFile {
			c.Text(u.fTiny, sx, sy, "● IN YOUR LIBRARY", colGreen)
		} else {
			c.Text(u.fTiny, sx, sy, "○ IN RADARR", colCyan)
		}
	}

	// title + liner notes under the poster
	ty := y + ph + u.px(16)
	bottom := c.H - u.px(40)
	for i, l := range u.fBody.Wrap(m.Title, w-u.px(4)) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBody, x, ty, l, colAmber)
		ty += u.fBody.height
	}
	meta2 := m.metaLine()
	if len(m.Genres) > 0 {
		meta2 += "  " + strings.ToUpper(strings.Join(m.Genres, " / "))
	}
	c.Text(u.fTiny, x, ty+u.px(2), u.fTiny.Ellipsize(meta2, w), colCyan)
	ty += u.fTiny.height + u.px(8)
	text := m.Overview
	if len(m.Cast) > 0 {
		var names []string
		for i, cm := range m.Cast {
			if i == 3 {
				break
			}
			names = append(names, cm.Name)
		}
		text = "WITH " + strings.ToUpper(strings.Join(names, ", ")) + "\n" + text
	}
	for i, l := range u.fTiny.Wrap(text, w-u.px(4)) {
		if ty+u.fTiny.height > bottom {
			break
		}
		col := colPaperDim
		if i == 0 && len(m.Cast) > 0 {
			col = colPaper
		}
		c.Text(u.fTiny, x, ty, l, col)
		ty += u.fTiny.height + u.px(1)
	}
}

// noPoster: a blank VHS sleeve with the title while the poster loads (or if there isn't one).
func (u *UI) noPoster(c *Canvas, m *Movie, x, y, w, h int, failed bool) {
	if !failed {
		c.Noise(x, y, w, h, uint32(u.frame/2))
		lw := u.fLCD.Width("TUNING") + u.px(12)
		c.Fill(x+(w-lw)/2, y+h/2-u.px(14), lw, u.px(28), colBG)
		c.TextCenter(u.fLCD, x+w/2, y+h/2-u.px(12), "TUNING", colAmber)
		return
	}
	c.Fill(x, y, w, h, colPanel)
	c.Fill(x, y+h*2/3, w, u.px(3), colAmber)
	c.Fill(x, y+h*2/3+u.px(3), w, u.px(3), colMagenta)
	c.Fill(x, y+h*2/3+u.px(6), w, u.px(3), colCyan)
	ly := y + u.px(14)
	for i, l := range u.fSmall.Wrap(strings.ToUpper(m.Title), w-u.px(16)) {
		if i == 4 {
			break
		}
		c.TextCenter(u.fSmall, x+w/2, ly, l, colPaper)
		ly += u.fSmall.height
	}
}

func (u *UI) drawDetail(c *Canvas) {
	m := u.cur
	top := u.headerH() + u.px(16)
	pw, ph := u.detailPosterBox()
	fx, fy := u.px(14), top
	c.Fill(fx, fy, pw+u.px(12), ph+u.px(12), colPanel2)
	c.Border(fx, fy, pw+u.px(12), ph+u.px(12), 1, colGrid)
	c.Corners(fx-u.px(3), fy-u.px(3), pw+u.px(18), ph+u.px(18), u.px(12), colCyan)
	img, failed := u.image(u.env.ImageURL("w342", m.Poster), pw, ph, true, false)
	if img != nil {
		c.Poster(img, fx+u.px(6), fy+u.px(6), pw, ph)
	} else {
		u.noPoster(c, m, fx+u.px(6), fy+u.px(6), pw, ph, failed)
	}
	c.Text(u.fTiny, fx, fy+ph+u.px(18), fmt.Sprintf("TAPE %02d/%02d", u.sel+1, len(u.visible)), colAmberDim)
	c.TextRight(u.fTiny, fx+pw+u.px(12), fy+ph+u.px(18), "◀ L1  R1 ▶", colAmberDim)

	x := fx + pw + u.px(30)
	w := c.W - x - u.px(14)
	y := top - u.px(2)
	for i, l := range u.fBig.Wrap(strings.ToUpper(m.Title), w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBig, x, y, l, colAmber)
		y += u.fBig.height + u.px(2)
	}
	c.Text(u.fSmall, x, y+u.px(2), m.metaLine(), colCyan)
	y += u.fSmall.height + u.px(8)
	cx := x
	for _, g := range m.Genres {
		lbl := strings.ToUpper(g)
		if cx+u.fTiny.Width(lbl)+u.px(12) > x+w {
			break
		}
		cx = c.Chip(u.fTiny, cx, y, lbl, colPaperDim, false)
	}
	if len(m.Genres) > 0 {
		y += u.fTiny.height + u.px(12)
	}

	// score block
	bh := u.px(84)
	c.Fill(x, y, w, bh, colPanel)
	c.Corners(x, y, w, bh, u.px(8), colAmberDim)
	ts := u.px(30)
	c.Tomato(x+u.px(12), y+u.px(14), ts, m.RT)
	c.TextGlow(u.fLCDBig, x+u.px(12)+ts+u.px(8), y+u.px(6), rtText(m.RT), rtColor(m.RT))
	lbl := "TOMATOMETER"
	switch {
	case m.RT >= 60:
		lbl = "TOMATOMETER · FRESH"
	case m.RT >= 0:
		lbl = "TOMATOMETER · ROTTEN"
	case u.env.Config.OMDbKey == "":
		lbl = "TOMATOMETER · NEEDS OMDB KEY"
	}
	c.Text(u.fTiny, x+u.px(12), y+u.px(52), lbl, colPaperDim)
	vu := 0.0
	if m.RT >= 0 {
		vu = float64(m.RT) / 100
	}
	c.VU(x+u.px(12), y+u.px(68), w*58/100, u.px(8), 16, vu)
	// other scores
	ox := x + w*66/100
	oy := y + u.px(10)
	other := func(k, v string) {
		c.Text(u.fTiny, ox, oy+u.px(3), k, colPaperDim)
		c.Text(u.fLCD, ox+u.px(48), oy-u.px(2), v, colPaper)
		oy += u.px(23)
	}
	imdb, meta, tm := "--", "--", "--"
	if m.IMDb > 0 {
		imdb = fmt.Sprintf("%.1f", m.IMDb)
	}
	if m.Meta >= 0 {
		meta = fmt.Sprint(m.Meta)
	}
	if m.VoteCount > 0 {
		tm = fmt.Sprintf("%.1f", m.VoteAvg)
	}
	other("IMDB", imdb)
	other("META", meta)
	other("TMDB", tm)
	y += bh + u.px(10)

	kv := func(k, v string, col color.RGBA, lines int) {
		if v == "" {
			return
		}
		c.Text(u.fTiny, x, y+u.px(2), k, colPaperDim)
		for i, l := range u.fSmall.Wrap(v, w-u.px(86)) {
			if i == lines {
				break
			}
			if i == lines-1 {
				l = u.fSmall.Ellipsize(l, w-u.px(86))
			}
			c.Text(u.fSmall, x+u.px(86), y, l, col)
			y += u.fSmall.height + u.px(1)
		}
		y += u.px(3)
	}
	var cast []string
	for i, cm := range m.Cast {
		if i == 4 {
			break
		}
		cast = append(cast, cm.Name)
	}
	kv("DIRECTOR", strings.Join(m.Directors, ", "), colPaper, 1)
	kv("STARRING", strings.Join(cast, ", "), colPaper, 2)
	kv("STREAMING", strings.ToUpper(strings.Join(m.ProviderNames(), " · ")), colCyan, 2)
	if !m.Released.IsZero() {
		kv("DIGITAL", strings.ToUpper(m.Released.Format("Jan 2, 2006")), colPaper, 1)
	}
	if st, col := u.radarrStatus(m); st != "" {
		kv("RADARR", st, col, 1)
	}

	// liner notes, full width under both columns
	ny := max(y+u.px(4), fy+ph+u.px(40))
	nx, nw := u.px(14), c.W-u.px(28)
	bottom := c.H - u.px(38)
	var lines []string
	var colors []color.RGBA
	add := func(s string, col color.RGBA) {
		for _, l := range u.fSmall.Wrap(s, nw-u.px(20)) {
			lines = append(lines, l)
			colors = append(colors, col)
		}
	}
	if m.Tagline != "" {
		add("\""+m.Tagline+"\"", colAmber)
	}
	add(orAll(m.Overview, "No synopsis on file."), colPaper)
	if m.Awards != "" {
		add("★ "+m.Awards, colAmber)
	}
	if len(m.Cast) > 0 {
		add("", colPaper)
		add("CAST", colAmberDim)
		for _, cm := range m.Cast {
			s := cm.Name
			if cm.Role != "" {
				s += " … " + cm.Role
			}
			add(s, colPaperDim)
		}
	}
	if len(m.Writers) > 0 {
		add("WRITTEN BY "+strings.Join(m.Writers, ", "), colPaperDim)
	}
	add("Data: TMDB · streaming info: JustWatch · scores: OMDb", colCyanDim)
	if bottom-ny > u.px(40) {
		c.Fill(nx, ny, nw, bottom-ny, colPanel)
		c.Text(u.fTiny, nx+u.px(10), ny+u.px(4), "SYNOPSIS", colAmberDim)
		c.HLineDashed(nx+u.px(10)+u.fTiny.Width("SYNOPSIS ")+u.px(4), ny+u.px(4)+u.fTiny.height/2, nw-u.px(150), u.px(4), u.px(3), colGrid)
		ly := ny + u.fTiny.height + u.px(8)
		maxLines := max(1, (bottom-ly)/(u.fSmall.height+u.px(1)))
		if u.detailScrl > max(0, len(lines)-maxLines) {
			u.detailScrl = max(0, len(lines)-maxLines)
		}
		for i := u.detailScrl; i < len(lines) && i-u.detailScrl < maxLines; i++ {
			c.Text(u.fSmall, nx+u.px(10), ly, lines[i], colors[i])
			ly += u.fSmall.height + u.px(1)
		}
		if len(lines) > maxLines {
			c.TextRight(u.fTiny, nx+nw-u.px(8), ny+u.px(4), "↕ MORE", colAmberDim)
		}
	}
	label := "TRAILER"
	if m.Trailer == nil {
		label = "STILLS"
	}
	items := [][2]string{{"A", label}}
	if u.env.RadarrOn() {
		if _, ok := u.inRadarr(m); !ok {
			items = append(items, [2]string{"X", "ADD TO RADARR"})
		}
	}
	items = append(items, [2]string{"L1", "PREV"}, [2]string{"R1", "NEXT"}, [2]string{"B", "BACK"})
	u.footer(c, items, "", colPaperDim)
}

func (u *UI) drawTrailer(c *Canvas) {
	m := u.cur
	tw, th := u.tvBox()
	bx, by := u.px(12), u.headerH()+u.px(14)
	bezel := u.px(8)
	c.Fill(bx, by, tw+2*bezel, th+2*bezel, rgb(0x1b, 0x1c, 0x20))
	c.Border(bx, by, tw+2*bezel, th+2*bezel, 1, colGrid)
	c.Corners(bx-u.px(3), by-u.px(3), tw+2*bezel+u.px(6), th+2*bezel+u.px(6), u.px(12), colAmberDim)
	ix, iy := bx+bezel, by+bezel
	n := len(m.Stills)
	var img *image.RGBA
	failed := n == 0
	if n > 0 {
		img, failed = u.stillImage(m.Stills[u.still], true)
	}
	t := u.frame - u.stillStart
	switch {
	case img != nil:
		// slow Ken Burns pan across the slightly oversized still
		b := img.Bounds()
		ex, ey := b.Dx()-tw, b.Dy()-th
		f := math.Min(1, float64(u.playFrames%stillFrames)/float64(stillFrames))
		if u.paused {
			f = 0.5
		}
		if u.still%2 == 1 {
			f = 1 - f
		}
		c.Blit(img, int(float64(ex)*f), int(float64(ey)*(0.3+0.4*f)), ix, iy, tw, th)
	case failed:
		c.Fill(ix, iy, tw, th, rgb(0x12, 0x2a, 0xc0)) // VCR blue
		c.OSD(u.fOSD, ix+tw/2-u.fOSD.Width("NO STILLS")/2, iy+th/2-u.px(16), "NO STILLS", colWhite)
	default:
		c.Noise(ix, iy, tw, th, uint32(u.frame))
	}
	c.Scanlines(ix, iy, tw, th, 0.22)
	if t < 9 && n > 1 && !u.paused {
		band := iy + (th+u.px(40))*t/9 - u.px(40)
		c.Tracking(ix, iy, tw, th, band, u.px(26), uint32(u.frame))
	}
	// OSD
	osdCol := colWhite
	if u.paused {
		c.OSD(u.fOSD, ix+u.px(14), iy+u.px(8), "PAUSE", osdCol)
		c.PauseIcon(ix+u.px(18)+u.fOSD.Width("PAUSE"), iy+u.px(15), u.px(16), osdCol)
	} else {
		c.OSD(u.fOSD, ix+u.px(14), iy+u.px(8), "PLAY", osdCol)
		c.PlayIcon(ix+u.px(20)+u.fOSD.Width("PLAY")+u.px(2), iy+u.px(16), u.px(16), colBlack)
		c.PlayIcon(ix+u.px(18)+u.fOSD.Width("PLAY"), iy+u.px(14), u.px(16), osdCol)
	}
	c.OSD(u.fOSD, ix+tw-u.px(14)-u.fOSD.Width("SP"), iy+u.px(8), "SP", osdCol)
	secs := u.playFrames / 20
	counter := fmt.Sprintf("0:%02d:%02d", secs/60%60, secs%60)
	c.OSD(u.fOSD, ix+u.px(14), iy+th-u.px(36), counter, osdCol)
	if n > 0 {
		s := fmt.Sprintf("%d/%d", u.still+1, n)
		c.OSD(u.fOSD, ix+tw-u.px(14)-u.fOSD.Width(s), iy+th-u.px(36), s, osdCol)
	}

	// under the TV: title and tagline
	ty := by + th + 2*bezel + u.px(12)
	tx := bx
	twid := tw + 2*bezel
	c.TextGlow(u.fBig, tx, ty, u.fBig.Ellipsize(strings.ToUpper(m.Title), twid), colAmber)
	ty += u.fBig.height + u.px(4)
	c.Text(u.fSmall, tx, ty, u.fSmall.Ellipsize(m.metaLine()+"   ·   STILL REEL FROM TMDB", twid), colCyan)
	ty += u.fSmall.height + u.px(8)
	var lines []string
	var cols []color.RGBA
	if m.Tagline != "" {
		for _, l := range u.fSmall.Wrap("\""+m.Tagline+"\"", twid) {
			lines, cols = append(lines, l), append(cols, colAmberDim)
		}
	}
	for _, l := range u.fTiny.Wrap(m.Overview, twid) {
		lines, cols = append(lines, l), append(cols, colPaperDim)
	}
	for i, l := range lines {
		f := u.fTiny
		if cols[i] == colAmberDim {
			f = u.fSmall
		}
		if ty+f.height > c.H-u.px(38) {
			break
		}
		c.Text(f, tx, ty, l, cols[i])
		ty += f.height + u.px(1)
	}

	// right: QR to the real trailer
	qx := bx + twid + u.px(16)
	qw := c.W - qx - u.px(12)
	qy := by
	c.Fill(qx, qy, qw, th+2*bezel, colPanel)
	c.Corners(qx, qy, qw, th+2*bezel, u.px(8), colCyan)
	url, head, sub := "", "SCAN FOR TRAILER", "PLAYS ON YOUR PHONE"
	if m.Trailer != nil {
		url = m.Trailer.URL()
	} else {
		url = fmt.Sprintf("https://www.themoviedb.org/movie/%d", m.ID)
		head, sub = "NO TRAILER ON FILE", "SCAN FOR THE TMDB PAGE"
	}
	c.TextCenter(u.fSmall, qx+qw/2, qy+u.px(10), head, colCyan)
	mod := max(2, u.px(4))
	for mod > 2 && QRSize(url, mod) > qw-u.px(16) {
		mod--
	}
	qs := QRSize(url, mod)
	c.QR(qx+(qw-qs)/2, qy+u.px(34), mod, url)
	ly := qy + u.px(34) + qs + u.px(8)
	short := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "www.")
	c.TextCenter(u.fTiny, qx+qw/2, ly, u.fTiny.Ellipsize(short, qw-u.px(8)), colPaper)
	ly += u.fTiny.height + u.px(2)
	c.TextCenter(u.fTiny, qx+qw/2, ly, sub, colPaperDim)
	if m.Trailer != nil && m.Trailer.Name != "" {
		ly += u.fTiny.height + u.px(8)
		for i, l := range u.fTiny.Wrap(strings.ToUpper(m.Trailer.Name), qw-u.px(12)) {
			if i == 2 {
				break
			}
			c.TextCenter(u.fTiny, qx+qw/2, ly, l, colAmberDim)
			ly += u.fTiny.height
		}
	}
	act := "PAUSE"
	if u.paused {
		act = "PLAY"
	}
	items := [][2]string{{"A", act}, {"◀▶", "STILL"}, {"L1·R1", "MOVIE"}}
	if u.env.RadarrOn() {
		if _, ok := u.inRadarr(m); !ok {
			items = append(items, [2]string{"X", "RADARR"})
		}
	}
	u.footer(c, append(items, [2]string{"B", "STOP"}), "", colPaperDim)
}

func (u *UI) drawSetup(c *Canvas) {
	y := u.headerH() + u.px(16)
	c.TextGlow(u.fBig, u.px(16), y, "INSERT TAPE", colAmber)
	c.Text(u.fSmall, u.px(16)+u.fBig.Width("INSERT TAPE")+u.px(14), y+u.px(3), "REWIND NEEDS TWO FREE API KEYS", colCyan)
	y += u.fBig.height + u.px(12)
	colW := (c.W - u.px(44)) / 2
	block := func(x int, n, title, need, url, short string) {
		h := u.px(212)
		c.Fill(x, y, colW, h, colPanel)
		c.Corners(x, y, colW, h, u.px(8), colAmberDim)
		c.Text(u.fLCD, x+u.px(10), y+u.px(4), n, colAmber)
		c.Text(u.fSmall, x+u.px(36), y+u.px(8), title, colPaper)
		c.Text(u.fTiny, x+u.px(36), y+u.px(8)+u.fSmall.height, need, colPaperDim)
		mod := u.px(4)
		qs := QRSize(url, mod)
		for mod > 2 && qs > u.px(140) {
			mod--
			qs = QRSize(url, mod)
		}
		c.QR(x+(colW-qs)/2, y+u.px(52), mod, url)
		c.TextCenter(u.fTiny, x+colW/2, y+u.px(52)+qs+u.px(6), short, colCyan)
	}
	block(u.px(16), "1", "TMDB API KEY", "REQUIRED · MOVIES, CAST, TRAILERS", "https://www.themoviedb.org/settings/api", "themoviedb.org/settings/api")
	block(u.px(28)+colW, "2", "OMDB API KEY", "FOR ROTTEN TOMATOES SCORES", "https://www.omdbapi.com/apikey.aspx", "omdbapi.com/apikey.aspx")
	y += u.px(224)
	c.Text(u.fSmall, u.px(16), y, "Then put them in App/Rewind/data/config.json on the SD card:", colPaper)
	y += u.fSmall.height + u.px(6)
	c.Fill(u.px(16), y, c.W-u.px(32), u.px(46), colBlack)
	c.Text(u.fTiny, u.px(26), y+u.px(6), `"tmdb_api_key": "your TMDB key or read access token",`, colGreen)
	c.Text(u.fTiny, u.px(26), y+u.px(6)+u.fTiny.height+u.px(2), `"omdb_api_key": "your OMDb key",`, colGreen)
	u.footer(c, [][2]string{{"A", "RELOAD CONFIG"}, {"B", "EXIT"}}, "SCAN A CODE WITH YOUR PHONE", colPaperDim)
}

func (u *UI) panel(c *Canvas, w, h int, accent color.RGBA) (int, int) {
	c.Blend(0, 0, c.W, c.H, colBlack, 0.72)
	x, y := (c.W-w)/2, (c.H-h)/2
	c.Fill(x, y, w, h, colPanel)
	c.Border(x, y, w, h, 1, colGrid)
	c.Fill(x, y, w, u.px(3), accent)
	c.Corners(x-u.px(4), y-u.px(4), w+u.px(8), h+u.px(8), u.px(14), accent)
	return x, y
}

func (u *UI) drawFilter(c *Canvas) {
	rh := u.fSmall.height + u.px(12)
	w, h := c.W*80/100, u.px(96)+fRows*rh
	x, y := u.panel(c, w, h, colMagenta)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(14), "TRACKING", colMagenta)
	c.Text(u.fTiny, x+u.px(22)+u.fBig.Width("TRACKING")+u.px(8), y+u.px(20), "SORT & FILTER THE SHELF", colPaperDim)
	ry := y + u.px(50)
	vx := x + u.px(190)
	for i := 0; i < fRows; i++ {
		sel := i == u.filterRow
		if sel {
			c.Fill(x+u.px(12), ry-u.px(4), w-u.px(24), rh-u.px(2), colPanel2)
			c.Fill(x+u.px(12), ry-u.px(4), u.px(3), rh-u.px(2), colMagenta)
		}
		lc := colPaperDim
		if sel {
			lc = colPaper
		}
		c.Text(u.fSmall, x+u.px(24), ry, filterLabels[i], lc)
		val := u.filterValue(i)
		vc := colAmber
		if i == fRowReset {
			vc = colPaperDim
			if sel {
				vc = colMagenta
			}
		}
		if i != fRowReset {
			ac := colGrid
			if sel {
				ac = colMagenta
			}
			c.Text(u.fSmall, vx, ry, "◀", ac)
			c.Text(u.fSmall, x+w-u.px(30), ry, "▶", ac)
		}
		c.Text(u.fSmall, vx+u.px(24), ry, u.fSmall.Ellipsize(val, w-u.px(250)), vc)
		ry += rh
	}
	c.Text(u.fSmall, x+u.px(24), y+h-u.px(32), fmt.Sprintf("%d OF %d TAPES MATCH", len(u.visible), len(u.lib.Movies)), colCyan)
	c.Button(u.fTiny, x+w-u.px(110), y+h-u.px(32), "B", "DONE", colPaperDim)
}

func (u *UI) drawMsg(c *Canvas) {
	w := c.W * 84 / 100
	var lines []string
	for _, para := range u.msgBody {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, u.fSmall.Wrap(para, w-u.px(40))...)
	}
	maxLines := (c.H*80/100 - u.px(100)) / (u.fSmall.height + u.px(2))
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	h := u.px(96) + len(lines)*(u.fSmall.height+u.px(2))
	x, y := u.panel(c, w, h, u.msgCol)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(16), u.msgTitle, u.msgCol)
	ly := y + u.px(52)
	for _, l := range lines {
		c.Text(u.fSmall, x+u.px(20), ly, l, colPaper)
		ly += u.fSmall.height + u.px(2)
	}
	c.Button(u.fTiny, x+u.px(20), y+h-u.px(30), "A", "OK", colAmber)
}

func (u *UI) drawBoot(c *Canvas) {
	cx := c.W / 2
	u.drawStripes(c, c.H/2-u.px(140))
	t := "REWIND"
	c.TextGlow(u.fLogo, cx-u.fLogo.Width(t)/2, c.H/2-u.px(120), t, colAmber)
	c.TextCenter(u.fTiny, cx, c.H/2-u.px(86), "NEW ON STREAMING · A VIDEO GUIDE FOR ONION OS", colCyan)
	vw, vh := u.px(300), u.px(170)
	prog := 0.0
	if u.prog.Total > 0 {
		prog = float64(u.prog.Done) / float64(u.prog.Total)
	}
	angle := -u.angle() * 1.5
	if u.bootErr != "" {
		angle = 0
	}
	c.VHS(cx-vw/2, c.H/2-u.px(64), vw, vh, "NEW RELEASES", "BE KIND · REWIND", angle, 1-prog, u.fBody, u.fTiny)
	y := c.H/2 + u.px(120)
	if u.bootErr != "" {
		c.TextCenter(u.fBody, cx, y, "TRACKING ERROR", colMagenta)
		for i, l := range u.fSmall.Wrap(u.bootErr, c.W-u.px(80)) {
			if i == 2 {
				break
			}
			c.TextCenter(u.fSmall, cx, y+u.px(24)+i*(u.fSmall.height+u.px(2)), l, colPaper)
		}
		c.Button(u.fTiny, cx-u.px(90), c.H-u.px(34), "A", "RETRY", colAmber)
		c.Button(u.fTiny, cx+u.px(20), c.H-u.px(34), "B", "EXIT", colPaperDim)
		return
	}
	msg := "◀◀ " + u.prog.Phase
	if u.prog.Total > 0 {
		msg += fmt.Sprintf("  %d/%d", u.prog.Done, u.prog.Total)
	}
	c.TextCenter(u.fSmall, cx, y, msg+strings.Repeat(".", u.frame/4%4), colAmber)
	c.VU(cx-u.px(150), y+u.px(26), u.px(300), u.px(10), 20, prog)
	c.TextCenter(u.fTiny, cx, c.H-u.px(26), "DATA: TMDB · JUSTWATCH · OMDB   ·   v"+version, colPaperDim)
}

func (u *UI) drawBye(c *Canvas) {
	c.Fill(0, 0, c.W, c.H, rgb(0x12, 0x2a, 0xc0))
	cx := c.W / 2
	c.OSD(u.fOSD, u.px(30), u.px(30), "REW", colWhite)
	c.OSD(u.fOSD, u.px(34)+u.fOSD.Width("REW "), u.px(30), "◀◀", colWhite)
	vw, vh := u.px(240), u.px(136)
	c.VHS(cx-vw/2, c.H/2-vh/2-u.px(20), vw, vh, "REWIND", "", -float64(u.frame)*0.9, 0.2, u.fBody, u.fTiny)
	c.OSD(u.fOSD, cx-u.fOSD.Width("BE KIND, PLEASE REWIND")/2, c.H/2+vh/2, "BE KIND, PLEASE REWIND", colWhite)
	c.Tracking(0, 0, c.W, c.H, (u.frame*37)%c.H, u.px(18), uint32(u.frame))
}

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
