package imagefilter

import (
	"context"
	"errors"

	"github.com/caitun/inferkit/services/imagepipeline"
	"github.com/caitun/inferkit/services/imagetiming"
)

func preprocessNative(ctx context.Context, data []byte, maxPixels int64, dst []float32) (returnErr error) {
	if len(dst) != inputLength {
		return ErrPreprocess
	}
	done := imagetiming.Measure(ctx, imagetiming.Preprocess)
	defer func() { done(returnErr) }()
	pixels, err := normalizedPixels(ctx, data, maxPixels)
	if err != nil {
		return err
	}
	defer pixels.Close()
	if trace := imagetiming.From(ctx); trace != nil {
		trace.Format(pixels.Header.Format)
		trace.Decoder("libvips")
		trace.ResizeBackend("libvips")
		trace.ResizeWorkers(1)
		trace.Image(pixels.Header.Width, pixels.Header.Height, "rgb")
		recordNativeDiagnostics(trace, pixels.Diagnostics)
	}
	doneTensor := imagetiming.Measure(ctx, imagetiming.Tensor)
	writeRGBTensor(pixels.Data, dst)
	doneTensor(nil)
	return nil
}

func recordNativeDiagnostics(trace *imagetiming.Trace, stats imagepipeline.Diagnostics) {
	trace.Native(stats.Header, stats.Prepare, stats.Eval, stats.Cleanup)
	if stats.JPEGLoader != "" {
		trace.JPEG(stats.JPEGLoader, stats.JPEGShrink, stats.JPEGWidth, stats.JPEGHeight, stats.JPEGLoadBuilds, stats.JPEGMultiscan)
	}
}

func normalizedPixels(ctx context.Context, data []byte, maxPixels int64) (*imagepipeline.Pixels, error) {
	done := imagetiming.Measure(ctx, imagetiming.NativePipeline)
	pixels, err := imagepipeline.Normalize(ctx, data, maxPixels, imageSize)
	done(err)
	if err == nil {
		return pixels, nil
	}
	if errors.Is(err, imagepipeline.ErrTooLarge) {
		return nil, errors.Join(ErrPreprocess, ErrTooLarge, err)
	}
	if errors.Is(err, imagepipeline.ErrUnavailable) {
		return nil, errors.Join(ErrConfig, err)
	}
	return nil, errors.Join(ErrPreprocess, ErrInput, err)
}

// All image operations have finished. Pack RGB bytes into NCHW floats.
func writeRGBTensorScalar(src []byte, dst []float32) {
	plane := len(src) / channels
	writeRGBChannels(src, dst[:plane], dst[plane:2*plane], dst[2*plane:])
}

func writeRGBChannels(src []byte, red, green, blue []float32) {
	for i := range red {
		red[i] = float32(src[i*channels]) / pixelMax
		green[i] = float32(src[i*channels+1]) / pixelMax
		blue[i] = float32(src[i*channels+2]) / pixelMax
	}
}
