package cmd

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/caitun/inferkit/services/imagefilter"
)

func TestImageFilterBackendFlags(t *testing.T) {
	t.Parallel()
	commands := append([]*cobra.Command{imageFilterCmd}, imageFilterCmd.Commands()...)
	for _, command := range commands {
		flag := command.Flags().Lookup("preprocess")
		if command.Name() == "serve" {
			if flag != nil {
				t.Fatal("serve must enable both API backends without a selector")
			}
			continue
		}
		if flag == nil || flag.DefValue != string(imagefilter.Libvips) {
			t.Fatalf("missing backend flag on %s", command.Name())
		}
	}
}

func TestInvalidImageBackendDoesNotLoadModel(t *testing.T) {
	t.Parallel()
	command := new(cobra.Command)
	command.Flags().String("preprocess", "unknown", "test backend")
	err := withImageDetector(command, func(imagefilter.Detector) error {
		t.Error("invalid selector reached the detector")
		return nil
	})
	if !errors.Is(err, ErrImageFilterCommand) || !errors.Is(err, imagefilter.ErrConfig) {
		t.Fatalf("backend validation identity lost: %v", err)
	}
}
