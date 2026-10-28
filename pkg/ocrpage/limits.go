package ocrpage

import (
	"errors"
	"fmt"
	"math"
)

// ErrLimit identifies input resource-limit failures.
var ErrLimit = errors.New("ocrpage: input limit exceeded")

// Limits bounds untrusted document input. Zero fields use safe defaults.
type Limits struct {
	MaxBytes           int
	MaxElements        int
	MaxDepth           int
	MaxPixelCoordinate int
}

func (l Limits) defaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 64 << 20
	}
	if l.MaxElements <= 0 {
		l.MaxElements = 1_000_000
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = 256
	}
	if l.MaxPixelCoordinate <= 0 {
		l.MaxPixelCoordinate = 1_000_000
	}
	return l
}

// Validate rejects invalid geometry, confidences and excessive element counts.
func Validate(p Page, limits Limits) error {
	l := limits.defaults()
	if p.Width < 0 || p.Height < 0 || p.Width > l.MaxPixelCoordinate || p.Height > l.MaxPixelCoordinate {
		return fmt.Errorf("ocrpage: invalid page dimensions")
	}
	n := 0
	check := func(b Rect, pts []Point, c float64) error {
		n += 1 + len(pts)
		if n > l.MaxElements {
			return ErrLimit
		}
		if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
			return errors.New("ocrpage: invalid confidence")
		}
		if b.X1 < 0 || b.Y1 < 0 || b.X2 < b.X1 || b.Y2 < b.Y1 || b.X2 > l.MaxPixelCoordinate || b.Y2 > l.MaxPixelCoordinate || (p.Width > 0 && b.X2 > p.Width) || (p.Height > 0 && b.Y2 > p.Height) {
			return errors.New("ocrpage: geometry outside page")
		}
		for _, v := range pts {
			if v.X < 0 || v.Y < 0 || v.X > l.MaxPixelCoordinate || v.Y > l.MaxPixelCoordinate || (p.Width > 0 && v.X > p.Width) || (p.Height > 0 && v.Y > p.Height) {
				return errors.New("ocrpage: point outside page")
			}
		}
		return nil
	}
	for _, r := range p.Regions {
		if err := check(r.Box, r.Polygon, r.Confidence); err != nil {
			return err
		}
		if r.Table != nil {
			for _, c := range r.Table.Cells {
				if err := check(c.Box, nil, 0); err != nil {
					return err
				}
				if c.Row < 0 || c.Col < 0 || c.RowSpan < 1 || c.ColSpan < 1 || c.RowSpan > r.Table.Rows-c.Row || c.ColSpan > r.Table.Cols-c.Col {
					return errors.New("ocrpage: invalid table cell")
				}
			}
		}
		for _, v := range r.Lines {
			if err := check(v.Box, v.Polygon, v.Confidence); err != nil {
				return err
			}
			if err := check(v.Box, v.Baseline, 0); err != nil {
				return err
			}
			for _, w := range v.Words {
				if err := check(w.Box, w.Polygon, w.Confidence); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
