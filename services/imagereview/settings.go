// Package imagereview defines collection and administrator access settings.
package imagereview

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/spf13/viper"
)

// ErrSettings classifies invalid collection or administrator settings.
var ErrSettings = errors.New("invalid image review settings")

// Settings separates result collection from access to the review console.
type Settings struct {
	Username string
	Password string
	Store    bool
	Enabled  bool
}

// Configuration defaults to collecting URL results when MySQL is configured.
func Configuration(v *viper.Viper) Settings {
	store := v.GetString("db.database") != ""
	if v.IsSet("image.filter.recordResults") {
		store = v.GetBool("image.filter.recordResults")
	}
	return Settings{
		Store: store, Enabled: v.GetBool("image.review.enabled"),
		Username: v.GetString("image.review.username"), Password: v.GetString("image.review.password"),
	}
}

// Validate requires a database for collection and separate credentials for review.
func (s Settings) Validate(databaseConfigured bool) error {
	if (s.Store || s.Enabled) && !databaseConfigured {
		return ErrSettings
	}
	if !s.Enabled {
		return nil
	}
	if !validUsername(s.Username) {
		return ErrSettings
	}
	length := utf8.RuneCountInString(s.Password)
	if length < 16 || length > 256 {
		return ErrSettings
	}
	return nil
}

func validUsername(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		!strings.Contains(value, ":") && utf8.RuneCountInString(value) <= 128
}
