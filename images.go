package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxImageBytes = 4 << 20

var pngFast = &png.Encoder{CompressionLevel: png.BestSpeed}

// Image downloads (or loads from the card cache) a picture, scaled to w×h.
// cover=false fits inside the box; cover=true fills it and crops the overflow.
func (env *Env) Image(url string, w, h int, cover bool) (*image.RGBA, error) {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%v", url, w, h, cover)))
	cache := filepath.Join(env.DataDir, "cache", "img-"+hex.EncodeToString(sum[:8])+".png")
	if f, err := os.Open(cache); err == nil {
		img, err := png.Decode(f)
		f.Close()
		if err == nil {
			now := time.Now()
			os.Chtimes(cache, now, now)
			return toRGBA(img), nil
		}
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	cl := &http.Client{Timeout: 30 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, netHint(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("image: %s", resp.Status)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, err
	}
	var out *image.RGBA
	if cover {
		out = fill(img, w, h)
	} else {
		out = fit(img, w, h)
	}
	os.MkdirAll(filepath.Dir(cache), 0o755)
	if f, err := os.Create(cache + ".tmp"); err == nil {
		pngFast.Encode(f, out)
		f.Close()
		os.Rename(cache+".tmp", cache)
	}
	return out, nil
}

// PruneImageCache keeps the picture cache under limit bytes, dropping the least recently used.
func (env *Env) PruneImageCache(limit int64) {
	dir := filepath.Join(env.DataDir, "cache")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		path string
		size int64
		t    time.Time
	}
	var fs []f
	var total int64
	for _, e := range ents {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			fs = append(fs, f{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	if total <= limit {
		return
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t.Before(fs[j].t) })
	for _, x := range fs {
		if total <= limit*8/10 {
			break
		}
		os.Remove(x.path)
		total -= x.size
	}
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Bounds(), img, b.Min, draw.Src)
	return r
}

// fit box-filters the image down so it fits inside w×h, keeping aspect ratio.
func fit(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	scale := float64(w) / float64(sw)
	if s2 := float64(h) / float64(sh); s2 < scale {
		scale = s2
	}
	return resample(src, 0, 0, sw, sh, max(1, int(float64(sw)*scale)), max(1, int(float64(sh)*scale)))
}

// fill scales the image to cover w×h and crops the centre.
func fill(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	// source rect with the target aspect
	cw, ch := sw, sw*h/w
	if ch > sh {
		ch, cw = sh, sh*w/h
	}
	return resample(src, (sw-cw)/2, (sh-ch)/2, cw, ch, w, h)
}

// resample box-filters (downscale) or nearest-neighbours (upscale) a source rect into dw×dh.
func resample(src *image.RGBA, sx, sy, sw, sh, dw, dh int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := sy+y*sh/dh, sy+(y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := sx+x*sw/dw, sx+(x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n int
			for yy := y0; yy < y1; yy++ {
				o := yy*src.Stride + x0*4
				for xx := x0; xx < x1; xx++ {
					r += int(src.Pix[o])
					g += int(src.Pix[o+1])
					b += int(src.Pix[o+2])
					n++
					o += 4
				}
			}
			d := y*dst.Stride + x*4
			dst.Pix[d], dst.Pix[d+1], dst.Pix[d+2], dst.Pix[d+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}
