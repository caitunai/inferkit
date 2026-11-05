package cmd

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"os"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func writeSourceResults[T any](cmd *cobra.Command, args []string, failure error, check func(context.Context, imageinput.Source) (T, error)) error {
	for _, source := range args {
		result, err := checkSourceArgument(cmd, source, failure, check)
		if err != nil {
			return err
		}
		if err = json.MarshalWrite(cmd.OutOrStdout(), result); err != nil {
			return errors.Join(failure, err)
		}
		if _, err = cmd.OutOrStdout().Write([]byte("\n")); err != nil {
			return errors.Join(failure, err)
		}
	}
	return nil
}

func checkSourceArgument[T any](cmd *cobra.Command, source string, failure error, check func(context.Context, imageinput.Source) (T, error)) (result T, returnErr error) {
	u, err := url.Parse(source)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		result, err = check(cmd.Context(), imageinput.Source{URL: source})
	} else {
		file, openErr := os.Open(source) //nolint:gosec // Only CLI users explicitly select local image files.
		if openErr != nil {
			return result, errors.Join(failure, openErr)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("close image file")
				returnErr = errors.Join(failure, returnErr, closeErr)
			}
		}()
		result, err = check(cmd.Context(), imageinput.Source{Reader: file})
	}
	if err != nil {
		return result, errors.Join(failure, err)
	}
	return result, nil
}
