package main

import (
	"os"
	"testing"
)

func writeFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testUI(t *testing.T) *UI {
	env := &Env{DataDir: t.TempDir(), Config: defaultConfig()}
	u := NewUI(env, 640, 480)
	demoScene(u, "list")
	return u
}

// Every scene draws without panicking at the Mini Plus and v4 resolutions.
func TestScenesDraw(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {752, 560}} {
		for _, s := range []string{"boot", "list", "loading", "filter", "family", "detail", "trailer", "trailer-cut", "setup", "bye", "error"} {
			env := &Env{DataDir: t.TempDir(), Config: defaultConfig()}
			c := NewCanvas(size[0], size[1])
			u := NewUI(env, c.W, c.H)
			demoScene(u, s)
			for i := 0; i < 3; i++ {
				u.frame++
				u.Draw(c)
			}
		}
	}
}

func TestListKeys(t *testing.T) {
	u := testUI(t)
	u.sel = 0
	n := len(u.visible)
	u.Key(BtnUp)
	if u.sel != n-1 {
		t.Errorf("up from top should wrap, sel=%d", u.sel)
	}
	u.Key(BtnDown)
	if u.sel != 0 {
		t.Errorf("sel=%d", u.sel)
	}
	u.Key(BtnY)
	if u.filter.Sort != SortNewest {
		t.Errorf("Y should cycle sort, got %v", u.filter.Sort)
	}
	u.Key(BtnR1)
	if u.filter.Genre == "" || len(u.visible) >= n {
		t.Errorf("R1 should pick a genre: %q %d", u.filter.Genre, len(u.visible))
	}
	for _, m := range u.visible {
		if !contains(m.Genres, u.filter.Genre) {
			t.Errorf("%s isn't %s", m.Title, u.filter.Genre)
		}
	}
	u.Key(BtnL1)
	if u.filter.Genre != "" || len(u.visible) != n {
		t.Errorf("L1 should go back to all genres")
	}
	u.Key(BtnX)
	if u.modal != modalFilter {
		t.Fatal("X opens filters")
	}
	u.Key(BtnDown) // genre
	u.Key(BtnDown) // service
	u.Key(BtnDown) // tomatometer
	u.Key(BtnRight)
	for _, m := range u.visible {
		if m.RT < 60 {
			t.Errorf("%s is %d%%", m.Title, m.RT)
		}
	}
	u.Key(BtnUp)
	u.Key(BtnUp)
	u.Key(BtnUp)
	u.Key(BtnUp) // wraps to reset
	if u.filterRow != fRowReset {
		t.Fatalf("row %d", u.filterRow)
	}
	u.Key(BtnA)
	if u.filter.MinRT != 0 || len(u.visible) != n {
		t.Errorf("reset didn't clear")
	}
	u.Key(BtnB)
	if u.modal != modalNone {
		t.Error("B closes")
	}
	u.Key(BtnA)
	if u.scr != scrDetail || u.cur != u.visible[u.sel] {
		t.Fatal("A opens detail")
	}
	before := u.sel
	u.Key(BtnR1)
	if u.cur != u.visible[(before+1)%len(u.visible)] {
		t.Error("R1 next movie")
	}
	u.Key(BtnA)
	if u.scr != scrTrailer {
		t.Fatal("A opens trailer")
	}
	u.Key(BtnA)
	if !u.paused {
		t.Error("A pauses")
	}
	u.Key(BtnRight)
	if u.still != 1 {
		t.Error("right steps stills")
	}
	u.Key(BtnB)
	u.Key(BtnB)
	if u.scr != scrList {
		t.Error("B B back to list")
	}
	u.Key(BtnB)
	if !u.quit {
		t.Error("B on list exits")
	}
}

func TestTrailerReelAdvances(t *testing.T) {
	u := testUI(t)
	u.openTrailer(u.visible[0])
	for i := 0; i < stillFrames+1; i++ {
		u.frame++
		u.Tick()
	}
	if u.still != 1 {
		t.Errorf("still %d after one interval", u.still)
	}
}
