package imagereview

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestCollectionAndAccessSettings(t *testing.T) {
	t.Parallel()
	v := viper.New()
	if Configuration(v).Store {
		t.Fatal("standalone inference must not require MySQL")
	}
	v.Set("db.database", "test")
	if !Configuration(v).Store {
		t.Fatal("configured databases should collect URL results by default")
	}
	v.Set("image.filter.recordResults", false)
	if Configuration(v).Store {
		t.Fatal("explicit opt-out ignored")
	}
	valid := Settings{Store: true, Enabled: true, Username: "review-admin", Password: strings.Repeat("x", 20)}
	if err := valid.Validate(true); err != nil {
		t.Fatal(err)
	}
	valid.Store = false
	if err := valid.Validate(true); err != nil {
		t.Fatal("historical results should remain reviewable when collection is disabled", err)
	}
	for _, scenario := range []struct {
		settings Settings
		database bool
	}{
		{settings: Settings{Store: true}},
		{settings: Settings{Enabled: true}, database: true},
		{settings: Settings{Store: true, Enabled: true, Username: "a:b", Password: valid.Password}, database: true},
		{settings: Settings{Store: true, Enabled: true, Username: valid.Username, Password: "short"}, database: true},
	} {
		if err := scenario.settings.Validate(scenario.database); !errors.Is(err, ErrSettings) {
			t.Fatalf("invalid settings accepted: %v", err)
		}
	}
}
