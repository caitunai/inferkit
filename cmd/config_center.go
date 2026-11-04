package cmd

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/spf13/viper"
)

const (
	configCenterUsernameMaxLength = 128
	configCenterPasswordMinLength = 16
	configCenterPasswordMaxLength = 256
)

var (
	// ErrConfigCenterSettings indicates invalid config center settings.
	ErrConfigCenterSettings = errors.New("invalid config center settings")
	// ErrConfigCenterUsernameRequired indicates config center username is required when enabled.
	ErrConfigCenterUsernameRequired = errors.New("config center username is required when enabled")
	// ErrConfigCenterUsernameInvalid indicates config center username must not have surrounding whitespace or contain a colon, and must be at most 128 characters.
	ErrConfigCenterUsernameInvalid = errors.New("config center username must not have surrounding whitespace or contain a colon, and must be at most 128 characters")
	// ErrConfigCenterPasswordRequired indicates config center password is required when enabled.
	ErrConfigCenterPasswordRequired = errors.New("config center password is required when enabled")
	// ErrConfigCenterPasswordInvalid indicates config center password must be between 16 and 256 characters.
	ErrConfigCenterPasswordInvalid = errors.New("config center password must be between 16 and 256 characters")
)

func validateConfigCenterSettings() error {
	return validateConfigCenterAccess(
		viper.GetBool("configcenter.enabled"),
		viper.GetString("configcenter.username"),
		viper.GetString("configcenter.password"),
	)
}

func validateConfigCenterAccess(enabled bool, username, password string) error {
	if !enabled {
		return nil
	}
	if strings.TrimSpace(username) == "" {
		return errors.Join(ErrConfigCenterSettings, ErrConfigCenterUsernameRequired)
	}
	if username != strings.TrimSpace(username) || strings.Contains(username, ":") ||
		utf8.RuneCountInString(username) > configCenterUsernameMaxLength {
		return errors.Join(ErrConfigCenterSettings, ErrConfigCenterUsernameInvalid)
	}
	if password == "" {
		return errors.Join(ErrConfigCenterSettings, ErrConfigCenterPasswordRequired)
	}
	passwordLength := utf8.RuneCountInString(password)
	if passwordLength < configCenterPasswordMinLength || passwordLength > configCenterPasswordMaxLength {
		return errors.Join(ErrConfigCenterSettings, ErrConfigCenterPasswordInvalid)
	}
	return nil
}
