package main

// Rewind's own widgets: VHS cassette, tomato badges, QR codes, VCR tracking noise.

import (
	"image"
	"image/color"
	"math"

	"rewind/internal/qr"
)

// Blit copies a w×h window of src starting at (sx,sy) to (x,y), clipped.
func (c *Canvas) Blit(src *image.RGBA, sx, sy, x, y, w, h int) {
	sb := src.Bounds()
	for dy := 0; dy < h; dy++ {
		py, qy := y+dy, sy+dy
		if py < 0 || py >= c.H || qy < 0 || qy >= sb.Dy() {
			continue
		}
		x0, w0, qx := x, w, sx
		if x0 < 0 {
			qx -= x0
			w0 += x0
			x0 = 0
		}
		if qx < 0 {
			x0 -= qx
			w0 += qx
			qx = 0
		}
		if x0+w0 > c.W {
			w0 = c.W - x0
		}
		if qx+w0 > sb.Dx() {
			w0 = sb.Dx() - qx
		}
		if w0 <= 0 {
			continue
		}
		so := qy*src.Stride + qx*4
		do := py*c.Stride + x0*4
		copy(c.Pix[do:do+w0*4], src.Pix[so:so+w0*4])
	}
}

// Poster draws an image centred in a box with a letterbox fill.
func (c *Canvas) Poster(img *image.RGBA, x, y, w, h int) {
	c.Fill(x, y, w, h, colBlack)
	if img == nil {
		return
	}
	b := img.Bounds()
	if b.Dx() == w && b.Dy() == h {
		c.Blit(img, 0, 0, x, y, w, h)
		return
	}
	c.DrawImage(img, x+(w-min(w, b.Dx()))/2, y+(h-min(h, b.Dy()))/2, min(w, b.Dx()), min(h, b.Dy()))
}

// rtColor: fresh (60+) is tomato red, rotten is splat green, unknown dim.
func rtColor(rt int) color.RGBA {
	switch {
	case rt < 0:
		return colPaperDim
	case rt >= 60:
		return colTomato
	}
	return colSplat
}

// Tomato draws a small fresh tomato (rt>=60), a rotten splat, or an empty ring when unknown.
func (c *Canvas) Tomato(x, y, s, rt int) {
	r := s / 2
	cx, cy := x+r, y+r+s/12
	switch {
	case rt < 0:
		c.Circle(cx, cy, r-1, colPaperDim, false, 1)
		return
	case rt >= 60:
		c.Circle(cx, cy, r-1, colTomato, true, 0)
		c.Circle(cx-r/3, cy-r/3, max(1, r/4), mix(colTomato, colPaper, 0.55), true, 0)
		// leafy crown
		lw := max(2, s/5)
		c.Fill(cx-lw, cy-r+1, lw*2+1, max(1, s/10), colGreen)
		c.Fill(cx-max(1, lw/3), cy-r-max(1, s/8), max(2, lw*2/3), max(2, s/6), colGreen)
	default:
		// splat: blob with drips
		c.Circle(cx, cy, r*3/4, colSplat, true, 0)
		for i := 0; i < 6; i++ {
			a := float64(i)*math.Pi/3 + 0.4
			dx := int(math.Cos(a) * float64(r) * 0.8)
			dy := int(math.Sin(a) * float64(r) * 0.8)
			c.Circle(cx+dx, cy+dy, max(1, r/4), colSplat, true, 0)
		}
	}
}

// VHS draws a VHS cassette seen from the front: label, window with two reels.
func (c *Canvas) VHS(x, y, w, h int, label, sub string, angle, progress float64, fLabel, fSub *Font) {
	shell := rgb(0x14, 0x15, 0x18)
	c.Fill(x, y, w, h, shell)
	c.Border(x, y, w, h, 2, colGrid)
	c.Fill(x+2, y+2, w-4, 2, rgb(0x26, 0x28, 0x2e)) // top bevel
	// ridged grips at the sides
	for i := 0; i < 5; i++ {
		gy := y + h*30/100 + i*h/14
		c.Fill(x+6, gy, w/24, 2, colGrid)
		c.Fill(x+w-6-w/24, gy, w/24, 2, colGrid)
	}
	// label
	lx, ly, lw, lh := x+w/9, y+h/12, w-2*w/9, h*36/100
	c.Fill(lx, ly, lw, lh, colPaper)
	sw := max(3, lh/9)
	c.Fill(lx, ly+lh-3*sw-3, lw, sw, colAmber)
	c.Fill(lx, ly+lh-2*sw-3, lw, sw, colMagenta)
	c.Fill(lx, ly+lh-sw-3, lw, sw, colCyan)
	c.Text(fLabel, lx+8, ly+4, fLabel.Ellipsize(label, lw-16), colBlack)
	if sub != "" && 4+fLabel.height+fSub.height < lh-3*sw-3 {
		c.Text(fSub, lx+8, ly+4+fLabel.height, fSub.Ellipsize(sub, lw-16), rgb(0x55, 0x50, 0x44))
	}
	// window
	wx, wy := x+w*22/100, ly+lh+h/14
	ww, wh := w*56/100, h-(wy-y)-h/7
	c.Fill(wx, wy, ww, wh, colBG)
	c.Border(wx, wy, ww, wh, 1, colGrid)
	r := wh * 30 / 100
	if r < 5 {
		r = 5
	}
	cy := wy + wh/2
	lcx, rcx := wx+ww*22/100, wx+ww*78/100
	maxT := min(wh/2-2, ww*22/100-2)
	lt := r + int(float64(maxT-r)*(1-progress))
	rt := r + int(float64(maxT-r)*progress)
	c.Reel(lcx, cy, r, angle, lt, colAmber, colAmberDim)
	c.Reel(rcx, cy, r, angle, rt, colAmber, colAmberDim)
	// tape across the bottom of the window
	c.Fill(lcx, wy+wh-3, rcx-lcx, 2, colAmberDk)
	// bottom flap
	c.Fill(x+w/12, y+h-h/10, w-w/6, h/14, rgb(0x0e, 0x0f, 0x11))
	c.Fill(x+w/12, y+h-h/10, w-w/6, 1, colGrid)
}

// QR draws text as a QR code with a white quiet zone, module px per cell. Returns the size drawn.
func (c *Canvas) QR(x, y, module int, text string) int {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return 0
	}
	n := code.Size + 8
	c.Fill(x, y, n*module, n*module, rgb(0xff, 0xff, 0xff))
	for yy := 0; yy < code.Size; yy++ {
		for xx := 0; xx < code.Size; xx++ {
			if code.Black(xx, yy) {
				c.Fill(x+(xx+4)*module, y+(yy+4)*module, module, module, colBlack)
			}
		}
	}
	return n * module
}

// QRSize is the drawn size of a QR code for text at a given module size.
func QRSize(text string, module int) int {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return 0
	}
	return (code.Size + 8) * module
}

// Tracking draws a VCR tracking-error band: shifted rows plus static, at row band.
func (c *Canvas) Tracking(x, y, w, h, band, bh int, seed uint32) {
	s := seed*2654435761 + 7
	for yy := band; yy < band+bh; yy++ {
		if yy < y || yy >= y+h {
			continue
		}
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		shift := int(s%9) - 4
		row := c.Pix[yy*c.Stride+x*4 : yy*c.Stride+(x+w)*4]
		tmp := make([]byte, len(row))
		copy(tmp, row)
		for xx := 0; xx < w; xx++ {
			sx := xx - shift
			if sx < 0 {
				sx = 0
			}
			if sx >= w {
				sx = w - 1
			}
			copy(row[xx*4:xx*4+3], tmp[sx*4:sx*4+3])
		}
		// sprinkle of white noise
		for k := 0; k < w/10; k++ {
			s ^= s << 13
			s ^= s >> 17
			s ^= s << 5
			px := int(s % uint32(w))
			v := uint8(150 + s>>25)
			o := px * 4
			row[o], row[o+1], row[o+2] = v, v, v
		}
	}
}

// OSD draws VCR on-screen-display text: bright with a hard drop shadow.
func (c *Canvas) OSD(f *Font, x, y int, s string, col color.RGBA) int {
	c.Text(f, x+2, y+2, s, colBlack)
	return c.Text(f, x, y, s, col)
}

// PlayIcon / PauseIcon for the OSD.
func (c *Canvas) PlayIcon(x, y, s int, col color.RGBA) {
	for i := 0; i < s; i++ {
		top := y + i/2
		bot := y + s - i/2
		c.Fill(x+i, top, 1, bot-top, col)
	}
}

func (c *Canvas) PauseIcon(x, y, s int, col color.RGBA) {
	bw := max(2, s/3)
	c.Fill(x, y, bw, s, col)
	c.Fill(x+s-bw, y, bw, s, col)
}
