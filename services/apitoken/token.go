// Package apitoken signs API client credentials for the application's JWT middleware.
package apitoken

import (
	"crypto/rand"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultLifetime is one year, defined as 365 days.
const DefaultLifetime = 365 * 24 * time.Hour

var (
	// ErrSettings indicates missing signing settings or an issuer outside the allowlist.
	ErrSettings = errors.New("API token requires an auth.api.secret of at least 32 bytes, audience and allowed issuer")
	// ErrSubject indicates an invalid API client identifier.
	ErrSubject = errors.New("API token subject must be a nonempty, nonnumeric client identifier of at most 128 bytes")
	// ErrLifetime indicates a lifetime too short for JWT second precision.
	ErrLifetime = errors.New("API token lifetime must be at least one second")
	// ErrSign indicates a JWT signing failure.
	ErrSign = errors.New("sign API token failed")
)

// Settings describes the existing HMAC API verifier configuration and token lifetime.
type Settings struct {
	Audience string
	Issuer   string
	Secret   []byte
	Issuers  []string
	Lifetime time.Duration
}

// Issue creates an HS256 token. An empty issuer selects the first configured issuer.
// Numeric subjects belong to the user-login branch and cannot authenticate API clients.
func Issue(settings Settings, subject string, now time.Time) (string, error) {
	if settings.Issuer == "" && len(settings.Issuers) > 0 {
		settings.Issuer = settings.Issuers[0]
	}
	if err := settings.validate(); err != nil {
		return "", err
	}
	subject = strings.TrimSpace(subject)
	if err := validateSubject(subject); err != nil {
		return "", err
	}
	claims := jwt.RegisteredClaims{
		Subject: subject, Issuer: settings.Issuer, Audience: jwt.ClaimStrings{settings.Audience},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(settings.Lifetime)),
		ID: rand.Text(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(settings.Secret)
	if err != nil {
		return "", errors.Join(ErrSign, err)
	}
	return token, nil
}

func (s Settings) validate() error {
	if len(s.Secret) < 32 || strings.TrimSpace(s.Audience) == "" || strings.TrimSpace(s.Issuer) == "" || !slices.Contains(s.Issuers, s.Issuer) {
		return ErrSettings
	}
	if s.Lifetime < time.Second {
		return ErrLifetime
	}
	return nil
}

func validateSubject(subject string) error {
	if subject == "" || len(subject) > 128 {
		return ErrSubject
	}
	if strings.IndexFunc(subject, func(char rune) bool { return char < '0' || char > '9' }) < 0 {
		return ErrSubject
	}
	return nil
}
