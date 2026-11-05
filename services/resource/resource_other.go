//go:build !linux && !darwin

package resource

func platformBudget() (Budget, error) { return Budget{Memory: fallbackMemory}, nil }
