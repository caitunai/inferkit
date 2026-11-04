package cmd

import (
	"errors"
	"strings"
	"testing"
)

const (
	testConfigCenterUsername = "config-admin"
	testConfigCenterPassword = "correct horse battery staple"
)

type configCenterValidationError uint8

const (
	configCenterValidationOK configCenterValidationError = iota
	configCenterValidationUsernameRequired
	configCenterValidationUsernameInvalid
	configCenterValidationPasswordRequired
	configCenterValidationPasswordInvalid
)

//nolint:funlen,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestValidateConfigCenterAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		username  string
		password  string
		wantError configCenterValidationError
		enabled   bool
	}{
		{
			name:    "disabled does not require credentials",
			enabled: false,
		},
		{
			name:     "valid credentials",
			enabled:  true,
			username: testConfigCenterUsername,
			password: testConfigCenterPassword,
		},
		{
			name:      "missing username",
			enabled:   true,
			password:  testConfigCenterPassword,
			wantError: configCenterValidationUsernameRequired,
		},
		{
			name:      "invalid username",
			enabled:   true,
			username:  " config-admin",
			password:  testConfigCenterPassword,
			wantError: configCenterValidationUsernameInvalid,
		},
		{
			name:      "username contains basic auth delimiter",
			enabled:   true,
			username:  "config:admin",
			password:  testConfigCenterPassword,
			wantError: configCenterValidationUsernameInvalid,
		},
		{
			name:      "missing password",
			enabled:   true,
			username:  testConfigCenterUsername,
			wantError: configCenterValidationPasswordRequired,
		},
		{
			name:      "short password",
			enabled:   true,
			username:  testConfigCenterUsername,
			password:  "too-short",
			wantError: configCenterValidationPasswordInvalid,
		},
		{
			name:      "long password",
			enabled:   true,
			username:  testConfigCenterUsername,
			password:  strings.Repeat("a", configCenterPasswordMaxLength+1),
			wantError: configCenterValidationPasswordInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConfigCenterAccess(tt.enabled, tt.username, tt.password)
			wantErr := configCenterValidationSentinel(tt.wantError)
			if wantErr == nil {
				if err != nil {
					t.Fatalf("validateConfigCenterAccess() error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrConfigCenterSettings) {
				t.Fatalf("error = %v, want classification %v", err, ErrConfigCenterSettings)
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want classification %v", err, wantErr)
			}
		})
	}
}

func configCenterValidationSentinel(kind configCenterValidationError) error {
	switch kind {
	case configCenterValidationUsernameRequired:
		return ErrConfigCenterUsernameRequired
	case configCenterValidationUsernameInvalid:
		return ErrConfigCenterUsernameInvalid
	case configCenterValidationPasswordRequired:
		return ErrConfigCenterPasswordRequired
	case configCenterValidationPasswordInvalid:
		return ErrConfigCenterPasswordInvalid
	case configCenterValidationOK:
		return nil
	}
	return ErrConfigCenterSettings
}
