package cmd

import (
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
	imageFilterCmd.AddCommand(&cobra.Command{Use: "validate", Short: "Compare exported tensors and images with validation.json", Args: cobra.NoArgs, RunE: validateImageFilter})
	imageFilterCmd.AddCommand(&cobra.Command{
		Use: "serve", Short: "Start the image API without Redis or queue initialization", Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return validateConfigCenterSettings() }, RunE: serveImageFilter,
	})
}

func serveImageFilter(cmd *cobra.Command, _ []string) error {
	return withImageFilter(func(service *imagefilter.Service) error {
		s := server.NewServer(viper.GetString("port"), viper.GetString("mode"))
		if err := configureImageServer(cmd.Context(), s, service); err != nil {
			return err
		}
		s.Start(cmd.Context())
		return nil
	})
}

func validateImageFilter(cmd *cobra.Command, _ []string) error {
	return withImageFilter(func(service *imagefilter.Service) error {
		results, validationErr := service.Validate(cmd.Context())
		if err := json.MarshalWrite(cmd.OutOrStdout(), results); err != nil {
			return errors.Join(ErrImageFilterCommand, err)
		}
		if validationErr != nil {
			return errors.Join(ErrImageFilterCommand, validationErr)
		}
		return nil
	})
}

func withImageFilter(run func(*imagefilter.Service) error) (returnErr error) {
	service, err := imagefilter.New(imagefilter.Configuration(viper.GetViper()))
	if err != nil {
		return errors.Join(ErrImageFilterCommand, err)
	}
	defer func() { returnErr = errors.Join(returnErr, service.Close()) }()
	return run(service)
}

func runImageFilter(cmd *cobra.Command, args []string) error {
	return withImageFilter(func(service *imagefilter.Service) error { return writeImageResults(cmd, service, args) })
}

func writeImageResults(cmd *cobra.Command, service *imagefilter.Service, args []string) error {
	return writeSourceResults(cmd, args, ErrImageFilterCommand, service.Check)
}
