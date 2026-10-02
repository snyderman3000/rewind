// Package qr encodes QR codes. Adapted from rsc.io/qr (BSD-3-Clause, see
// licenses/BSD-rsc-qr.txt); it only keeps what Rewind needs to draw codes.
package qr

import (
	"errors"

	"rewind/internal/qr/coding"
)

type Level int

const (
	L Level = iota // 20% redundant
	M              // 38% redundant
	Q              // 55% redundant
	H              // 65% redundant
)

type Code struct {
	Bitmap []byte // 1 is black, 0 is white
	Size   int    // modules on a side
	Stride int    // bytes per row
}

func (c *Code) Black(x, y int) bool {
	return 0 <= x && x < c.Size && 0 <= y && y < c.Size &&
		c.Bitmap[y*c.Stride+x/8]&(1<<uint(7-x&7)) != 0
}

// Encode picks the smallest version that fits text at the given level.
func Encode(text string, level Level) (*Code, error) {
	var enc coding.Encoding
	switch {
	case coding.Num(text).Check() == nil:
		enc = coding.Num(text)
	case coding.Alpha(text).Check() == nil:
		enc = coding.Alpha(text)
	default:
		enc = coding.String(text)
	}
	l := coding.Level(level)
	var v coding.Version
	for v = coding.MinVersion; ; v++ {
		if v > coding.MaxVersion {
			return nil, errors.New("text too long to encode as QR")
		}
		if enc.Bits(v) <= v.DataBytes(l)*8 {
			break
		}
	}
	p, err := coding.NewPlan(v, l, 0)
	if err != nil {
		return nil, err
	}
	cc, err := p.Encode(enc)
	if err != nil {
		return nil, err
	}
	return &Code{cc.Bitmap, cc.Size, cc.Stride}, nil
}
