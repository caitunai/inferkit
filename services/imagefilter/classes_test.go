package imagefilter

import (
	"errors"
	"slices"
	"testing"
)

func tenClassManifest() *manifest {
	labels := []string{"00_invalid", "01_valid", "03_childish", "04_paper", "05_other_app", "06_professional", "07_stolen", "08_screenshot", "09_sensitive", "10_photo"}
	names := []string{"无效", "有效", "幼稚", "纸绘", "其它绘画APP", "专业", "盗图", "截图", "敏感图", "普通照片"}
	m := &manifest{Labels: labels, LabelProfile: "artwork_10class_v1"}
	for i, label := range labels {
		id := i
		if i >= 2 {
			id++
		}
		m.ClassMapping = append(m.ClassMapping, Class{Label: label, Name: names[i], Index: i, LabelID: id})
	}
	return m
}

func TestTenClassArgmaxPreservesBusinessIDs(t *testing.T) {
	t.Parallel()
	m := tenClassManifest()
	s := &Service{labels: m.Labels, classes: m.classes(), labelProfile: m.LabelProfile}
	for i, class := range m.ClassMapping {
		probabilities := make([]float32, len(m.Labels))
		probabilities[i] = 1
		result, err := s.result(probabilities)
		if err != nil {
			t.Fatal(err)
		}
		if result.ClassID != i || result.LabelID != class.LabelID || result.Label != class.Label || result.ClassName != class.Name {
			t.Fatalf("class index/business ID confused: %+v", result)
		}
		if len(result.Probabilities) != 10 || result.LabelProfile != m.LabelProfile {
			t.Fatal("incomplete multiclass output")
		}
	}
}

func TestClassMappingRejectsAmbiguousMetadata(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*manifest){
		func(m *manifest) { m.ClassMapping[2].Index = 3 },
		func(m *manifest) { m.ClassMapping[2].LabelID = 1 },
		func(m *manifest) { m.ClassMapping[2].Label = "02_missing" },
		func(m *manifest) { m.ClassMapping[2].Name = "" },
		func(m *manifest) { m.ClassMapping = m.ClassMapping[:2] },
		func(m *manifest) { m.ClassMapping = nil },
	} {
		m := tenClassManifest()
		mutate(m)
		if !errors.Is(m.validateClasses(), ErrModel) {
			t.Fatal("ambiguous class mapping accepted")
		}
	}
}

func TestOutputWidthMustMatchLabelCount(t *testing.T) {
	t.Parallel()
	m := tenClassManifest()
	n := node{Name: "output0", DType: "float32", Kind: "probabilities", Softmax: true, Shape: []int64{1, 10}}
	if err := errors.Join(validateLabels(m.Labels, m.Labels), m.validateClasses(), n.validateOutput(10)); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(n.validateOutput(2), ErrModel) {
		t.Fatal("output width detached from label count")
	}
	bad := slices.Clone(m.Labels)
	bad[2] = bad[1]
	if !errors.Is(validateLabels(bad, bad), ErrModel) {
		t.Fatal("duplicate output label accepted")
	}
}

func TestLegacyBinaryMappingAndValidationIndex(t *testing.T) {
	t.Parallel()
	m := &manifest{Labels: []string{"0_invalid", "1_valid"}}
	if len(m.classes()) != 2 || m.classes()[1].LabelID != 1 {
		t.Fatal("legacy mapping lost")
	}
	file := validationFile{Samples: []validationSample{{ClassIndex: new(2), Probabilities: make([]float32, 10)}}}
	if err := file.validate(10); err != nil {
		t.Fatal(err)
	}
	file.Samples[0].ClassID = new(0)
	if !errors.Is(file.validate(10), ErrValidation) {
		t.Fatal("conflicting export indices accepted")
	}
	file.Samples[0] = validationSample{ClassID: new(1), Probabilities: make([]float32, 2)}
	if err := file.validate(2); err != nil {
		t.Fatal("legacy class_id fixture rejected", err)
	}
}
