package main

// Radarr screens: library markers, the status line on the details screen,
// and the "add to Radarr" confirmation.

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"
)

type radarrUI struct {
	lib      map[int]RadarrMovie // by TMDB id; nil until loaded
	syncing  bool
	syncErr  string
	target   *RadarrTarget
	checking bool // fetching the target for the add dialog
	addErr   string
	adding   bool
	queue    map[int]*RadarrQueue // by TMDB id
	queueAt  map[int]time.Time
}

// radarrSync reloads the list of movies already in Radarr.
func (u *UI) radarrSync() {
	if !u.env.RadarrOn() || u.rad.syncing {
		return
	}
	u.rad.syncing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		lib, err := u.env.RadarrLibrary(ctx)
		u.post <- func() {
			u.rad.syncing = false
			if err != nil {
				u.rad.syncErr = netHint(err).Error()
				u.showToast("RADARR: " + strings.ToUpper(shortErr(u.rad.syncErr)))
				return
			}
			u.rad.syncErr = ""
			u.rad.lib = lib
			if u.filter.Owned > 0 {
				u.keepRefilter()
			}
		}
	}()
}

func (u *UI) keepRefilter() {
	keep := 0
	if m := u.selected(); m != nil {
		keep = m.ID
	}
	u.refilter(keep)
}

// ownedFn is the filter's "already in Radarr" test, or nil when Radarr isn't known.
func (u *UI) ownedFn() func(*Movie) bool {
	if u.rad.lib == nil {
		return nil
	}
	return func(m *Movie) bool { _, ok := u.rad.lib[m.ID]; return ok }
}

func (u *UI) inRadarr(m *Movie) (RadarrMovie, bool) {
	if u.rad.lib == nil {
		return RadarrMovie{}, false
	}
	r, ok := u.rad.lib[m.ID]
	return r, ok
}

// fetchQueue looks up the download progress of a movie Radarr has but hasn't finished.
func (u *UI) fetchQueue(m *Movie) {
	r, ok := u.inRadarr(m)
	if !ok || r.HasFile || r.ID == 0 || time.Since(u.rad.queueAt[m.ID]) < 15*time.Second {
		return
	}
	u.rad.queueAt[m.ID] = time.Now()
	id := m.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		q, err := u.env.RadarrQueueFor(ctx, r.ID)
		if err != nil {
			return
		}
		u.post <- func() { u.rad.queue[id] = q }
	}()
}

// radarrStatus describes where a movie stands in Radarr, for the details screen.
func (u *UI) radarrStatus(m *Movie) (string, color.RGBA) {
	switch {
	case !u.env.RadarrOn():
		return "", colVFDDim
	case u.rad.lib == nil && u.rad.syncing:
		return "CHECKING…", colVFDDim
	case u.rad.lib == nil:
		return "UNREACHABLE", colRec
	}
	r, ok := u.rad.lib[m.ID]
	switch {
	case !ok:
		return "NOT RECORDED · X TO REC", colVFDMid
	case r.HasFile:
		return "● DOWNLOADED · IN YOUR LIBRARY", colVFD
	}
	if q := u.rad.queue[m.ID]; q != nil {
		s := fmt.Sprintf("▼ DOWNLOADING %d%%", q.Percent())
		if t := shortTimeLeft(q.TimeLeft); t != "" {
			s += " · " + t + " LEFT"
		}
		if strings.EqualFold(q.State, "importPending") || strings.EqualFold(q.State, "importing") {
			s = "▼ IMPORTING INTO LIBRARY"
		}
		return s, colDeckAmb
	}
	if !r.Monitored {
		return "○ IN RADARR · NOT MONITORED", colVFDMid
	}
	return "○ WANTED · RADARR IS LOOKING", colDeckAmb
}

// shortTimeLeft turns Radarr's "01:23:45" / "1.02:03:04" into "1H 23M".
func shortTimeLeft(s string) string {
	if s == "" {
		return ""
	}
	days := 0
	if i := strings.Index(s, "."); i > 0 && strings.Count(s, ":") == 2 && i < strings.Index(s, ":") {
		fmt.Sscanf(s[:i], "%d", &days)
		s = s[i+1:]
	}
	var h, m, sec int
	if n, _ := fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec); n < 2 {
		return ""
	}
	h += days * 24
	switch {
	case h > 0:
		return fmt.Sprintf("%dH %02dM", h, m)
	case m > 0:
		return fmt.Sprintf("%dM", m)
	}
	return "<1M"
}

// openAdd shows the add dialog, fetching Radarr's profile and folder first.
func (u *UI) openAdd(m *Movie) {
	if !u.env.RadarrOn() {
		u.message("NO RADARR SET UP", colAmber, "Add radarr_url and radarr_api_key to App/Rewind/data/config.json to send movies to Radarr.\n\nExample: \"radarr_url\": \"http://192.168.1.20:7878\"\n\nThe API key is in Radarr under Settings → General.")
		return
	}
	if r, ok := u.inRadarr(m); ok {
		if r.HasFile {
			u.showToast("ALREADY DOWNLOADED — IT'S IN YOUR LIBRARY")
		} else {
			u.showToast("ALREADY IN RADARR")
		}
		return
	}
	u.modal = modalAdd
	u.rad.addErr = ""
	if u.rad.target != nil || u.rad.checking {
		return
	}
	u.rad.checking = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		t, err := u.env.RadarrTarget(ctx)
		u.post <- func() {
			u.rad.checking = false
			if err != nil {
				u.rad.addErr = netHint(err).Error()
				return
			}
			u.rad.target = t
		}
	}()
}

func (u *UI) keyAdd(k int) {
	switch k {
	case BtnB, BtnX:
		if !u.rad.adding {
			u.modal = modalNone
		}
	case BtnA, BtnStart:
		switch {
		case u.rad.adding || u.rad.checking:
		case u.rad.addErr != "" && u.rad.target == nil:
			u.rad.target = nil
			u.modal = modalNone
			u.openAdd(u.cur) // retry
		case u.rad.target != nil:
			u.doAdd(u.cur, u.rad.target)
		}
	}
}

func (u *UI) doAdd(m *Movie, t *RadarrTarget) {
	u.rad.adding = true
	u.rad.addErr = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		r, err := u.env.RadarrAdd(ctx, m.ID, t)
		u.post <- func() {
			u.rad.adding = false
			if err != nil {
				u.rad.addErr = netHint(err).Error()
				return
			}
			if u.rad.lib == nil {
				u.rad.lib = map[int]RadarrMovie{}
			}
			u.rad.lib[m.ID] = r
			u.modal = modalNone
			if u.filter.Owned > 0 {
				u.keepRefilter()
			}
			switch {
			case r.HasFile:
				u.showToast("ALREADY IN YOUR LIBRARY")
			case t.Search:
				u.showToast("ADDED TO RADARR — SEARCHING NOW")
			default:
				u.showToast("ADDED TO RADARR")
			}
		}
	}()
}

func (u *UI) drawAdd(c *Canvas) {
	m := u.cur
	w, h := c.W*80/100, u.px(196)
	if u.rad.addErr != "" {
		h += u.px(56)
	}
	x, y := u.panel(c, w, h, colRec)
	if u.rad.adding && u.blink(6) || !u.rad.adding {
		c.Circle(x+u.px(28), y+u.px(26), u.px(8), colRec, true, 0)
	}
	c.TextGlow(u.fBig, x+u.px(44), y+u.px(14), "REC TO RADARR", colRec)
	ly := y + u.px(50)
	title := strings.ToUpper(m.Title)
	if m.Year > 0 {
		title += fmt.Sprintf(" (%d)", m.Year)
	}
	for i, l := range u.fBody.Wrap(title, w-u.px(40)) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBody, x+u.px(20), ly, l, colVFD)
		ly += u.fBody.height
	}
	ly += u.px(8)
	kv := func(k, v string, col color.RGBA) {
		c.Text(u.fTiny, x+u.px(20), ly+u.px(2), k, colVFDDim)
		c.Text(u.fSmall, x+u.px(110), ly, u.fSmall.Ellipsize(v, w-u.px(130)), col)
		ly += u.fSmall.height + u.px(4)
	}
	dots := strings.Repeat("·", u.frame/3%4)
	switch {
	case u.rad.checking:
		kv("RADARR", "CONNECTING"+dots, colDeckAmb)
	case u.rad.target != nil:
		t := u.rad.target
		kv("QUALITY", t.Profile.Name, colVFD)
		folder := t.Folder.Path
		if t.Folder.FreeSpace > 0 {
			folder += fmt.Sprintf("  (%s FREE)", humanBytes(t.Folder.FreeSpace))
		}
		kv("FOLDER", folder, colVFD)
		if t.Search {
			kv("THEN", "SEARCH AND DOWNLOAD NOW", colDeckAmb)
		} else {
			kv("THEN", "WAIT FOR RADARR'S NEXT RSS CHECK", colVFD)
		}
	}
	if u.rad.addErr != "" {
		for i, l := range u.fSmall.Wrap("▲ "+u.rad.addErr, w-u.px(40)) {
			if i == 3 {
				break
			}
			c.Text(u.fSmall, x+u.px(20), ly, l, colRec)
			ly += u.fSmall.height + u.px(1)
		}
	}
	by := y + h - u.px(28)
	switch {
	case u.rad.adding:
		c.Text(u.fSmall, x+u.px(20), by, "RECORDING"+dots, colRec)
	case u.rad.target != nil:
		c.Text(u.fTiny, x+u.px(20), by+u.px(2), "A ● REC     B CANCEL", colVFDMid)
	case u.rad.addErr != "":
		c.Text(u.fTiny, x+u.px(20), by+u.px(2), "A RETRY     B CANCEL", colVFDMid)
	default:
		c.Text(u.fTiny, x+u.px(20), by+u.px(2), "B CANCEL", colVFDMid)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.0f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}
