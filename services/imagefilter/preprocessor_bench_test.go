//go:build linux || darwin

package imagefilter

import (
	"os"
	"strconv"
	"testing"

	"github.com/caitun/inferkit/services/resource"
)

// Same model, provider, inputs and automatic resource budget; caches disabled.
func BenchmarkPreprocessorBackends(b *testing.B) {
	c := nativeConfig(b)
	c.BypassCache = true
	c.Provider = os.Getenv("IMAGE_FILTER_TEST_PROVIDER")
	images := workloadImages(b)
	for _, backend := range []Backend{Libvips, Go} {
		for _, format := range fixtureFormats {
			for _, callers := range []int{1, 8} {
				b.Run(string(backend)+"/"+format+"/"+strconv.Itoa(callers), func(b *testing.B) {
					s := nativeServiceWithPlan(b, c, automaticPlan(c, resource.Detect()))
					benchmarkFilterImages(b, s.WithBackend(backend), [][]byte{images[format]}, callers)
				})
			}
		}
	}
}
