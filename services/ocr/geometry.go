package ocr

import (
	"cmp"
	"math"
	"slices"
)

// Point is an original, orientation-corrected image coordinate.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Box lists corners clockwise: top left, top right, bottom right, bottom left.
type Box [4]Point

func cross(o, a, b Point) float64 { return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X) }
func distance(a, b Point) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func convexHull(points []Point) []Point {
	slices.SortFunc(points, func(a, b Point) int { return cmp.Or(cmp.Compare(a.X, b.X), cmp.Compare(a.Y, b.Y)) })
	points = slices.Compact(points)
	if len(points) < 3 {
		return points
	}
	hull := make([]Point, 0, 2*len(points))
	for _, p := range points {
		hull = appendHull(hull, p, 0)
	}
	lower := len(hull)
	for i := len(points) - 2; i >= 0; i-- {
		hull = appendHull(hull, points[i], lower-1)
	}
	return hull[:len(hull)-1]
}

func appendHull(hull []Point, p Point, floor int) []Point {
	for len(hull) > floor+1 && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
		hull = hull[:len(hull)-1]
	}
	return append(hull, p)
}

type rectangle struct{ ux, uy, minU, maxU, minV, maxV float64 }

func minimumRectangle(points []Point) (rectangle, bool) {
	hull := convexHull(points)
	if len(hull) < 3 {
		return rectangle{}, false
	}
	best := rectangle{}
	area := math.Inf(1)
	for i, p := range hull {
		q := hull[(i+1)%len(hull)]
		length := distance(p, q)
		candidate := projectRectangle(hull, (q.X-p.X)/length, (q.Y-p.Y)/length)
		if value := (candidate.maxU - candidate.minU) * (candidate.maxV - candidate.minV); value < area {
			best = candidate
			area = value
		}
	}
	return best, true
}

func projectRectangle(hull []Point, ux, uy float64) rectangle {
	r := rectangle{ux: ux, uy: uy, minU: math.Inf(1), maxU: math.Inf(-1), minV: math.Inf(1), maxV: math.Inf(-1)}
	for _, p := range hull {
		u, v := p.X*ux+p.Y*uy, -p.X*uy+p.Y*ux
		r.minU = min(r.minU, u)
		r.maxU = max(r.maxU, u)
		r.minV = min(r.minV, v)
		r.maxV = max(r.maxV, v)
	}
	return r
}

func (r rectangle) box(expand float64) Box {
	uv := [4]Point{{X: r.minU - expand, Y: r.minV - expand}, {X: r.maxU + expand, Y: r.minV - expand}, {X: r.maxU + expand, Y: r.maxV + expand}, {X: r.minU - expand, Y: r.maxV + expand}}
	var result Box
	for i, p := range uv {
		result[i] = Point{X: p.X*r.ux - p.Y*r.uy, Y: p.X*r.uy + p.Y*r.ux}
	}
	return orderBox(result)
}

func orderBox(box Box) Box {
	slices.SortFunc(box[:], func(a, b Point) int { return cmp.Or(cmp.Compare(a.X, b.X), cmp.Compare(a.Y, b.Y)) })
	left, right := box[:2], box[2:]
	if left[0].Y > left[1].Y {
		left[0], left[1] = left[1], left[0]
	}
	if right[0].Y > right[1].Y {
		right[0], right[1] = right[1], right[0]
	}
	return Box{left[0], right[0], right[1], left[1]}
}

func boxSize(box Box) (float64, float64) {
	return max(distance(box[0], box[1]), distance(box[3], box[2])), max(distance(box[0], box[3]), distance(box[1], box[2]))
}

// homography maps the unit square onto a quadrilateral for perspective cropping.
type homography struct{ a, b, c, d, e, f, g, h float64 }

func projective(box Box) homography {
	p, q, r, s := box[0], box[1], box[2], box[3]
	dx1, dx2, dx3 := q.X-r.X, s.X-r.X, p.X-q.X+r.X-s.X
	dy1, dy2, dy3 := q.Y-r.Y, s.Y-r.Y, p.Y-q.Y+r.Y-s.Y
	g, h := float64(0), float64(0)
	if denominator := dx1*dy2 - dx2*dy1; math.Abs(denominator) > 1e-9 {
		g = (dx3*dy2 - dx2*dy3) / denominator
		h = (dx1*dy3 - dx3*dy1) / denominator
	}
	return homography{a: q.X - p.X + g*q.X, b: s.X - p.X + h*s.X, c: p.X, d: q.Y - p.Y + g*q.Y, e: s.Y - p.Y + h*s.Y, f: p.Y, g: g, h: h}
}

func (h homography) at(u, v float64) Point {
	denominator := h.g*u + h.h*v + 1
	return Point{X: (h.a*u + h.b*v + h.c) / denominator, Y: (h.d*u + h.e*v + h.f) / denominator}
}

// validBox rejects collapsed corners after clipping to image bounds, before a
// projective denominator or pixel index can become invalid.
func validBox(box Box) bool {
	for i, p := range box {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			return false
		}
		if cross(p, box[(i+1)%4], box[(i+2)%4]) <= 1e-9 {
			return false
		}
	}
	return true
}
