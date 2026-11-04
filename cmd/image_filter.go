package cmd

import (
	"encoding/json/v2"
	"errors"
	"net/url"
	"os"

	"github.com/rs/zerolog/log"
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
		s.ImageFilter = service
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
	for _, source := range args {
		result, checkErr := checkImageArgument(cmd, service, source)
		if checkErr != nil {
			return checkErr
		}
		if err := json.MarshalWrite(cmd.OutOrStdout(), result); err != nil {
			return errors.Join(ErrImageFilterCommand, err)
		}
		if _, err := cmd.OutOrStdout().Write([]byte("\n")); err != nil {
			return errors.Join(ErrImageFilterCommand, err)
		}
	}
	return nil
}

func checkImageArgument(cmd *cobra.Command, service *imagefilter.Service, source string) (imagefilter.Result, error) {
	input, cleanup, err := openImageArgument(source)
	if err != nil {
		return imagefilter.Result{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	result, err := service.Check(cmd.Context(), input)
	if err != nil {
		return imagefilter.Result{}, errors.Join(ErrImageFilterCommand, err)
	}
	return result, nil
}

func openImageArgument(source string) (imagefilter.Source, func(), error) {
	u, err := url.Parse(source)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return imagefilter.Source{URL: source}, nil, nil
	}
	file, err := os.Open(source) //nolint:gosec // CLI users explicitly select local image files; HTTP never reaches this path.
	if err != nil {
		return imagefilter.Source{}, nil, errors.Join(ErrImageFilterCommand, err)
	}
	cleanup := func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("close image file")
		}
	}
	return imagefilter.Source{Reader: file}, cleanup, nil
}
