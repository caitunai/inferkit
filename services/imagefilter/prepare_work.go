package imagefilter

import (
	"context"
	"errors"
	"image"

	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/xutil"
)

const minimumParallelPixels = imageSize * imageSize

// A preparation lease borrows only idle caller slots, never allocates tensors,
// and returns every slot after all row workers have stopped.
type preparationLease struct {
	pool chan *tensorSlot
	held []*tensorSlot
}

func (e *engine) reservePreparation() preparationLease {
	lease := preparationLease{pool: e.slots}
	for range cap(e.slots) {
		select {
		case slot := <-e.slots:
			lease.held = append(lease.held, slot)
		default:
			return lease
		}
	}
	return lease
}

func (l preparationLease) release() {
	for _, slot := range l.held {
		l.pool <- slot
	}
}

func resizeWithBudget(ctx context.Context, src *image.NRGBA, w, h int, reserve func() preparationLease) (*image.NRGBA, error) {
	lease := preparationLease{}
	if reserve != nil && src.Bounds().Dx()*src.Bounds().Dy() >= minimumParallelPixels && (src.Bounds().Dx() != w || src.Bounds().Dy() != h) {
		lease = reserve()
	}
	defer lease.release()
	workers := len(lease.held) + 1
	if trace := imagetiming.From(ctx); trace != nil {
		trace.ResizeWorkers(workers)
	}
	return resizeRGBA(ctx, src, w, h, workers)
}

func timedImageRows(ctx context.Context, src *image.NRGBA, workers int, transform func([]byte), stage imagetiming.Stage) (returnErr error) {
	done := imagetiming.Measure(ctx, stage)
	defer func() { done(returnErr) }()
	return forImageRows(ctx, src, workers, transform)
}

func forImageRows(ctx context.Context, src *image.NRGBA, workers int, transform func([]byte)) error {
	width := src.Bounds().Dx() * 4
	return forRows(ctx, src.Bounds().Dy(), workers, func(row int) {
		transform(src.Pix[row*src.Stride:][:width])
	})
}

func forRows(ctx context.Context, rows, workers int, render func(int)) error {
	if rows == 0 {
		return nil
	}
	workers = max(1, min(rows, workers))
	if workers == 1 {
		return renderRows(ctx, 0, rows, render)
	}
	group := xutil.ErrGroup(ctx, "image_filter_prepare_rows").Limit(workers)
	for worker := range workers {
		group.Go(func(ctx context.Context) error {
			return renderRows(ctx, worker*rows/workers, (worker+1)*rows/workers, render)
		})
	}
	if err := group.Wait(); err != nil {
		return errors.Join(ErrPreprocess, err)
	}
	return nil
}

func renderRows(ctx context.Context, first, last int, render func(int)) error {
	for row := first; row < last; row++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrPreprocess, err)
		}
		render(row)
	}
	return nil
}
