package cmd

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/server"
	"github.com/caitun/inferkit/services/imagefilter"
)

// ErrImageFilterCommand classifies CLI input and output failures.
var ErrImageFilterCommand = errors.New("image filter command failed")

var imageFilterCmd = &cobra.Command{
	Use:   "image-filter SOURCE [SOURCE...]",
	Short: "Classify local images or HTTP(S) image URLs on CPU (JSON Lines output)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runImageFilter,
}

func init() {
	rootCmd.AddCommand(imageFilterCmd)
	imageFilterCmd.Flags().String("preprocess", string(imagefilter.Libvips), "Image preprocessing backend: libvips or go")
	validate := &cobra.Command{Use: "validate", Short: "Compare exported tensors and images with validation.json", Args: cobra.NoArgs, RunE: validateImageFilter}
	validate.Flags().String("preprocess", string(imagefilter.Libvips), "Image preprocessing backend: libvips or go")
	imageFilterCmd.AddCommand(validate)
	imageFilterCmd.AddCommand(&cobra.Command{
		Use: "serve", Short: "Start the image API without Redis or queue initialization", Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return validateConfigCenterSettings() }, RunE: serveImageFilter,
	})
}

func serveImageFilter(cmd *cobra.Command, _ []string) error {
	return withImageFilter(cmd.Context(), func(service *imagefilter.Service) error {
		s := server.NewServer(viper.GetString("port"), viper.GetString("mode"))
		if err := configureImageServer(cmd.Context(), s, service); err != nil {
			return err
		}
		s.Start(cmd.Context())
		return nil
	})
}

func validateImageFilter(cmd *cobra.Command, _ []string) error {
	return withImageDetector(cmd, func(detector imagefilter.Detector) error {
		results, validationErr := detector.Validate(cmd.Context())
		if err := json.MarshalWrite(cmd.OutOrStdout(), results); err != nil {
			return errors.Join(ErrImageFilterCommand, err)
		}
		if validationErr != nil {
			return errors.Join(ErrImageFilterCommand, validationErr)
		}
		return nil
	})
}

func withImageFilter(ctx context.Context, run func(*imagefilter.Service) error) (returnErr error) {
	service, err := imagefilter.NewContext(ctx, imagefilter.Configuration(viper.GetViper()))
	if err != nil {
		return errors.Join(ErrImageFilterCommand, err)
	}
	defer func() { returnErr = errors.Join(returnErr, service.Close()) }()
	return run(service)
}

func runImageFilter(cmd *cobra.Command, args []string) error {
	return withImageDetector(cmd, func(detector imagefilter.Detector) error {
		return writeSourceResults(cmd, args, ErrImageFilterCommand, detector.Check)
	})
}

func withImageDetector(cmd *cobra.Command, run func(imagefilter.Detector) error) error {
	value, err := cmd.Flags().GetString("preprocess")
	if err != nil {
		return errors.Join(ErrImageFilterCommand, err)
	}
	backend, err := imagefilter.ParseBackend(value)
	if err != nil {
		return errors.Join(ErrImageFilterCommand, err)
	}
	return withImageFilter(cmd.Context(), func(service *imagefilter.Service) error { return run(service.WithBackend(backend)) })
}
