package imagereview

import "slices"

// Labels returns the artwork review taxonomy shared by validation and the page.
func Labels() []string {
	return []string{"00_invalid", "01_valid", "03_childish", "04_paper", "05_other_app", "06_professional", "07_stolen", "08_screenshot", "09_sensitive", "10_photo"}
}

// FilterLabels includes immutable labels retained by historical binary models.
func FilterLabels() []string { return append(Labels(), "0_invalid", "1_valid") }

// ValidLabel preserves existing annotation clients while accepting the new taxonomy.
func ValidLabel(label string) bool { return slices.Contains(FilterLabels(), label) }
