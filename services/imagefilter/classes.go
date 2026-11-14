package imagefilter

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const maxClasses = 256

// Class maps a model output index to a stable business ID and display name.
// Index and LabelID deliberately differ for taxonomies with missing numeric IDs.
type Class struct {
	Label   string `json:"class"`
	Name    string `json:"name"`
	Index   int    `json:"index"`
	LabelID int    `json:"label_id"`
}

func (m *manifest) readClassMapping(dir string) error {
	var mapping []Class
	err := readJSON(filepath.Join(dir, "class_mapping.json"), &mapping)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Existing binary deployments have no mapping sidecar.
	}
	if err != nil {
		return err
	}
	if len(m.ClassMapping) > 0 && !slices.Equal(m.ClassMapping, mapping) {
		return ErrModel
	}
	m.ClassMapping = mapping
	return nil
}

func (m *manifest) validateClasses() error {
	if len(m.LabelProfile) > 128 {
		return ErrModel
	}
	if len(m.ClassMapping) == 0 && len(m.Labels) == 2 {
		return nil
	}
	if len(m.ClassMapping) != len(m.Labels) {
		return ErrModel
	}
	seen := make(map[int]bool, len(m.ClassMapping))
	for i, class := range m.ClassMapping {
		if err := class.validate(i, m.Labels[i]); err != nil {
			return err
		}
		if seen[class.LabelID] {
			return ErrModel
		}
		seen[class.LabelID] = true
	}
	return nil
}

func (c Class) validate(index int, label string) error {
	if c.Index != index || c.Label != label || c.LabelID < 0 || c.LabelID > math.MaxInt32 {
		return ErrModel
	}
	if !utf8.ValidString(c.Name) || strings.TrimSpace(c.Name) == "" || utf8.RuneCountInString(c.Name) > 128 {
		return ErrModel
	}
	return nil
}

func (m *manifest) classes() []Class {
	if len(m.ClassMapping) > 0 {
		return slices.Clone(m.ClassMapping)
	}
	classes := make([]Class, len(m.Labels))
	for i, label := range m.Labels {
		classes[i] = Class{Index: i, LabelID: i, Label: label, Name: label}
	}
	return classes
}
