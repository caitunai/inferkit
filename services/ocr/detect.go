package ocr

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
)

func detectionBoxes(ctx context.Context, values []float32, shape ort.Shape, w, h int, c Config) ([]Box, error) {
	if !validDetectionOutput(values, shape, c.DetectionMaxSide) {
		return nil, ErrInference
	}
	rows, cols := int(shape[2]), int(shape[3])
	mask := make([]bool, len(values))
	for i, v := range values {
		mask[i] = float64(v) > c.DetectionThreshold
	}
	d := boxDetector{values: values, mask: mask, config: c, cols: cols, rows: rows}
	for i, on := range mask {
		if !on {
			continue
		}
		if err := d.add(ctx, i); err != nil {
			return nil, err
		}
	}
	boxes := mappedBoxes(d.boxes, cols, rows, w, h)
	sortBoxes(boxes)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.OCRBoxes(d.candidates, len(boxes))
	}
	return boxes, nil
}

type boxDetector struct {
	values                 []float32
	mask                   []bool
	boxes                  []Box
	config                 Config
	cols, rows, candidates int
}

func (d *boxDetector) add(ctx context.Context, index int) error {
	d.candidates++
	if d.candidates > d.config.MaxCandidates {
		return imageinput.ErrTooLarge
	}
	points, err := component(ctx, d.mask, d.values, d.cols, d.rows, index, d.config.DetectionThreshold)
	if err != nil {
		return err
	}
	box, ok := componentBox(points, d.values, d.cols, d.rows, d.config)
	if !ok {
		return nil
	}
	if len(d.boxes) >= d.config.MaxLines {
		return imageinput.ErrTooLarge
	}
	d.boxes = append(d.boxes, box)
	return nil
}

// component uses 8-connectivity and collects boundary pixels for the convex hull.
// The original probability map is immutable; only the visited mask is cleared.
func component(ctx context.Context, mask []bool, values []float32, w, h, start int, threshold float64) ([]Point, error) {
	queue := []int{start}
	mask[start] = false
	boundary := make([]Point, 0)
	for head := 0; head < len(queue); head++ {
		if head%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(ErrInference, err)
			}
		}
		index := queue[head]
		var edge bool
		queue, edge = visitNeighbors(queue, mask, values, w, h, index, threshold)
		if edge {
			boundary = append(boundary, Point{X: float64(index % w), Y: float64(index / w)})
		}
	}
	return boundary, nil
}

func visitNeighbors(queue []int, mask []bool, values []float32, w, h, index int, threshold float64) ([]int, bool) {
	x, y := index%w, index/w
	edge := false
	for _, offset := range [8][2]int{{-1, -1}, {0, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}} {
		nx, ny := x+offset[0], y+offset[1]
		if nx < 0 || ny < 0 || nx >= w || ny >= h {
			edge = true
			continue
		}
		next := ny*w + nx
		if float64(values[next]) <= threshold {
			edge = true
		}
		if mask[next] {
			mask[next] = false
			queue = append(queue, next)
		}
	}
	return queue, edge
}

func componentBox(points []Point, values []float32, w, h int, c Config) (Box, bool) {
	r, ok := minimumRectangle(points)
	if !ok {
		return Box{}, false
	}
	width, height := r.maxU-r.minU, r.maxV-r.minV
	if min(width, height) < 3 || rectangleScore(r, values, w, h) < c.BoxThreshold {
		return Box{}, false
	}
	expand := width * height * c.UnclipRatio / (2 * (width + height))
	if min(width, height)+2*expand < 5 {
		return Box{}, false
	}
	return r.box(expand), true
}

func rectangleScore(r rectangle, values []float32, w, h int) float64 {
	box := r.box(0)
	minX, maxX, minY, maxY := float64(w-1), float64(0), float64(h-1), float64(0)
	for _, p := range box {
		minX = min(minX, p.X)
		maxX = max(maxX, p.X)
		minY = min(minY, p.Y)
		maxY = max(maxY, p.Y)
	}
	total, count := float64(0), 0
	for y := max(0, int(math.Floor(minY))); y <= min(h-1, int(math.Ceil(maxY))); y++ {
		for x := max(0, int(math.Floor(minX))); x <= min(w-1, int(math.Ceil(maxX))); x++ {
			u, v := float64(x)*r.ux+float64(y)*r.uy, -float64(x)*r.uy+float64(y)*r.ux
			if u >= r.minU-0.5 && u <= r.maxU+0.5 && v >= r.minV-0.5 && v <= r.maxV+0.5 {
				total += float64(values[y*w+x])
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func mapBox(box Box, cols, rows, w, h int) Box {
	for i, p := range box {
		box[i] = Point{X: max(0, min(float64(w-1), math.Round(p.X/float64(cols)*float64(w)))), Y: max(0, min(float64(h-1), math.Round(p.Y/float64(rows)*float64(h))))}
	}
	return box
}

func sortBoxes(boxes []Box) {
	slices.SortStableFunc(boxes, func(a, b Box) int { return cmp.Or(cmp.Compare(a[0].Y, b[0].Y), cmp.Compare(a[0].X, b[0].X)) })
	// Match Paddle's row-local correction without a non-transitive comparator.
	for i := 1; i < len(boxes); i++ {
		for j := i; j > 0; j-- {
			a, b := boxes[j-1], boxes[j]
			if math.Abs(a[0].Y-b[0].Y) >= 10 || a[0].X <= b[0].X {
				break
			}
			boxes[j-1], boxes[j] = b, a
		}
	}
}

func validProbabilities(values []float32) bool {
	return probabilitiesValid(values)
}

func scalarProbabilitiesValid(values []float32) bool {
	for _, v := range values {
		if math.IsNaN(float64(v)) || v < 0 || v > 1 {
			return false
		}
	}
	return true
}

func validDetectionOutput(values []float32, shape ort.Shape, limit int) bool {
	if len(shape) != 4 || shape[0] != 1 || shape[1] != 1 {
		return false
	}
	if shape[2] < 1 || shape[3] < 1 || shape[2] > int64(limit) || shape[3] > int64(limit) {
		return false
	}
	return int64(len(values)) == shape[2]*shape[3] && validProbabilities(values)
}

func mappedBoxes(boxes []Box, cols, rows, w, h int) []Box {
	result := make([]Box, 0, len(boxes))
	for _, box := range boxes {
		mapped := mapBox(box, cols, rows, w, h)
		width, height := boxSize(mapped)
		if validBox(mapped) && min(width, height) > 3 {
			result = append(result, mapped)
		}
	}
	return result
}
