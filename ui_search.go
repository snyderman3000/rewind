package main

// Search: look up any movie by title, old or new, and record it to Radarr.
// SELECT on the shelf opens an on-screen keyboard; the results take over the shelf
// until B goes back to the guide.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var kbRows = []string{"ABCDEFGHIJ", "KLMNOPQRST", "UVWXYZ0123", "456789'&-:"}

// the wide keys on the bottom row, with the columns each one covers
var kbSpecial = []struct {
	label      string
	start, end int
}{{"SPACE", 0, 2}, {"DELETE", 3, 4}, {"CLEAR", 5, 6}, {"SEARCH", 7, 9}}

const maxRecent = 5

type keyboardState struct {
	text     string
	row, col int // row len(kbRows) is the special row; rows after it are recent searches
	back     screen
}

type searchState struct {
	on      bool // the shelf shows search results instead of the guide
	query   string
	results []*Movie
	busy    bool
	err     string
	prog    Progress
	cancel  context.CancelFunc
	recent  []string
	guideID int // the guide movie that was selected before searching
	loaded  bool
}

func (env *Env) searchesPath() string { return filepath.Join(env.DataDir, "searches.json") }

func (u *UI) loadRecent() {
	if u.search.loaded {
		return
	}
	u.search.loaded = true
	if b, err := os.ReadFile(u.env.searchesPath()); err == nil {
		json.Unmarshal(b, &u.search.recent)
	}
	if len(u.search.recent) > maxRecent {
		u.search.recent = u.search.recent[:maxRecent]
	}
}

func (u *UI) addRecent(q string) {
	out := []string{q}
	for _, r := range u.search.recent {
		if !strings.EqualFold(r, q) && len(out) < maxRecent {
			out = append(out, r)
		}
	}
	u.search.recent = out
	b, _ := json.Marshal(out)
	os.WriteFile(u.env.searchesPath(), b, 0o644)
}

func (u *UI) openKeyboard() {
	u.loadRecent()
	text := ""
	if u.search.on {
		text = u.search.query
	}
	u.kb = keyboardState{text: text, back: u.scr}
	u.scr = scrKeyboard
}

func (u *UI) kbRowCount() int { return len(kbRows) + 1 + len(u.search.recent) }

func specialAt(col int) int {
	for i, k := range kbSpecial {
		if col >= k.start && col <= k.end {
			return i
		}
	}
	return 0
}

func (u *UI) keyKeyboard(k int) {
	kb := &u.kb
	rows := u.kbRowCount()
	switch k {
	case BtnUp:
		kb.row = (kb.row - 1 + rows) % rows
	case BtnDown:
		kb.row = (kb.row + 1) % rows
	case BtnLeft, BtnRight:
		d := 1
		if k == BtnLeft {
			d = -1
		}
		switch {
		case kb.row < len(kbRows):
			kb.col = (kb.col + d + 10) % 10
		case kb.row == len(kbRows):
			i := (specialAt(kb.col) + d + len(kbSpecial)) % len(kbSpecial)
			kb.col = kbSpecial[i].start
		}
	case BtnA:
		switch {
		case kb.row < len(kbRows):
			u.kbType(string(kbRows[kb.row][kb.col]))
		case kb.row == len(kbRows):
			switch kbSpecial[specialAt(kb.col)].label {
			case "SPACE":
				u.kbType(" ")
			case "DELETE":
				u.kbDelete()
			case "CLEAR":
				kb.text = ""
			case "SEARCH":
				u.runSearch(kb.text)
			}
		default:
			u.runSearch(u.search.recent[kb.row-len(kbRows)-1])
		}
	case BtnX:
		u.kbType(" ")
	case BtnB:
		if kb.text == "" {
			u.scr = kb.back
			return
		}
		u.kbDelete()
	case BtnY:
		kb.text = ""
	case BtnStart:
		u.runSearch(kb.text)
	case BtnSelect:
		u.scr = kb.back
	}
}

func (u *UI) kbType(s string) {
	t := u.kb.text
	if len(t) >= 40 || (s == " " && (t == "" || strings.HasSuffix(t, " "))) {
		return
	}
	u.kb.text += s
}

func (u *UI) kbDelete() {
	if n := len(u.kb.text); n > 0 {
		u.kb.text = u.kb.text[:n-1]
	}
}

// runSearch puts the results for q on the shelf.
func (u *UI) runSearch(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		u.showToast("TYPE A MOVIE'S NAME FIRST")
		return
	}
	if u.env.Config.TMDBKey == "" {
		u.showToast("NO TMDB KEY IN CONFIG.JSON")
		return
	}
	if !u.search.on {
		u.search.guideID = 0
		if m := u.selected(); m != nil {
			u.search.guideID = m.ID
		}
	}
	if u.search.cancel != nil {
		u.search.cancel()
	}
	u.addRecent(q)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	u.search = searchState{on: true, query: q, busy: true, cancel: cancel, recent: u.search.recent,
		guideID: u.search.guideID, loaded: true, prog: Progress{Phase: "SEARCHING"}}
	u.scr = scrList
	u.refilter(0)
	go func() {
		ms, err := u.env.SearchResults(ctx, q, func(p Progress) {
			select {
			case u.post <- func() {
				if u.search.query == q {
					u.search.prog = p
				}
			}:
			default:
			}
		})
		u.post <- func() {
			cancel()
			if u.search.query != q || !u.search.on {
				return // searched for something else, or left, since
			}
			u.search.busy = false
			if err != nil {
				u.search.err = netHint(err).Error()
				return
			}
			u.search.results = ms
			if u.cur != nil {
				for _, m := range ms {
					if m.ID == u.cur.ID {
						u.cur = m
					}
				}
			}
			u.refilter(0)
		}
	}()
}

// leaveSearch puts the guide back on the shelf.
func (u *UI) leaveSearch() {
	if u.search.cancel != nil {
		u.search.cancel()
	}
	id := u.search.guideID
	u.search = searchState{recent: u.search.recent, loaded: u.search.loaded}
	u.scr = scrList
	u.refilter(id)
}

func (u *UI) keySearchList(k int) bool {
	switch k {
	case BtnB:
		u.leaveSearch()
	case BtnSelect:
		u.openKeyboard()
	case BtnY, BtnL1, BtnR1, BtnL2, BtnR2, BtnStart:
		u.showToast("SEARCH RESULTS · B FOR THE GUIDE")
	default:
		return false
	}
	return true
}

// ---------- drawing ----------

func (u *UI) drawKeyboard(c *Canvas) {
	top := u.topH() + u.px(10)
	c.TextGlow(u.fBig, u.px(16), top, "SEARCH", colVFD)
	c.Text(u.fTiny, u.px(16)+u.fBig.Width("SEARCH")+u.px(12), top+u.px(6), "ANY MOVIE, OLD OR NEW", colVFDDim)

	// the title being typed, in the VFD window
	y := top + u.fBig.height + u.px(8)
	fx, fw, fh := u.px(14), c.W-u.px(28), u.fLCD.height+u.px(10)
	c.Fill(fx, y, fw, fh, colGlass2)
	c.Border(fx, y, fw, fh, 1, colVFDDim)
	t := u.fLCD.Ellipsize(u.kb.text, fw-u.px(30))
	x := c.Text(u.fLCD, fx+u.px(10), y+u.px(5), t, colVFD)
	if u.blink(6) {
		c.Fill(x+u.px(2), y+u.px(6), u.px(10), u.fLCD.height-u.px(2), colDeckAmb)
	}
	if u.kb.text == "" {
		c.Text(u.fSmall, x+u.px(18), y+u.px(9), "a movie's title, or part of it", colVFDDim)
	}
	y += fh + u.px(10)

	// keys, styled like the deck's buttons
	kw := (c.W - u.px(28)) / 10
	kh := u.px(32)
	key := func(kx, ky, w, h int, label string, f *Font, sel, accent bool) {
		if sel {
			c.Fill(kx, ky, w, h, colVFD)
		} else {
			c.Fill(kx, ky, w, h, colKey)
			c.Fill(kx, ky, w, max(1, u.px(2)), colKeyTop)
		}
		fg := rgb(0xd0, 0xd2, 0xd6)
		switch {
		case sel:
			fg = colGlass
		case accent:
			fg = colDeckAmb
		}
		c.TextCenter(f, kx+w/2, ky+(h-f.height)/2, label, fg)
	}
	for r, row := range kbRows {
		for col, ch := range row {
			sel := u.kb.row == r && u.kb.col == col
			key(u.px(14)+col*kw, y, kw-u.px(4), kh-u.px(4), string(ch), u.fSmall, sel, false)
		}
		y += kh
	}
	for i, k := range kbSpecial {
		sel := u.kb.row == len(kbRows) && specialAt(u.kb.col) == i
		w := (k.end-k.start+1)*kw - u.px(4)
		key(u.px(14)+k.start*kw, y, w, kh-u.px(4), k.label, u.fTiny, sel, k.label == "SEARCH")
	}
	y += kh + u.px(4)

	// recent searches
	if len(u.search.recent) > 0 {
		c.Text(u.fTiny, u.px(16), y, "RECENT", colVFDDim)
		y += u.fTiny.height + u.px(2)
		rh := u.fSmall.height + u.px(5)
		for i, q := range u.search.recent {
			if y+rh > u.contentBot()-u.px(2) {
				break
			}
			sel := u.kb.row == len(kbRows)+1+i
			if sel {
				c.Fill(u.px(10), y-u.px(2), c.W-u.px(20), rh, colGlass2)
				c.Text(u.fSmall, u.px(14), y, "▶", colDeckAmb)
			}
			col := colVFDMid
			if sel {
				col = colVFD
			}
			c.Text(u.fSmall, u.px(34), y, u.fSmall.Ellipsize(strings.ToUpper(q), c.W-u.px(60)), col)
			y += rh
		}
	}
	u.keys(c, [][2]string{{"A", "TYPE"}, {"B", "DELETE"}, {"X", "SPACE"}, {"ST", "SEARCH"}, {"SEL", "CANCEL"}})
}

// searchHeader is the shelf's program line while showing results.
func (u *UI) searchHeader(c *Canvas, hy int) {
	x := c.Text(u.fTiny, u.px(16), hy, "SEARCH ", colVFDDim)
	c.Text(u.fSmall, x, hy-u.px(2), u.fSmall.Ellipsize("\""+strings.ToUpper(u.search.query)+"\"", u.px(300)), colVFD)
	if u.search.busy {
		p := u.search.prog
		s := "◀◀ " + p.Phase
		if p.Total > 0 {
			s = fmt.Sprintf("◀◀ %s %d/%d", p.Phase, p.Done, p.Total)
		}
		c.TextRight(u.fTiny, c.W-u.px(14), hy, s, colDeckAmb)
		return
	}
	r := c.W - u.px(14)
	c.TextRight(u.fTiny, r, hy, "FOUND", colVFDDim)
	cnt := fmt.Sprintf("%02d", min(99, len(u.visible)))
	dw, dh, t := u.px(8), u.px(14), max(1, u.px(2))
	c.Seg7(r-u.fTiny.Width("FOUND ")-Seg7Width(dw, t, cnt), hy, dw, dh, t, cnt, colVFD, colGlass)
}

// searchEmpty is the shelf's message when there are no results to show.
func (u *UI) searchEmpty() (string, string) {
	switch {
	case u.search.busy:
		return "SEARCHING", "LOOKING UP \"" + strings.ToUpper(u.search.query) + "\"…"
	case u.search.err != "":
		return "TRACKING ERROR", strings.ToUpper(shortErr(u.search.err))
	}
	return "NO MATCHES", "SELECT TO TRY ANOTHER TITLE · B FOR THE GUIDE"
}
