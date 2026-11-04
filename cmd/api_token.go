package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/apitoken"
)

// ErrAPITokenCommand classifies API credential creation and output failures.
var ErrAPITokenCommand = errors.New("create API token failed")

func init() { rootCmd.AddCommand(newAPITokenCommand(viper.GetViper())) }

func newAPITokenCommand(config *viper.Viper) *cobra.Command {
	var subject, issuer string
	var lifetime time.Duration
	command := &cobra.Command{Use: "api-token", Short: "Manage API client tokens", Args: cobra.NoArgs}
	create := &cobra.Command{
		Use: "create", Short: "Create an HS256 API token (default lifetime: 365 days)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := apitoken.Settings{
				Secret: []byte(config.GetString("auth.api.secret")), Audience: config.GetString("auth.api.audience"),
				Issuers: config.GetStringSlice("auth.api.issuers"), Issuer: issuer, Lifetime: lifetime,
			}
			token, err := apitoken.Issue(settings, subject, time.Now())
			if err != nil {
				return errors.Join(ErrAPITokenCommand, err)
			}
			if _, err = fmt.Fprintln(cmd.OutOrStdout(), token); err != nil {
				return errors.Join(ErrAPITokenCommand, err)
			}
			return nil
		},
	}
	create.Flags().StringVar(&subject, "subject", "api-client", "nonnumeric API client identifier")
	create.Flags().StringVar(&issuer, "issuer", "", "issuer (default: first entry of auth.api.issuers)")
	create.Flags().DurationVar(&lifetime, "expires-in", apitoken.DefaultLifetime, "token lifetime; one year is 365 days")
	command.AddCommand(create)
	return command
}
