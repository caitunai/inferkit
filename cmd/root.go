package cmd

import (
	"errors"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/cache"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/queue"
	"github.com/caitun/inferkit/redis"
	"github.com/caitun/inferkit/services/configload"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	// ErrCloseApplicationResources indicates close application resources failed.
	ErrCloseApplicationResources = errors.New("close application resources failed")
	// ErrExecuteApplicationCommand indicates execute application command failed.
	ErrExecuteApplicationCommand = errors.New("execute application command failed")
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:               "app",
	Short:             "A golang application",
	Long:              "A golang application server, with api and website ui",
	PersistentPreRunE: initializeRootConfiguration,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := execute(); err != nil {
		os.Exit(1)
	}
}

func execute() (returnErr error) {
	defer func() {
		closeErr := closeApplicationResources()
		if closeErr == nil {
			return
		}
		log.Error().Err(closeErr).Msg("close application resources failed")
		returnErr = errors.Join(returnErr, closeErr)
	}()

	if err := rootCmd.Execute(); err != nil {
		return errors.Join(ErrExecuteApplicationCommand, err)
	}
	return nil
}

func closeApplicationResources() error {
	queueErr := queue.Close()
	cache.Close()
	redisErr := redis.Close()
	databaseErr := db.Close()
	closeErr := errors.Join(queueErr, redisErr, databaseErr)
	if closeErr != nil {
		return errors.Join(ErrCloseApplicationResources, closeErr)
	}
	return nil
}

func init() {
	cobra.EnableTraverseRunHooks = true
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(
		&cfgFile,
		"config",
		"",
		"config file (default is $HOME/.app.toml)",
	)
}

func initializeRootConfiguration(cmd *cobra.Command, _ []string) error {
	// The generate command bootstraps the first keyring and therefore cannot
	// depend on an already readable encrypted configuration source.
	if cmd != generateConfigKeyCmd {
		if err := configureConfigEncryption(); err != nil {
			return err
		}
	}
	enabled := viper.GetBool("configload.enabled")
	if !enabled {
		return nil
	}
	settings, err := publishedConfigLoadSettings(viper.GetViper())
	if err != nil {
		return errors.Join(ErrPublishedConfigLoad, err)
	}
	if !shouldLoadPublishedConfiguration(cmd, enabled, settings) {
		return nil
	}
	if loadErr := loadPublishedConfiguration(cmd.Context(), settings); loadErr != nil {
		return loadErr
	}
	return nil
}

func shouldLoadPublishedConfiguration(cmd *cobra.Command, enabled bool, settings configload.Settings) bool {
	return enabled && (cmd != generateConfigKeyCmd || !configload.UsesSource(settings, configload.SourceDatabase))
}

func initConfig() {
	if cfgFile != "" {
		// Use a config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)
		// Search config in home directory with name ".ai-canvas" (without extension).
		viper.AddConfigPath(".")
		viper.AddConfigPath(home)
		viper.SetConfigType("toml")
		viper.SetConfigName(".app")
	}
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err == nil {
		log.Trace().Msgf("Using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Trace().Err(err).Send()
	}
	if viper.GetString("db.database") != "" {
		db.Conn()
	}
	initLogger()
}

func initLogger() {
	if viper.GetString("mode") != "dev" {
		switch viper.GetString("logger.level") {
		case "debug":
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		case "info":
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
		case "warn":
			zerolog.SetGlobalLevel(zerolog.WarnLevel)
		case "error":
			zerolog.SetGlobalLevel(zerolog.ErrorLevel)
		}
	} else {
		consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr}
		log.Logger = log.Output(consoleWriter)
	}
	zerolog.DefaultContextLogger = &log.Logger
}
