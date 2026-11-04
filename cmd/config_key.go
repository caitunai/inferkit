package cmd

import (
	"errors"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/configcrypt"
)

var (
	// ErrConfigKeyCommand indicates configuration encryption key command failed.
	ErrConfigKeyCommand = errors.New("configuration encryption key command failed")
	// ErrConfigEncryptionSetup indicates configuration encryption setup failed.
	ErrConfigEncryptionSetup = errors.New("configuration encryption setup failed")
	generatedKeyID           string
	generatedKeyring         string
)

var configKeyCmd = &cobra.Command{
	Use:   "config-key",
	Short: "Manage configuration center encryption keys",
	Args:  cobra.NoArgs,
}

var generateConfigKeyCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate an AES-256 key in the external configuration keyring",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		keyID := strings.TrimSpace(generatedKeyID)
		keyringPath := strings.TrimSpace(generatedKeyring)
		if keyringPath == "" {
			keyringPath = viper.GetString("configcrypt.keyring")
		}
		if err := configcrypt.GenerateFileKey(keyringPath, keyID); err != nil {
			return errors.Join(ErrConfigKeyCommand, err)
		}
		log.Info().
			Str("key_id", keyID).
			Str("keyring", keyringPath).
			Msg("configuration encryption key generated; update activeKey to activate it")
		return nil
	},
}

var reencryptConfigCmd = &cobra.Command{
	Use:   "reencrypt",
	Short: "Encrypt plaintext configuration data and rotate stored data keys",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := db.ReencryptConfigStorage(cmd.Context())
		if err != nil {
			return errors.Join(ErrConfigKeyCommand, err)
		}
		log.Info().
			Str("active_key_id", configcrypt.ActiveKeyID()).
			Int64("namespace_records", result.NamespaceRecords).
			Int64("environment_records", result.EnvironmentRecords).
			Int64("release_records", result.ReleaseRecords).
			Int64("payloads", result.Payloads).
			Msg("configuration storage encryption completed")
		return nil
	},
}

func configureConfigEncryption() error {
	settings := configcrypt.Settings{
		Enabled:     viper.GetBool("configcrypt.enabled"),
		Provider:    strings.TrimSpace(viper.GetString("configcrypt.provider")),
		ActiveKeyID: strings.TrimSpace(viper.GetString("configcrypt.activeKey")),
		KeyringPath: strings.TrimSpace(viper.GetString("configcrypt.keyring")),
	}
	if err := configcrypt.Configure(settings); err != nil {
		return errors.Join(ErrConfigEncryptionSetup, err)
	}
	if configcrypt.Enabled() {
		log.Info().Str("active_key_id", configcrypt.ActiveKeyID()).Msg("configuration encryption enabled")
	} else {
		log.Warn().Msg("configuration encryption disabled")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(configKeyCmd)
	configKeyCmd.AddCommand(generateConfigKeyCmd)
	configKeyCmd.AddCommand(reencryptConfigCmd)

	generateConfigKeyCmd.Flags().StringVar(&generatedKeyID, "id", "", "unique key ID, for example config-key-2026-08")
	generateConfigKeyCmd.Flags().StringVar(&generatedKeyring, "keyring", "", "absolute external keyring path (defaults to configcrypt.keyring)")
	cobra.CheckErr(generateConfigKeyCmd.MarkFlagRequired("id"))
}
