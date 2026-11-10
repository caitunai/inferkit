package cmd

import (
	"context"
	"errors"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/pulse"
)

func newPulseClient(ctx context.Context, v *viper.Viper) (*pulse.Client, error) {
	if !v.GetBool("pulse.app.enabled") {
		return nil, nil //nolint:nilnil // Disabled optional reporting has no client and is not an error.
	}
	client, err := pulse.NewContext(ctx, pulse.Configuration(v))
	if err != nil {
		return nil, errors.Join(ErrServeCommand, err)
	}
	return client, nil
}
