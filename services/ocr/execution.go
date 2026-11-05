package ocr

import (
	"context"
	"errors"

	"golang.org/x/sync/semaphore"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/resource"
)

const (
	modelMemoryReserve int64 = 512 << 20
	// Account for both native output and its Go copy, plus per-run activations.
	recognitionWorkingBytes int64 = 2*maxOutputElements*4 + 64<<20
)

type executionPlan struct {
	memory             int64
	cpus               int
	detectionThreads   int
	recognitionThreads int
	lineWorkers        int
	images             int
	requests           int
}

// automaticPlan partitions one machine budget across detection and line work.
// It keeps finite admission even when callers remove all legacy tuning keys.
func automaticPlan(c Config, b resource.Budget) executionPlan {
	cpus := max(1, b.CPUs)
	available := max(1, b.Memory/2-modelMemoryReserve)
	imageBytes := c.MaxPixels*(8+imageinput.ExtraDecodeBytesPerPixel) + c.MaxBytes*2 + int64(c.DetectionMaxSide)*int64(c.DetectionMaxSide)*16
	lineBytes := recognitionWorkingBytes + int64(c.RecognitionHeight)*int64(c.RecognitionMaxWidth)*3*4
	images := int(max(1, min(int64(cpus), available/2/max(1, imageBytes))))
	workers := int(max(1, min(int64(cpus), available/2/max(1, lineBytes))))
	remaining := max(0, available-int64(images)*imageBytes-int64(workers)*lineBytes)
	waiting := int(max(1, min(int64(cpus), remaining/max(1, c.MaxBytes))))
	return executionPlan{memory: b.Memory, cpus: cpus, detectionThreads: cpus, recognitionThreads: 1, lineWorkers: workers, images: images, requests: images + waiting}
}

type executor struct {
	cpu   *semaphore.Weighted
	lines chan struct{}
	cpus  int64
}

func newExecutor(plan executionPlan) *executor {
	return &executor{cpu: semaphore.NewWeighted(int64(plan.cpus)), lines: make(chan struct{}, plan.lineWorkers), cpus: int64(plan.cpus)}
}

func (e *executor) acquire(ctx context.Context, weight int64) (func(), error) {
	if err := e.cpu.Acquire(ctx, weight); err != nil {
		return nil, errors.Join(ErrInference, err)
	}
	return func() { e.cpu.Release(weight) }, nil
}

func (e *executor) acquireLine(ctx context.Context) (func(), error) {
	select {
	case e.lines <- struct{}{}:
	case <-ctx.Done():
		return nil, errors.Join(ErrInference, ctx.Err())
	}
	release, err := e.acquire(ctx, 1)
	if err != nil {
		<-e.lines
		return nil, err
	}
	return func() { release(); <-e.lines }, nil
}
