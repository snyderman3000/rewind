package main

// Drawing for the Front Panel look: every screen sits inside a silver VCR deck,
// with the VFD clock and indicators on top and deck keys along the bottom.

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"
)

// ---------- layout ----------

func (u *UI) topH() int           { return u.px(58) }
func (u *UI) keysH() int          { return u.px(42) }
func (u *UI) contentBot() int     { return u.H - u.keysH() }
func (u *UI) rowH() int           { return u.px(31) }
func (u *UI) listTop() int        { return u.topH() + u.px(34) }
func (u *UI) listW() int          { return u.W * 61 / 100 }
func (u *UI) rowsPerPage() int    { return max(1, (u.contentBot()-u.listTop()-u.px(4))/u.rowH()) }
func (u *UI) angle() float64      { return float64(u.frame) * 0.35 }
func (u *UI) blink(n int) bool    { return u.frame/n%2 == 0 }
func (u *UI) dimText() color.RGBA { return colVFDMid }

func (u *UI) listPosterBox() (int, int)   { return u.px(116), u.px(174) }
func (u *UI) detailPosterBox() (int, int) { return u.px(176), u.px(264) }
func (u *UI) tvBox() (int, int)           { return u.px(400), u.px(225) }

// ---------- frame ----------

func (u *UI) Draw(c *Canvas) {
	c.Fill(0, 0, c.W, c.H, colGlass)
	if u.scr == scrBye {
		u.drawBye(c)
		return
	}
	u.deckTop(c)
	switch u.scr {
	case scrBoot:
		u.drawBoot(c)
	case scrSetup:
		u.drawSetup(c)
	case scrList:
		u.drawList(c)
	case scrDetail:
		u.drawDetail(c)
	case scrTrailer:
		u.drawTrailer(c)
	case scrKeyboard:
		u.drawKeyboard(c)
	}
	c.Scanlines(0, u.topH(), c.W, u.contentBot()-u.topH(), 0.10)
	switch u.modal {
	case modalMsg:
		u.drawMsg(c)
	case modalFilter:
		u.drawFilter(c)
	case modalAdd:
		u.drawAdd(c)
	}
	if u.toast != "" && time.Now().Before(u.toastUntil) {
		t := u.fSmall.Ellipsize(u.toast, c.W-u.px(60))
		w := u.fSmall.Width(t) + u.px(24)
		h := u.fSmall.height + u.px(10)
		x, y := (c.W-w)/2, u.contentBot()-h-u.px(8)
		c.Fill(x, y, w, h, colGlass2)
		c.Border(x, y, w, h, 1, colVFD)
		c.Text(u.fSmall, x+u.px(12), y+u.px(5), t, colVFD)
	}
}

// deckTop: the silver strip with the engraved name, the tape slot and the VFD window.
func (u *UI) deckTop(c *Canvas) {
	h := u.topH()
	c.Metal(0, 0, c.W, h)
	lx := u.px(18)
	c.Text(u.fLogo, lx+1, u.px(14)+1, "REWIND", rgb(0xd8, 0xda, 0xde))
	c.Text(u.fLogo, lx, u.px(14), "REWIND", colEngrave)
	c.Text(u.fTiny, lx+u.px(2), u.px(40), "NEW RELEASES · 4 HEAD", rgb(0x44, 0x46, 0x4c))
	// tape slot and its flap; a tape sits in it while one is "playing"
	sx, sy, sw, sh := u.px(196), u.px(14), u.px(204), u.px(30)
	c.Fill(sx, sy, sw, sh, rgb(0x1a, 0x1b, 0x1e))
	if u.scr == scrTrailer || u.scr == scrDetail {
		c.Fill(sx+u.px(4), sy+u.px(4), sw-u.px(8), sh-u.px(8), rgb(0x10, 0x10, 0x12))
		c.Fill(sx+u.px(20), sy+u.px(6), sw-u.px(40), u.px(4), colLabelY)
	} else {
		c.Fill(sx+u.px(4), sy+u.px(4), sw-u.px(8), sh-u.px(8), rgb(0x2c, 0x2d, 0x31))
		c.Fill(sx+u.px(4), sy+u.px(4), sw-u.px(8), u.px(2), colKeyTop)
	}
	// VFD window
	vx := u.px(414)
	vw := c.W - u.px(10) - vx
	vy, vh := u.px(8), u.px(42)
	c.Fill(vx, vy, vw, vh, colGlass)
	c.Border(vx, vy, vw, vh, 1, colEngrave)
	clock := strings.TrimPrefix(time.Now().Format("15:04"), "0")
	dw, dh, t := u.px(14), u.px(26), max(2, u.px(3))
	cw := Seg7Width(dw, t, "88:88")
	c.Seg7(vx+u.px(8)+cw-Seg7Width(dw, t, clock), vy+u.px(8), dw, dh, t, clock, colVFD, colGlass)
	ix := vx + u.px(14) + cw
	lit := func(x, y int, s string, on bool, col color.RGBA) int {
		if !on {
			col = mix(colGlass, colVFD, 0.16)
		}
		return c.Text(u.fTiny, x, y, s, col) + u.px(7)
	}
	y1, y2 := vy+u.px(6), vy+u.px(22)
	x := lit(ix, y1, "PLAY", u.scr == scrTrailer && !u.paused, colVFD)
	x = lit(x, y1, "REC", u.rad.adding || (u.modal == modalAdd && u.blink(6)), colRec)
	lit(x, y1, "REW", u.refreshing && u.blink(5) || u.scr == scrBoot, colVFD)
	ch := "CH--"
	if n := len(u.visible); n > 0 && u.scr != scrBoot && u.scr != scrSetup {
		ch = fmt.Sprintf("CH%02d", min(99, u.sel+1))
	}
	x = lit(ix, y2, ch, true, colVFD)
	x = lit(x, y2, "NET", wifiUp(), colVFD)
	if b := battery(); b >= 0 {
		col := colVFD
		if b <= 15 {
			col = colDeckAmb
		}
		lit(x, y2, fmt.Sprintf("%d%%", b), true, col)
	}
}

// keys: the deck buttons along the bottom. Each item is {button, caption}.
func (u *UI) keys(c *Canvas, items [][2]string) {
	y := u.contentBot()
	c.Metal(0, y, c.W, u.keysH())
	pad, gap := u.px(22), u.px(8)
	total := 0
	for _, it := range items {
		total += u.fTiny.Width(it[0]+" "+it[1]) + pad + gap
	}
	if total > c.W-u.px(20) {
		pad, gap = u.px(12), u.px(4)
	}
	x := u.px(12)
	ky, kh := y+u.px(8), u.px(26)
	for _, it := range items {
		w := u.fTiny.Width(it[0]+" "+it[1]) + pad
		c.Fill(x, ky, w, kh, colKey)
		c.Fill(x, ky, w, max(1, u.px(2)), colKeyTop)
		c.Fill(x, ky+kh-1, w, 1, rgb(0x18, 0x18, 0x1a))
		col := colVFD
		if strings.Contains(it[1], "REC") {
			col = colRec
		}
		bx := c.Text(u.fTiny, x+pad/2, ky+u.px(6), it[0], col)
		c.Text(u.fTiny, bx+u.fTiny.Width(" "), ky+u.px(6), it[1], rgb(0xd0, 0xd2, 0xd6))
		x += w + gap
	}
}

func (u *UI) radarrKey(m *Movie, long bool) [][2]string {
	if !u.env.RadarrOn() {
		return nil
	}
	if m != nil {
		if _, ok := u.inRadarr(m); ok {
			return [][2]string{{"X", "IN RADARR"}}
		}
	}
	if long {
		return [][2]string{{"X", "● REC TO RADARR"}}
	}
	return [][2]string{{"X", "● REC"}}
}

// ---------- text helpers ----------

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

func rtDigits(rt int) string {
	if rt < 0 {
		return "--"
	}
	return fmt.Sprint(rt)
}

// scoreCol: fresh scores glow teal, rotten ones amber, unknown stay dim.
func scoreCol(rt int, bright bool) color.RGBA {
	switch {
	case rt < 0:
		return colVFDDim
	case rt < 60:
		return colDeckAmb
	case bright:
		return colVFD
	}
	return mix(colVFD, colGlass, 0.22)
}

func (u *UI) refreshLabel() string {
	p := u.prog
	s := "◀◀ " + p.Phase
	if p.Total > 0 {
		s = fmt.Sprintf("◀◀ %s %d/%d", p.Phase, p.Done, p.Total)
	}
	return s
}

// ---------- shelf ----------

func (u *UI) drawList(c *Canvas) {
	top := u.listTop()
	lw := u.listW()
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

	// program line: genre, sort, filters · count or refresh progress
	hy := u.topH() + u.px(10)
	x := u.px(16)
	if u.search.on {
		u.searchHeader(c, hy)
	} else {
		x = c.Text(u.fSmall, x, hy-u.px(2), "◀ "+u.fSmall.Ellipsize(strings.ToUpper(orAll(u.filter.Genre, "ALL GENRES")), u.px(130))+" ▶", colVFD) + u.px(12)
		x = c.Text(u.fTiny, x, hy, "SORT ", colVFDDim)
		x = c.Text(u.fTiny, x, hy, sortNames[u.filter.Sort], colVFDMid) + u.px(12)
		extra := u.filter.Active()
		if u.filter.Genre != "" {
			extra--
		}
		if extra > 0 {
			c.Text(u.fTiny, x, hy, fmt.Sprintf("%d FILTER%s", extra, map[bool]string{true: "S", false: ""}[extra > 1]), colDeckAmb)
		}
		if u.refreshing {
			c.TextRight(u.fTiny, c.W-u.px(14), hy, u.fTiny.Ellipsize(u.refreshLabel(), u.px(230)), colDeckAmb)
		} else {
			r := c.W - u.px(14)
			c.TextRight(u.fTiny, r, hy, "TAPES", colVFDDim)
			cnt := fmt.Sprintf("%03d", min(999, n))
			dw, dh, t := u.px(8), u.px(14), max(1, u.px(2))
			c.Seg7(r-u.fTiny.Width("TAPES ")-Seg7Width(dw, t, cnt), hy, dw, dh, t, cnt, colVFD, colGlass)
		}
	}

	if n == 0 {
		cx := lw / 2
		title, sub := "NOTHING MATCHES", "START TO LOOSEN THE FILTERS"
		if u.search.on {
			title, sub = u.searchEmpty()
			cx = c.W / 2
		} else if len(u.lib.Movies) == 0 {
			title, sub = "NO TAPE", "START, THEN REFRESH GUIDE, TO LOAD IT"
			if u.refreshing {
				sub = "LOADING THE GUIDE…"
			}
		}
		c.TextGlow(u.fBig, cx-u.fBig.Width(title)/2, top+u.px(60), title, colVFD)
		c.TextCenter(u.fTiny, cx, top+u.px(96), sub, colVFDMid)
	}
	dw, dh, t := u.px(10), u.px(18), max(1, u.px(2))
	for i := 0; i < rows && sc+i < n; i++ {
		m := u.visible[sc+i]
		y := top + i*rh
		sel := sc+i == u.sel
		if sel {
			c.Fill(u.px(10), y-u.px(2), lw-u.px(14), rh-u.px(1), colGlass2)
			c.Text(u.fBody, u.px(14), y+u.px(2), "▶", colDeckAmb)
		}
		scol := scoreCol(m.RT, sel)
		sx := u.px(34)
		digits := fmt.Sprintf("%3s", rtDigits(m.RT))
		ex := c.Seg7(sx, y+u.px(4), dw, dh, t, digits, scol, colGlass)
		c.Text(u.fTiny, ex, y+u.px(9), "%", scol)
		tx := u.px(100)
		rx := lw - u.px(14)
		if r, ok := u.inRadarr(m); ok {
			mk := "○"
			if r.HasFile {
				mk = "●"
			}
			rx -= u.fTiny.Width(mk)
			c.Text(u.fTiny, rx, y+u.px(8), mk, colDeckAmb)
			rx -= u.px(8)
		}
		if m.HasDetails() && !m.Streaming() && len(m.Stores) > 0 {
			rx -= u.fTiny.Width("$")
			c.Text(u.fTiny, rx, y+u.px(8), "$", colVFDDim)
			rx -= u.px(8)
		}
		title := u.fBody.Ellipsize(strings.ToUpper(m.Title), rx-tx)
		if sel {
			c.TextGlow(u.fBody, tx, y+u.px(2), title, colVFD)
		} else {
			c.Text(u.fBody, tx, y+u.px(2), title, mix(colVFD, colGlass, 0.25))
		}
	}
	if n > rows {
		tx := lw - u.px(4)
		th := rows * rh
		c.Fill(tx, top, u.px(2), th, colVFDDim)
		bh := max(u.px(12), th*rows/n)
		by := top + (th-bh)*sc/max(1, n-rows)
		c.Fill(tx, by, u.px(2), bh, colVFD)
	}
	if m := u.selected(); m != nil {
		u.drawSidePanel(c, m, lw+u.px(8))
	}
	items := [][2]string{{"A", "INFO"}}
	items = append(items, u.radarrKey(nil, false)...)
	if u.search.on {
		items = append(items, [2]string{"SEL", "NEW SEARCH"}, [2]string{"B", "GUIDE"})
	} else {
		items = append(items, [2]string{"Y", "SORT"}, [2]string{"ST", "MENU"}, [2]string{"SEL", "SEARCH"}, [2]string{"B", "⏏ EJECT"})
	}
	u.keys(c, items)
}

func (u *UI) drawSidePanel(c *Canvas, m *Movie, x int) {
	y := u.topH() + u.px(32)
	pw, ph := u.listPosterBox()
	bz := max(2, u.px(4))
	c.Chrome(x, y, pw+2*bz, ph+2*bz, bz)
	img, failed := u.listPoster(m, false)
	if img != nil {
		c.Poster(img, x+bz, y+bz, pw, ph)
	} else {
		u.noPoster(c, m, x+bz, y+bz, pw, ph, failed)
	}
	sx := x + pw + 2*bz + u.px(10)
	sw := c.W - u.px(12) - sx
	sy := y + u.px(2)
	c.Tomato(sx, sy, u.px(13), m.RT)
	c.Text(u.fTiny, sx+u.px(17), sy, u.fTiny.Ellipsize("TOMATOMETER", sw-u.px(17)), colVFDDim)
	sy += u.fTiny.height + u.px(6)
	bdw, bdh, bt := u.px(20), u.px(38), max(2, u.px(4))
	ex := c.Seg7(sx, sy, bdw, bdh, bt, rtDigits(m.RT), scoreCol(m.RT, true), colGlass)
	if m.RT >= 0 {
		c.Text(u.fSmall, ex, sy+bdh-u.fSmall.height, "%", scoreCol(m.RT, true))
	}
	sy += bdh + u.px(8)
	v := 0.0
	if m.RT >= 0 {
		v = float64(m.RT) / 100
	}
	c.SegBar(sx, sy, sw, u.px(8), 10, v, scoreCol(m.RT, true))
	sy += u.px(16)
	line := func(k, v string, col color.RGBA) {
		c.Text(u.fTiny, sx, sy, k, colVFDDim)
		c.Text(u.fTiny, sx+u.px(40), sy, u.fTiny.Ellipsize(v, sw-u.px(40)), col)
		sy += u.fTiny.height + u.px(3)
	}
	imdb, meta := "--", "--"
	if m.IMDb > 0 {
		imdb = fmt.Sprintf("%.1f", m.IMDb)
	}
	if m.Meta >= 0 {
		meta = fmt.Sprint(m.Meta)
	}
	line("IMDB", imdb, colVFD)
	line("META", meta, colVFD)
	line("RATED", orAll(m.Cert, "NR"), colVFD)
	sy += u.px(3)
	names := m.ProviderNames()
	if len(names) == 0 && !m.HasDetails() {
		names = []string{"…"}
	}
	show, pcol := 2, colDeckAmb
	if len(names) == 0 && m.HasDetails() {
		if len(m.Stores) == 0 {
			c.Text(u.fTiny, sx, sy, "NOT STREAMING", colVFDDim)
			sy += u.fTiny.height + u.px(2)
		} else {
			c.Text(u.fTiny, sx, sy, "RENT / BUY", colVFDDim)
			sy += u.fTiny.height + u.px(2)
			names = m.StoreNames()
			show, pcol = 1, colVFD
		}
	}
	for i, p := range names {
		if i == show && len(names) > show {
			c.Text(u.fTiny, sx, sy, fmt.Sprintf("+%d MORE", len(names)-show), colVFDDim)
			sy += u.fTiny.height + u.px(2)
			break
		}
		c.Text(u.fTiny, sx, sy, u.fTiny.Ellipsize(strings.ToUpper(p), sw), pcol)
		sy += u.fTiny.height + u.px(2)
	}
	if r, ok := u.inRadarr(m); ok {
		s := "○ IN RADARR"
		if r.HasFile {
			s = "● IN LIBRARY"
		}
		c.Text(u.fTiny, sx, sy+u.px(2), s, colDeckAmb)
	}

	// title and notes under the poster
	w := c.W - x - u.px(12)
	ty := y + ph + 2*bz + u.px(10)
	bottom := u.contentBot() - u.px(6)
	for i, l := range u.fSmall.Wrap(strings.ToUpper(m.Title), w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fSmall, x, ty, l, colVFD)
		ty += u.fSmall.height + u.px(1)
	}
	meta2 := m.metaLine()
	if len(m.Genres) > 0 {
		if meta2 != "" {
			meta2 += " · "
		}
		meta2 += strings.ToUpper(strings.Join(m.Genres, " / "))
	}
	c.Text(u.fTiny, x, ty+u.px(2), u.fTiny.Ellipsize(meta2, w), colVFDMid)
	ty += u.fTiny.height + u.px(8)
	text := m.Overview
	if len(m.Cast) > 0 {
		var names []string
		for i, cm := range m.Cast {
			if i == 2 {
				break
			}
			names = append(names, cm.Name)
		}
		text = "WITH " + strings.ToUpper(strings.Join(names, ", ")) + "\n" + text
	}
	for i, l := range u.fTiny.Wrap(text, w) {
		if ty+u.fTiny.height > bottom {
			break
		}
		col := mix(colVFD, colGlass, 0.38)
		if i == 0 && len(m.Cast) > 0 {
			col = colVFDMid
		}
		c.Text(u.fTiny, x, ty, l, col)
		ty += u.fTiny.height + u.px(1)
	}
}

// noPoster: static while the poster tunes in, or a blank sleeve with the title.
func (u *UI) noPoster(c *Canvas, m *Movie, x, y, w, h int, failed bool) {
	if !failed {
		c.Noise(x, y, w, h, uint32(u.frame/2))
		lw := u.fSmall.Width("TUNING") + u.px(12)
		c.Fill(x+(w-lw)/2, y+h/2-u.px(12), lw, u.px(24), colGlass)
		c.TextCenter(u.fSmall, x+w/2, y+h/2-u.px(9), "TUNING", colVFD)
		return
	}
	c.Fill(x, y, w, h, colGlass2)
	ly := y + u.px(14)
	for i, l := range u.fSmall.Wrap(strings.ToUpper(m.Title), w-u.px(16)) {
		if i == 4 {
			break
		}
		c.TextCenter(u.fSmall, x+w/2, ly, l, colVFDMid)
		ly += u.fSmall.height
	}
}

// ---------- details ----------

func (u *UI) drawDetail(c *Canvas) {
	m := u.cur
	pw, ph := u.detailPosterBox()
	bz := max(2, u.px(4))
	fx, fy := u.px(16), u.topH()+u.px(12)
	c.Chrome(fx, fy, pw+2*bz, ph+2*bz, bz)
	img, failed := u.image(u.env.ImageURL("w342", m.Poster), pw, ph, true, false)
	if img != nil {
		c.Poster(img, fx+bz, fy+bz, pw, ph)
	} else {
		u.noPoster(c, m, fx+bz, fy+bz, pw, ph, failed)
	}

	x := fx + pw + 2*bz + u.px(18)
	w := c.W - x - u.px(16)
	y := fy - u.px(2)
	for i, l := range u.fBig.Wrap(strings.ToUpper(m.Title), w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBig, x, y, l, colVFD)
		y += u.fBig.height + u.px(2)
	}
	meta := m.metaLine()
	if len(m.Genres) > 0 {
		if meta != "" {
			meta += " · "
		}
		meta += strings.ToUpper(strings.Join(m.Genres, " / "))
	}
	c.Text(u.fSmall, x, y+u.px(2), u.fSmall.Ellipsize(meta, w), colVFDMid)
	y += u.fSmall.height + u.px(10)

	// score readout
	bh := u.px(90)
	c.Border(x, y, w, bh, 1, colVFDDim)
	c.Tomato(x+u.px(10), y+u.px(6), u.px(13), m.RT)
	c.Text(u.fTiny, x+u.px(28), y+u.px(6), "TOMATOMETER", colVFDDim)
	dw, dh, t := u.px(24), u.px(46), max(2, u.px(5))
	scol := scoreCol(m.RT, true)
	ex := c.Seg7(x+u.px(10), y+u.px(30), dw, dh, t, rtDigits(m.RT), scol, colGlass)
	lbl := ""
	switch {
	case m.RT >= 60:
		lbl = "% FRESH"
	case m.RT >= 0:
		lbl = "% ROTTEN"
	case u.env.Config.OMDbKey == "":
		lbl = "NEEDS OMDB KEY"
	default:
		lbl = "NO SCORE YET"
	}
	c.Text(u.fBody, ex+u.px(4), y+u.px(30)+dh-u.fBody.height, lbl, scol)
	ox := x + w*62/100
	oy := y + u.px(12)
	sdw, sdh, st := u.px(10), u.px(18), max(1, u.px(2))
	other := func(k, v string) {
		c.Text(u.fTiny, ox, oy+u.px(2), k, colVFDDim)
		c.Seg7(ox+u.px(48), oy, sdw, sdh, st, v, colVFD, colGlass)
		oy += u.px(26)
	}
	imdb, mscore, tm := "--", "--", "--"
	if m.IMDb > 0 {
		imdb = fmt.Sprintf("%.1f", m.IMDb)
	}
	if m.Meta >= 0 {
		mscore = fmt.Sprint(m.Meta)
	}
	if m.VoteCount > 0 {
		tm = fmt.Sprintf("%.1f", m.VoteAvg)
	}
	other("IMDB", imdb)
	other("META", mscore)
	other("TMDB", tm)
	y += bh + u.px(12)

	kv := func(k, v string, col color.RGBA) {
		if v == "" {
			return
		}
		c.Text(u.fTiny, x, y+u.px(2), k, colVFDDim)
		c.Text(u.fSmall, x+u.px(86), y, u.fSmall.Ellipsize(v, w-u.px(86)), col)
		y += u.fSmall.height + u.px(5)
	}
	var cast []string
	for i, cm := range m.Cast {
		if i == 3 {
			break
		}
		cast = append(cast, cm.Name)
	}
	kv("DIRECTOR", strings.Join(m.Directors, ", "), colVFD)
	kv("STARRING", strings.Join(cast, ", "), colVFD)
	kv("STREAMING", strings.ToUpper(strings.Join(m.ProviderNames(), " · ")), colDeckAmb)
	if !m.Streaming() {
		kv("RENT / BUY", strings.ToUpper(strings.Join(m.StoreNames(), " · ")), colVFD)
	}
	if !m.Released.IsZero() {
		kv("DIGITAL", strings.ToUpper(m.Released.Format("Jan 2, 2006")), colVFD)
	}
	if m.Arrived() {
		kv("ARRIVED", strings.ToUpper(m.OnSince.Format("Jan 2, 2006")), colVFD)
	}
	if st, col := u.radarrStatus(m); st != "" {
		kv("RADARR", st, col)
	}

	// synopsis, full width, scrollable
	ny := max(y+u.px(4), fy+ph+2*bz+u.px(10))
	nx, nw := u.px(16), c.W-u.px(32)
	bottom := u.contentBot() - u.px(6)
	var lines []string
	var colors []color.RGBA
	add := func(s string, col color.RGBA) {
		for _, l := range u.fSmall.Wrap(s, nw) {
			lines = append(lines, l)
			colors = append(colors, col)
		}
	}
	if m.Tagline != "" {
		add("\""+m.Tagline+"\"", colDeckAmb)
	}
	add(orAll(m.Overview, "No synopsis on file."), mix(colVFD, colGlass, 0.2))
	if m.Awards != "" {
		add("★ "+m.Awards, colDeckAmb)
	}
	if len(m.Cast) > 0 {
		add("", colVFD)
		add("CAST", colVFDDim)
		for _, cm := range m.Cast {
			s := cm.Name
			if cm.Role != "" {
				s += " … " + cm.Role
			}
			add(s, colVFDMid)
		}
	}
	if len(m.Writers) > 0 {
		add("WRITTEN BY "+strings.Join(m.Writers, ", "), colVFDMid)
	}
	add("Data: TMDB · streaming info: JustWatch · scores: OMDb", colVFDDim)
	lh := u.fSmall.height + u.px(1)
	maxLines := max(1, (bottom-ny)/lh)
	if u.detailScrl > max(0, len(lines)-maxLines) {
		u.detailScrl = max(0, len(lines)-maxLines)
	}
	ly := ny
	for i := u.detailScrl; i < len(lines) && i-u.detailScrl < maxLines; i++ {
		c.Text(u.fSmall, nx, ly, lines[i], colors[i])
		ly += lh
	}
	if len(lines) > maxLines {
		c.TextRight(u.fTiny, c.W-u.px(14), bottom-u.fTiny.height, "↕", colVFDDim)
	}
	label := "▶ TRAILER"
	if m.Trailer == nil {
		label = "▶ STILLS"
	}
	items := [][2]string{{"A", label}}
	items = append(items, u.radarrKey(m, true)...)
	items = append(items, [2]string{"L1", "◀◀"}, [2]string{"R1", "▶▶"}, [2]string{"B", "⏏ EJECT"})
	u.keys(c, items)
}

// ---------- trailer reel ----------

func (u *UI) drawTrailer(c *Canvas) {
	m := u.cur
	tw, th := u.tvBox()
	bx, by := u.px(12), u.topH()+u.px(10)
	bezel := u.px(8)
	c.Fill(bx, by, tw+2*bezel, th+2*bezel, rgb(0x14, 0x15, 0x18))
	c.Border(bx, by, tw+2*bezel, th+2*bezel, 1, colEngrave)
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
		c.Fill(ix, iy, tw, th, colOSDBlue)
		c.OSD(u.fOSD, ix+tw/2-u.fOSD.Width("NO STILLS")/2, iy+th/2-u.px(16), "NO STILLS", colWhite)
	default:
		c.Noise(ix, iy, tw, th, uint32(u.frame))
	}
	c.Scanlines(ix, iy, tw, th, 0.22)
	if t < 9 && n > 1 && !u.paused {
		band := iy + (th+u.px(40))*t/9 - u.px(40)
		c.Tracking(ix, iy, tw, th, band, u.px(26), uint32(u.frame))
	}
	osd := colWhite
	if u.paused {
		c.OSD(u.fOSD, ix+u.px(14), iy+u.px(8), "PAUSE", osd)
		c.PauseIcon(ix+u.px(18)+u.fOSD.Width("PAUSE"), iy+u.px(15), u.px(16), osd)
	} else {
		c.OSD(u.fOSD, ix+u.px(14), iy+u.px(8), "PLAY", osd)
		c.PlayIcon(ix+u.px(20)+u.fOSD.Width("PLAY")+u.px(2), iy+u.px(16), u.px(16), colBlack)
		c.PlayIcon(ix+u.px(18)+u.fOSD.Width("PLAY"), iy+u.px(14), u.px(16), osd)
	}
	c.OSD(u.fOSD, ix+tw-u.px(14)-u.fOSD.Width("SP"), iy+u.px(8), "SP", osd)
	secs := u.playFrames / 20
	c.OSD(u.fOSD, ix+u.px(14), iy+th-u.px(36), fmt.Sprintf("0:%02d:%02d", secs/60%60, secs%60), osd)
	if n > 0 {
		s := fmt.Sprintf("%d/%d", u.still+1, n)
		c.OSD(u.fOSD, ix+tw-u.px(14)-u.fOSD.Width(s), iy+th-u.px(36), s, osd)
	}

	ty := by + th + 2*bezel + u.px(10)
	twid := tw + 2*bezel
	c.TextGlow(u.fBig, bx, ty, u.fBig.Ellipsize(strings.ToUpper(m.Title), twid), colVFD)
	ty += u.fBig.height + u.px(4)
	c.Text(u.fSmall, bx, ty, u.fSmall.Ellipsize(m.metaLine()+"   ·   STILL REEL FROM TMDB", twid), colVFDMid)
	ty += u.fSmall.height + u.px(6)
	var lines []string
	var cols []color.RGBA
	if m.Tagline != "" {
		for _, l := range u.fSmall.Wrap("\""+m.Tagline+"\"", twid) {
			lines, cols = append(lines, l), append(cols, colDeckAmb)
		}
	}
	for _, l := range u.fTiny.Wrap(m.Overview, twid) {
		lines, cols = append(lines, l), append(cols, mix(colVFD, colGlass, 0.38))
	}
	for i, l := range lines {
		f := u.fTiny
		if cols[i] == colDeckAmb {
			f = u.fSmall
		}
		if ty+f.height > u.contentBot()-u.px(4) {
			break
		}
		c.Text(f, bx, ty, l, cols[i])
		ty += f.height + u.px(1)
	}

	// QR to the real trailer
	qx := bx + twid + u.px(14)
	qw := c.W - qx - u.px(12)
	qh := th + 2*bezel
	c.Fill(qx, by, qw, qh, colGlass2)
	c.Border(qx, by, qw, qh, 1, colVFDDim)
	url, head, sub := "", "SCAN FOR TRAILER", "PLAYS ON YOUR PHONE"
	if m.Trailer != nil {
		url = m.Trailer.URL()
	} else {
		url = fmt.Sprintf("https://www.themoviedb.org/movie/%d", m.ID)
		head, sub = "NO TRAILER ON FILE", "SCAN FOR THE TMDB PAGE"
	}
	c.TextCenter(u.fSmall, qx+qw/2, by+u.px(10), head, colVFD)
	mod := max(2, u.px(4))
	for mod > 2 && QRSize(url, mod) > qw-u.px(16) {
		mod--
	}
	qs := QRSize(url, mod)
	c.QR(qx+(qw-qs)/2, by+u.px(34), mod, url)
	ly := by + u.px(34) + qs + u.px(8)
	short := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "www.")
	c.TextCenter(u.fTiny, qx+qw/2, ly, u.fTiny.Ellipsize(short, qw-u.px(8)), colVFD)
	ly += u.fTiny.height + u.px(2)
	c.TextCenter(u.fTiny, qx+qw/2, ly, sub, colVFDMid)
	if m.Trailer != nil && m.Trailer.Name != "" {
		ly += u.fTiny.height + u.px(6)
		for i, l := range u.fTiny.Wrap(strings.ToUpper(m.Trailer.Name), qw-u.px(12)) {
			if i == 2 || ly+u.fTiny.height > by+qh-u.px(4) {
				break
			}
			c.TextCenter(u.fTiny, qx+qw/2, ly, l, colDeckAmb)
			ly += u.fTiny.height
		}
	}
	act := "❚❚ PAUSE"
	if u.paused {
		act = "▶ PLAY"
	}
	items := [][2]string{{"A", act}, {"◀▶", "STILL"}, {"L1·R1", "TAPE"}}
	items = append(items, u.radarrKey(m, false)...)
	u.keys(c, append(items, [2]string{"B", "■ STOP"}))
}

// ---------- overlays ----------

// panel dims the screen and draws a raised glass box with a coloured top edge.
func (u *UI) panel(c *Canvas, w, h int, accent color.RGBA) (int, int) {
	c.Blend(0, u.topH(), c.W, u.contentBot()-u.topH(), colBlack, 0.72)
	x, y := (c.W-w)/2, u.topH()+(u.contentBot()-u.topH()-h)/2
	c.Fill(x, y, w, h, colGlass2)
	c.Border(x, y, w, h, 1, mix(accent, colGlass, 0.4))
	c.Fill(x, y, w, max(2, u.px(3)), accent)
	return x, y
}

func (u *UI) drawFilter(c *Canvas) {
	rh := u.fSmall.height + u.px(10)
	w, h := c.W*82/100, u.px(84)+fRows*rh
	x, y := u.panel(c, w, h, colVFD)
	c.TextGlow(u.fBig, x+u.px(18), y+u.px(12), "MENU", colVFD)
	c.Text(u.fTiny, x+u.px(24)+u.fBig.Width("MENU"), y+u.px(18), "SORT & FILTER THE SHELF", colVFDDim)
	ry := y + u.px(46)
	vx := x + u.px(186)
	for i := 0; i < fRows; i++ {
		sel := i == u.filterRow
		if sel {
			c.Fill(x+u.px(10), ry-u.px(3), w-u.px(20), rh-u.px(2), mix(colGlass2, colVFD, 0.10))
			c.Text(u.fSmall, x+u.px(14), ry, "▶", colDeckAmb)
		}
		lc := colVFDMid
		if sel {
			lc = colVFD
		}
		c.Text(u.fSmall, x+u.px(32), ry, filterLabels[i], lc)
		val := u.filterValue(i)
		vc := colVFD
		if i == fRowReset || i == fRowRefresh {
			vc = colVFDDim
			if sel {
				vc = colDeckAmb
			}
		} else {
			ac := colVFDDim
			if sel {
				ac = colDeckAmb
			}
			c.Text(u.fSmall, vx, ry, "◀", ac)
			c.Text(u.fSmall, x+w-u.px(28), ry, "▶", ac)
		}
		c.Text(u.fSmall, vx+u.px(22), ry, u.fSmall.Ellipsize(val, w-u.px(240)), vc)
		ry += rh
	}
	c.Text(u.fSmall, x+u.px(32), y+h-u.px(28), fmt.Sprintf("%d OF %d TAPES MATCH", len(u.visible), len(u.lib.Movies)), colDeckAmb)
	c.TextRight(u.fTiny, x+w-u.px(16), y+h-u.px(26), "B DONE", colVFDMid)
}

func (u *UI) drawMsg(c *Canvas) {
	w := c.W * 84 / 100
	col := colVFD
	switch u.msgCol {
	case colMagenta, colRed:
		col = colRec
	case colAmber:
		col = colDeckAmb
	}
	var lines []string
	for _, para := range u.msgBody {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, u.fSmall.Wrap(para, w-u.px(40))...)
	}
	maxLines := (u.contentBot() - u.topH() - u.px(110)) / (u.fSmall.height + u.px(2))
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	h := u.px(92) + len(lines)*(u.fSmall.height+u.px(2))
	x, y := u.panel(c, w, h, col)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(14), u.msgTitle, col)
	ly := y + u.px(50)
	for _, l := range lines {
		c.Text(u.fSmall, x+u.px(20), ly, l, colVFD)
		ly += u.fSmall.height + u.px(2)
	}
	c.Text(u.fTiny, x+u.px(20), y+h-u.px(26), "A OK", colVFDMid)
}

// ---------- setup, boot, goodbye ----------

func (u *UI) drawSetup(c *Canvas) {
	y := u.topH() + u.px(12)
	c.TextGlow(u.fBig, u.px(16), y, "NO TAPE", colVFD)
	c.Text(u.fSmall, u.px(16)+u.fBig.Width("NO TAPE")+u.px(14), y+u.px(3), "REWIND NEEDS TWO FREE API KEYS", colVFDMid)
	y += u.fBig.height + u.px(10)
	colW := (c.W - u.px(44)) / 2
	bh := u.px(206)
	block := func(x int, n, title, need, url, short string) {
		c.Fill(x, y, colW, bh, colGlass2)
		c.Border(x, y, colW, bh, 1, colVFDDim)
		c.Seg7(x+u.px(10), y+u.px(8), u.px(12), u.px(22), max(2, u.px(3)), n, colDeckAmb, colGlass2)
		c.Text(u.fSmall, x+u.px(38), y+u.px(6), title, colVFD)
		c.Text(u.fTiny, x+u.px(38), y+u.px(6)+u.fSmall.height, need, colVFDMid)
		mod := u.px(4)
		qs := QRSize(url, mod)
		for mod > 2 && qs > u.px(136) {
			mod--
			qs = QRSize(url, mod)
		}
		c.QR(x+(colW-qs)/2, y+u.px(48), mod, url)
		c.TextCenter(u.fTiny, x+colW/2, y+u.px(48)+qs+u.px(6), short, colVFD)
	}
	block(u.px(16), "1", "TMDB API KEY", "REQUIRED · MOVIES, CAST, TRAILERS", "https://www.themoviedb.org/settings/api", "themoviedb.org/settings/api")
	block(u.px(28)+colW, "2", "OMDB API KEY", "FOR ROTTEN TOMATOES SCORES", "https://www.omdbapi.com/apikey.aspx", "omdbapi.com/apikey.aspx")
	y += bh + u.px(10)
	c.Text(u.fSmall, u.px(16), y, "Put them in App/Rewind/data/config.json on the SD card:", colVFD)
	y += u.fSmall.height + u.px(4)
	c.Fill(u.px(16), y, c.W-u.px(32), u.px(42), colBlack)
	c.Text(u.fTiny, u.px(26), y+u.px(5), `"tmdb_api_key": "your TMDB key or read access token",`, colDeckAmb)
	c.Text(u.fTiny, u.px(26), y+u.px(5)+u.fTiny.height+u.px(2), `"omdb_api_key": "your OMDb key",`, colDeckAmb)
	u.keys(c, [][2]string{{"A", "RELOAD CONFIG"}, {"B", "⏏ EJECT"}})
}

func (u *UI) drawBoot(c *Canvas) {
	cx := c.W / 2
	tw, th := u.px(300), u.px(166)
	ty := u.topH() + u.px(30)
	prog := 0.0
	if u.prog.Total > 0 {
		prog = float64(u.prog.Done) / float64(u.prog.Total)
	}
	angle := -u.angle() * 1.6
	if u.bootErr != "" {
		angle = 0
	}
	c.VHSTape(cx-tw/2, ty, tw, th, "BE KIND, REWIND", u.fBody, angle, 1-prog, colLabelY)
	y := ty + th + u.px(24)
	if u.bootErr != "" {
		c.TextGlow(u.fBig, cx-u.fBig.Width("TRACKING ERROR")/2, y, "TRACKING ERROR", colRec)
		for i, l := range u.fSmall.Wrap(u.bootErr, c.W-u.px(80)) {
			if i == 3 {
				break
			}
			c.TextCenter(u.fSmall, cx, y+u.px(30)+i*(u.fSmall.height+u.px(2)), l, colVFD)
		}
		u.keys(c, [][2]string{{"A", "◀◀ RETRY"}, {"B", "⏏ EJECT"}})
		return
	}
	msg := "◀◀ " + u.prog.Phase
	if u.prog.Total > 0 {
		msg += fmt.Sprintf("  %d/%d", u.prog.Done, u.prog.Total)
	}
	c.TextCenter(u.fSmall, cx, y, msg, colVFD)
	c.SegBar(cx-u.px(150), y+u.px(26), u.px(300), u.px(10), 24, prog, colVFD)
	c.TextCenter(u.fTiny, cx, y+u.px(48), "DATA: TMDB · JUSTWATCH · OMDB   ·   v"+version, colVFDDim)
	u.keys(c, [][2]string{{"B", "⏏ EJECT"}})
}

func (u *UI) drawBye(c *Canvas) {
	c.Fill(0, 0, c.W, c.H, colOSDBlue)
	cx := c.W / 2
	c.OSD(u.fOSD, u.px(30), u.px(30), "REW", colWhite)
	c.OSD(u.fOSD, u.px(34)+u.fOSD.Width("REW "), u.px(30), "◀◀", colWhite)
	vw, vh := u.px(240), u.px(134)
	c.VHSTape(cx-vw/2, c.H/2-vh/2-u.px(20), vw, vh, "BE KIND, REWIND", u.fSmall, -float64(u.frame)*0.9, 0.2+float64(u.frame%14)/40, colLabelY)
	c.OSD(u.fOSD, cx-u.fOSD.Width("BE KIND, PLEASE REWIND")/2, c.H/2+vh/2, "BE KIND, PLEASE REWIND", colWhite)
	c.Scanlines(0, 0, c.W, c.H, 0.12)
	c.Tracking(0, 0, c.W, c.H, (u.frame*37)%c.H, u.px(18), uint32(u.frame))
}
