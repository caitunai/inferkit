package imagefilter

import (
	"encoding/json/v2"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/caitun/inferkit/services/imagepipeline"
	"github.com/caitun/inferkit/services/resource"
)

type nativeTrialCase struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

type nativeTrialPixels struct {
	Case          string `json:"case"`
	Name          string `json:"name"`
	Variant       string `json:"variant"`
	RGB           string `json:"rgb"`
	MaxPixelError int    `json:"max_pixel_error"`
}

type nativeTrialReport struct {
	Cases  []nativeTrialCase   `json:"cases"`
	Pixels []nativeTrialPixels `json:"pixels"`
}

type nativeTrialQuality struct {
	Case                string    `json:"case"`
	Name                string    `json:"name"`
	Variant             string    `json:"variant"`
	Probabilities       []float32 `json:"probabilities"`
	MaxProbabilityError float64   `json:"max_probability_error"`
	MaxPixelError       int       `json:"max_pixel_error"`
	ClassID             int       `json:"class_id"`
	SameClass           bool      `json:"same_class"`
	WithinPixelContract bool      `json:"within_pixel_contract"`
}

// This opt-in experiment reports quality losses; it does not authorize them as
// a production profile. Candidate RGB canvases are made by the isolated C tool.
func TestNativeExperimentPixels(t *testing.T) {
	directory := os.Getenv("IMAGE_FILTER_EXPERIMENT_DIR")
	if directory == "" {
		t.Skip("set IMAGE_FILTER_EXPERIMENT_DIR to an isolated native benchmark report")
	}
	root := trialRoot(t, directory)
	var report nativeTrialReport
	if err := json.Unmarshal(trialFile(t, root, "report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Cases) == 0 || len(report.Pixels) == 0 {
		t.Fatal("empty experimental report")
	}
	c := nativeConfig(t)
	c.Provider = os.Getenv("IMAGE_FILTER_EXPERIMENT_PROVIDER")
	plan := automaticPlan(c, resource.Detect())
	plan.concurrency = 1
	s := nativeServiceWithPlan(t, c, plan)
	baselines := trialBaselines(t, s, root, report.Cases)
	results := trialResults(t, s, root, report.Pixels, baselines)
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile("model-quality-"+s.engine.provider+".json", encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d candidate quality checks with %s", len(results), s.engine.provider)
}

func trialResults(t *testing.T, s *Service, root *os.Root, candidates []nativeTrialPixels, baselines map[string]Result) []nativeTrialQuality {
	t.Helper()
	results := make([]nativeTrialQuality, 0, len(candidates))
	for _, candidate := range candidates {
		baseline, exists := baselines[candidate.Case]
		if !exists {
			t.Fatalf("missing baseline for %s", candidate.Case)
		}
		actual := trialInference(t, s, trialFile(t, root, candidate.RGB))
		results = append(results, trialQuality(candidate, actual, baseline))
	}
	return results
}

func trialRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return root
}

func trialFile(t *testing.T, root *os.Root, name string) []byte {
	t.Helper()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func trialBaselines(t *testing.T, s *Service, root *os.Root, cases []nativeTrialCase) map[string]Result {
	t.Helper()
	results := make(map[string]Result, len(cases))
	for _, sample := range cases {
		data := trialFile(t, trialRoot(t, filepath.Dir(sample.Source)), filepath.Base(sample.Source))
		pixels, normalizeErr := imagepipeline.Normalize(t.Context(), data, s.config.MaxPixels, imageSize)
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		reference := trialFile(t, root, "pixels/"+sample.ID+"_A.rgb")
		exact := slices.Equal(reference, pixels.Data)
		pixels.Close()
		if !exact {
			t.Fatalf("experimental baseline differs from production for %s", sample.Name)
		}
		results[sample.ID] = trialInference(t, s, reference)
	}
	return results
}

func trialInference(t *testing.T, s *Service, rgb []byte) Result {
	t.Helper()
	if len(rgb) != imageSize*imageSize*channels {
		t.Fatal("invalid experimental RGB canvas")
	}
	slot := <-s.engine.slots
	defer func() { s.engine.slots <- slot }()
	writeRGBTensor(rgb, slot.input.GetData())
	probabilities, err := s.engine.run(t.Context(), slot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.result(probabilities)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func trialQuality(candidate nativeTrialPixels, actual, baseline Result) nativeTrialQuality {
	var largest float64
	for i, value := range actual.Probabilities {
		largest = max(largest, math.Abs(float64(value)-float64(baseline.Probabilities[i])))
	}
	return nativeTrialQuality{
		Case: candidate.Case, Name: candidate.Name, Variant: candidate.Variant,
		Probabilities: actual.Probabilities, ClassID: actual.ClassID,
		MaxProbabilityError: largest, MaxPixelError: candidate.MaxPixelError,
		SameClass:           actual.ClassID == baseline.ClassID,
		WithinPixelContract: candidate.MaxPixelError <= imagepipeline.MaxPixelError,
	}
}
