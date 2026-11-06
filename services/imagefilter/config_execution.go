package imagefilter

import (
	"strconv"

	"github.com/spf13/viper"
)

const automaticThreads = "auto"

func readExecution(v *viper.Viper, c *Config) {
	if value := v.GetString("image.filter.threads"); value != "" && value != automaticThreads {
		c.Threads = executionCount(value)
	}
	if v.IsSet("image.filter.warmupRuns") {
		c.WarmupRuns = executionCount(v.GetString("image.filter.warmupRuns"))
	}
	if v.IsSet("image.filter.coremlCache") {
		c.CoreMLCache = v.GetBool("image.filter.coremlCache")
	}
}

func executionCount(value string) int {
	count, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return count
}
