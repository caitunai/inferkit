package apitoken

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testSettings() Settings {
	return Settings{Secret: []byte(strings.Repeat("s", 32)), Audience: "inferkit-api", Issuers: []string{"primary", "secondary"}, Lifetime: DefaultLifetime}
}

func TestIssueClaimsAndExpiry(t *testing.T) {
	t.Parallel()
	settings := testSettings()
	now := time.Unix(1_800_000_000, 0)
	value, err := Issue(settings, "image-filter-client", now)
	if err != nil {
		t.Fatal(err)
	}
	claims := new(jwt.RegisteredClaims)
	key := func(*jwt.Token) (any, error) { return settings.Secret, nil }
	_, err = jwt.ParseWithClaims(value, claims, key, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(settings.Audience), jwt.WithIssuer("primary"), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "image-filter-client" || claims.ID == "" || !claims.IssuedAt.Equal(now) {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if !claims.ExpiresAt.Equal(now.Add(DefaultLifetime)) {
		t.Fatalf("expected one year: %v", claims.ExpiresAt)
	}
	_, err = jwt.Parse(value, key, jwt.WithTimeFunc(func() time.Time { return now.Add(DefaultLifetime) }))
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("expected expired token: %v", err)
	}
}

func TestIssueSelectedIssuerAndLifetime(t *testing.T) {
	t.Parallel()
	settings := testSettings()
	settings.Issuer, settings.Lifetime = "secondary", time.Hour
	now := time.Now().Truncate(time.Second)
	value, err := Issue(settings, "client", now)
	if err != nil {
		t.Fatal(err)
	}
	claims := new(jwt.RegisteredClaims)
	_, err = jwt.ParseWithClaims(value, claims, func(*jwt.Token) (any, error) { return settings.Secret, nil }, jwt.WithIssuer("secondary"))
	if err != nil || !claims.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("selected settings: %+v %v", claims, err)
	}
}

func TestIssueRejectsInvalidSettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		change func(*Settings)
		want   error
	}{
		{func(s *Settings) { s.Secret = []byte("short") }, ErrSettings},
		{func(s *Settings) { s.Audience = "" }, ErrSettings},
		{func(s *Settings) { s.Issuers = nil }, ErrSettings},
		{func(s *Settings) { s.Issuer = "not-allowed" }, ErrSettings},
		{func(s *Settings) { s.Lifetime = 0 }, ErrLifetime},
	}
	for _, test := range cases {
		settings := testSettings()
		test.change(&settings)
		if _, err := Issue(settings, "client", time.Now()); !errors.Is(err, test.want) {
			t.Errorf("wanted %v, got %v", test.want, err)
		}
	}
	for _, subject := range []string{"", "   ", "123", "18446744073709551616", strings.Repeat("a", 129)} {
		if _, err := Issue(testSettings(), subject, time.Now()); !errors.Is(err, ErrSubject) {
			t.Errorf("subject %q: %v", subject, err)
		}
	}
}
