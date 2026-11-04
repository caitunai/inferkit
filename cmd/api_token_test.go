package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/apitoken"
)

func tokenCommand(t *testing.T, output *bytes.Buffer) (*cobra.Command, *viper.Viper) {
	t.Helper()
	config := viper.New()
	config.Set("auth.api.secret", strings.Repeat("x", 32))
	config.Set("auth.api.audience", "test-api")
	config.Set("auth.api.issuers", []string{"test-issuer"})
	command := newAPITokenCommand(config)
	command.SetOut(output)
	create, _, err := command.Find([]string{"create"})
	if err != nil {
		t.Fatal(err)
	}
	return create, config
}

func TestCreateAPITokenCommandDefaults(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	create, config := tokenCommand(t, &output)
	if err := create.RunE(create, nil); err != nil {
		t.Fatal(err)
	}
	claims := new(jwt.RegisteredClaims)
	_, err := jwt.ParseWithClaims(strings.TrimSpace(output.String()), claims, func(*jwt.Token) (any, error) { return []byte(config.GetString("auth.api.secret")), nil }, jwt.WithAudience("test-api"), jwt.WithIssuer("test-issuer"), jwt.WithExpirationRequired())
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "api-client" || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != apitoken.DefaultLifetime {
		t.Fatalf("unexpected CLI claims: %+v", claims)
	}
	if len(strings.Fields(output.String())) != 1 {
		t.Fatal("stdout must contain only the token")
	}
}

func TestCreateAPITokenCommandCustomLifetime(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	create, config := tokenCommand(t, &output)
	if err := create.ParseFlags([]string{"--subject", "image-client", "--expires-in", "24h"}); err != nil {
		t.Fatal(err)
	}
	if err := create.RunE(create, nil); err != nil {
		t.Fatal(err)
	}
	claims := new(jwt.RegisteredClaims)
	_, err := jwt.ParseWithClaims(strings.TrimSpace(output.String()), claims, func(*jwt.Token) (any, error) { return []byte(config.GetString("auth.api.secret")), nil })
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "image-client" || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 24*time.Hour {
		t.Fatalf("custom CLI claims: %+v", claims)
	}
}

func TestCreateAPITokenCommandRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	create, config := tokenCommand(t, &output)
	config.Set("auth.api.secret", "")
	err := create.RunE(create, nil)
	if !errors.Is(err, apitoken.ErrSettings) || !errors.Is(err, ErrAPITokenCommand) || output.Len() != 0 {
		t.Fatalf("invalid config: %v %q", err, output.String())
	}
}
