package ocr

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"math"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
)

func opaqueImage(img image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	return dst
}

func detectionShape(img *image.RGBA, limit int) ort.Shape {
	b := img.Bounds()
	ratio := min(1, float64(limit)/float64(max(b.Dx(), b.Dy())))
	w := max(32, int(math.RoundToEven(float64(b.Dx())*ratio/32))*32)
	h := max(32, int(math.RoundToEven(float64(b.Dy())*ratio/32))*32)
	return ort.Shape{1, 3, int64(h), int64(w)}
}

func detectionTensor(ctx context.Context, img *image.RGBA, limit int) ([]float32, ort.Shape, error) {
	shape := detectionShape(img, limit)
	data := make([]float32, int(shape.FlattenedSize()))
	return data, shape, fillDetection(ctx, img, shape, data)
}

func fillDetection(ctx context.Context, img *image.RGBA, shape ort.Shape, data []float32) error {
	w, h := int(shape[3]), int(shape[2])
	b := img.Bounds()
	mean := [3]float64{0.485, 0.456, 0.406}
	std := [3]float64{0.229, 0.224, 0.225}
	for y := range h {
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrInference, err)
		}
		for x := range w {
			pixel := sample(img, (float64(x)+0.5)*float64(b.Dx())/float64(w)-0.5, (float64(y)+0.5)*float64(b.Dy())/float64(h)-0.5)
			for c := range 3 {
				data[c*w*h+y*w+x] = float32((pixel[2-c]/255 - mean[c]) / std[c])
			}
		}
	}
	return nil
}

type lineGeometry struct {
	box                        Box
	transform                  homography
	width, height, scaledWidth int
	vertical                   bool
}

func recognitionGeometry(box Box, height, maxWidth int) (lineGeometry, error) {
	if !validBox(box) {
		return lineGeometry{}, ErrInference
	}
	w, h := boxSize(box)
	vertical := h >= w*1.5
	if vertical {
		w, h = h, w
	}
	if min(w, h) < 1 {
		return lineGeometry{}, ErrInference
	}
	resizedWidth := max(1, int(math.Ceil(float64(height)*w/h)))
	if resizedWidth > maxWidth {
		return lineGeometry{}, imageinput.ErrTooLarge
	}
	return lineGeometry{box: box, transform: projective(box), width: max(320, resizedWidth), height: height, scaledWidth: resizedWidth, vertical: vertical}, nil
}

func (g lineGeometry) shape(batch int) ort.Shape {
	return ort.Shape{int64(batch), 3, int64(g.height), int64(g.width)}
}

func recognitionTensor(ctx context.Context, img *image.RGBA, box Box, height, maxWidth int) ([]float32, ort.Shape, error) {
	g, err := recognitionGeometry(box, height, maxWidth)
	if err != nil {
		return nil, nil, err
	}
	shape := g.shape(1)
	data := make([]float32, int(shape.FlattenedSize()))
	return data, shape, fillRecognition(ctx, img, g, data)
}

func fillRecognition(ctx context.Context, img *image.RGBA, g lineGeometry, data []float32) error {
	clear(data) // Reused rows must retain normalized-zero padding.
	for y := range g.height {
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrInference, err)
		}
		writeRecognitionRow(img, data, g.transform, y, g.height, g.width, g.scaledWidth, g.vertical)
	}
	return nil
}

func sample(img *image.RGBA, x, y float64) [3]float64 {
	x = max(0, min(float64(img.Rect.Dx()-1), x))
	y = max(0, min(float64(img.Rect.Dy()-1), y))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, img.Rect.Dx()-1), min(y0+1, img.Rect.Dy()-1)
	fx, fy := x-float64(x0), y-float64(y0)
	offsets := [4]int{img.PixOffset(x0, y0), img.PixOffset(x1, y0), img.PixOffset(x0, y1), img.PixOffset(x1, y1)}
	var pixel [3]float64
	for c := range 3 {
		top := float64(img.Pix[offsets[0]+c])*(1-fx) + float64(img.Pix[offsets[1]+c])*fx
		bottom := float64(img.Pix[offsets[2]+c])*(1-fx) + float64(img.Pix[offsets[3]+c])*fx
		pixel[c] = top*(1-fy) + bottom*fy
	}
	return pixel
}

func writeRecognitionRow(img *image.RGBA, data []float32, transform homography, y, height, width, resizedWidth int, vertical bool) {
	for x := range resizedWidth {
		u, v := (float64(x)+0.5)/float64(resizedWidth), (float64(y)+0.5)/float64(height)
		if vertical {
			u, v = 1-v, u
		}
		p := transform.at(u, v)
		pixel := sample(img, p.X, p.Y)
		for c := range 3 {
			data[c*height*width+y*width+x] = float32(pixel[2-c]/127.5 - 1)
		}
	}
}
