package main

// The Front Panel look: a silver VCR deck with a vacuum-fluorescent display,
// seven-segment readouts, deck keys and a proper VHS tape.

import (
	"image/color"
	"math"
	"strings"
)

var (
	colVFD     = rgb(0x52, 0xf5, 0xd8) // lit VFD segments
	colVFDDim  = rgb(0x1c, 0x5a, 0x50) // labels, unlit indicators
	colVFDMid  = rgb(0x3a, 0xb8, 0xa2) // secondary text
	colGlass   = rgb(0x04, 0x0b, 0x0c) // the dark display glass
	colGlass2  = rgb(0x0a, 0x18, 0x19) // raised glass (selected rows, panels)
	colDeckAmb = rgb(0xff, 0xa8, 0x2a) // amber indicators
	colRec     = rgb(0xff, 0x3a, 0x30) // REC red
	colMetal   = rgb(0xb8, 0xba, 0xbe)
	colMetalDk = rgb(0x7a, 0x7c, 0x82)
	colEngrave = rgb(0x30, 0x32, 0x36)
	colKey     = rgb(0x2a, 0x2b, 0x2f)
	colKeyTop  = rgb(0x55, 0x57, 0x5c)
	colLabelY  = rgb(0xff, 0xe4, 0x4a) // the BE KIND, REWIND label
	colOSDBlue = rgb(0x12, 0x2a, 0xc0)
)

func (c *Canvas) VGrad(x, y, w, h int, top, bot color.RGBA) {
	for i := 0; i < h; i++ {
		c.Fill(x, y+i, w, 1, mix(top, bot, float64(i)/float64(max(1, h-1))))
	}
}

// Metal draws a strip of brushed silver deck panel.
func (c *Canvas) Metal(x, y, w, h int) {
	c.VGrad(x, y, w, h, colMetal, colMetalDk)
	for yy := y; yy < y+h; yy += 2 {
		c.Blend(x, yy, w, 1, colWhite, 0.05)
	}
	c.Fill(x, y, w, 1, rgb(0xe4, 0xe6, 0xea))
	c.Fill(x, y+h-1, w, 1, rgb(0x40, 0x42, 0x46))
}

// Seg7 draws digits (and . : - %) as seven-segment glyphs, unlit segments faintly visible.
// dw×dh is one digit, t the segment thickness. Returns the x after the text.
func (c *Canvas) Seg7(x, y, dw, dh, t int, s string, on, bg color.RGBA) int {
	segs := map[rune]string{'0': "abcdef", '1': "bc", '2': "abged", '3': "abgcd", '4': "fgbc", '5': "afgcd",
		'6': "afgedc", '7': "abc", '8': "abcdefg", '9': "abcdfg", '-': "g", ' ': ""}
	off := mix(bg, on, 0.09)
	glow := mix(bg, on, 0.30)
	for _, r := range s {
		switch r {
		case ':':
			c.Fill(x+t, y+dh/3-t/2, t, t, on)
			c.Fill(x+t, y+dh*2/3-t/2, t, t, on)
			x += t*3 + 1
			continue
		case '.':
			c.Fill(x, y+dh-t, t, t, on)
			x += t * 2
			continue
		case '%':
			c.Fill(x, y, t*2, t*2, on)
			c.Fill(x+dw-t*2, y+dh-t*2, t*2, t*2, on)
			for k := 0; k < max(1, t-1); k++ {
				c.Line(x+dw-1-k, y, x-k, y+dh-1, on)
			}
			x += dw + t*2
			continue
		}
		lit, ok := segs[r]
		if !ok {
			lit = ""
		}
		h2 := dh / 2
		rects := map[byte][4]int{
			'a': {x + t, y, dw - 2*t, t},
			'b': {x + dw - t, y + t, t, h2 - t},
			'c': {x + dw - t, y + h2, t, h2 - t},
			'd': {x + t, y + dh - t, dw - 2*t, t},
			'e': {x, y + h2, t, h2 - t},
			'f': {x, y + t, t, h2 - t},
			'g': {x + t, y + h2 - t/2, dw - 2*t, t},
		}
		for _, k := range []byte("abcdefg") {
			rr := rects[k]
			if strings.IndexByte(lit, k) >= 0 {
				c.Blend(rr[0]-1, rr[1]-1, rr[2]+2, rr[3]+2, glow, 0.6)
				c.Fill(rr[0], rr[1], rr[2], rr[3], on)
			} else {
				c.Fill(rr[0], rr[1], rr[2], rr[3], off)
			}
		}
		x += dw + t*2
	}
	return x
}

// Seg7Width is how wide Seg7 draws s.
func Seg7Width(dw, t int, s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ':':
			w += t*3 + 1
		case '.':
			w += t * 2
		default:
			w += dw + t*2
		}
	}
	return w
}

// SegBar is a row of VFD level segments, lit fraction v.
func (c *Canvas) SegBar(x, y, w, h, n int, v float64, on color.RGBA) {
	gap := max(1, w/(n*5))
	sw := (w - gap*(n-1)) / n
	lit := int(math.Round(v * float64(n)))
	for i := 0; i < n; i++ {
		col := on
		if i >= lit {
			col = mix(colGlass, on, 0.12)
		}
		c.Fill(x+i*(sw+gap), y, sw, h, col)
	}
}

// Chrome frames something (a poster) in a thin silver bezel.
func (c *Canvas) Chrome(x, y, w, h, t int) {
	c.VGrad(x, y, w, h, rgb(0xb0, 0xb2, 0xb8), rgb(0x70, 0x72, 0x78))
	c.Fill(x+t, y+t, w-2*t, h-2*t, colBlack)
}

// VHSTape draws a VHS cassette face-on: flat black shell, wide label, one long
// smoked window with both white toothed hubs, and the tape running between them.
// progress (0..1) moves tape from the left spool to the right.
func (c *Canvas) VHSTape(x, y, w, h int, label string, f *Font, angle, progress float64, labelCol color.RGBA) {
	c.Fill(x, y, w, h, rgb(0x16, 0x16, 0x18))
	e := max(1, h/40)
	c.Fill(x, y, w, e, rgb(0x2c, 0x2c, 0x30))
	c.Fill(x, y+h-e, w, e, rgb(0x08, 0x08, 0x09))
	for yy := y + h*80/100; yy < y+h-3; yy += max(2, h/30) {
		c.Fill(x+w/20, yy, w-w/10, 1, rgb(0x0e, 0x0e, 0x10))
	}
	lx, ly, lw, lh := x+w*8/100, y+h*9/100, w*84/100, h*30/100
	c.Fill(lx, ly, lw, lh, labelCol)
	c.Fill(lx, ly+lh-max(1, lh/8), lw, max(1, lh/8), mix(labelCol, colBlack, 0.12))
	if label != "" && f != nil {
		c.Text(f, lx+lw/24, ly+(lh-f.height)/2, f.Ellipsize(label, lw-lw/12), rgb(0x1a, 0x1a, 0x30))
	}
	wx, wy, ww, wh := x+w*20/100, y+h*48/100, w*60/100, h*30/100
	c.Fill(wx-2, wy-2, ww+4, wh+4, rgb(0x0a, 0x0a, 0x0b))
	c.Fill(wx, wy, ww, wh, rgb(0x24, 0x22, 0x26))
	r := wh * 46 / 100
	cy := wy + wh/2
	hub := max(3, r*42/100)
	for i, cx := range []int{wx + ww*20/100, wx + ww*80/100} {
		frac := 1 - progress
		if i == 1 {
			frac = progress
		}
		spool := hub + int(float64(r-hub)*(0.25+0.75*frac))
		c.Circle(cx, cy, spool, rgb(0x3a, 0x26, 0x18), true, 0)
		c.Circle(cx, cy, hub, rgb(0xe8, 0xe6, 0xe0), true, 0)
		for k := 0; k < 6; k++ {
			a := angle + float64(k)*math.Pi/3
			tx := cx + int(math.Cos(a)*float64(hub)*0.62)
			ty := cy + int(math.Sin(a)*float64(hub)*0.62)
			c.Circle(tx, ty, max(1, hub/5), rgb(0x24, 0x22, 0x26), true, 0)
		}
		c.Circle(cx, cy, max(1, hub/4), rgb(0x24, 0x22, 0x26), true, 0)
	}
	c.Fill(wx+ww*12/100, wy+wh-max(2, wh/8), ww*76/100, max(1, wh/12), rgb(0x3a, 0x26, 0x18))
	ax := x + w*88/100
	for i := 0; i < h/10; i++ {
		c.Fill(ax+i/2, wy+wh/2-h/20+i/2, 1, max(1, h/10-i), rgb(0x2a, 0x2a, 0x2e))
	}
}
