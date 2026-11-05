package cmd

import (
	"errors"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/server"
	"github.com/caitun/inferkit/cache"
	"github.com/caitun/inferkit/queue"
	"github.com/caitun/inferkit/redis"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/ocr"
)

// ErrServeCommand indicates run serve command failed.
var ErrServeCommand = errors.New("run serve command failed")

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Command to start api server",
	Long:  "Start the server, you should set the config file, named: .app.toml",
	PreRunE: func(_ *cobra.Command, _ []string) error {
		if err := validateConfigCenterSettings(); err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, _ []string) (returnErr error) {
		var filter *imagefilter.Service
		if viper.GetString("image.filter.model") != "" {
			var initErr error
			filter, initErr = imagefilter.New(imagefilter.Configuration(viper.GetViper()))
			if initErr != nil {
				return errors.Join(ErrServeCommand, initErr)
			}
			defer func() { returnErr = errors.Join(returnErr, filter.Close()) }()
		}

		var recognizer *ocr.Service
		if viper.GetBool("image.ocr.enabled") {
			var initErr error
			recognizer, initErr = ocr.New(ocr.Configuration(viper.GetViper()))
			if initErr != nil {
				return errors.Join(ErrServeCommand, initErr)
			}
			defer func() { returnErr = errors.Join(returnErr, recognizer.Close()) }()
		}
		if err := redis.Init(cmd.Context()); err != nil {
			return errors.Join(ErrServeCommand, err)
		}
		cache.InitCache()
		err := queue.Init()
		if err != nil {
			log.Error().
				Err(err).
				Str("method", "Run").
				Str("package", "cmd").
				Str("command", "serve").
				Msg("init the queue publisher failed")
			return errors.Join(ErrServeCommand, err)
		}
		s := server.NewServer(viper.GetString("port"), viper.GetString("mode"))
		s.OCR = recognizer
		if setupErr := configureImageServer(cmd.Context(), s, filter); setupErr != nil {
			return errors.Join(ErrServeCommand, setupErr)
		}
		s.Start(cmd.Context())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
