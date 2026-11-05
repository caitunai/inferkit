//go:build linux

package resource

import "os"

func platformBudget() (Budget, error) { return linuxBudget(os.DirFS("/")) }
