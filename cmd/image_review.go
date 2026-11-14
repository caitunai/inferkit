package cmd

import (
	"context"
	"errors"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/server"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imagereview"
	"github.com/caitun/inferkit/services/reclassify"
)

// ErrImageReviewStartup identifies missing settings or unapplied collection migrations.
var ErrImageReviewStartup = errors.New("initialize image result collection failed")

func configureImageServer(ctx context.Context, s *server.Server, filter *imagefilter.Service) error {
	s.ImageFilter = filter
	settings := imagereview.Configuration(viper.GetViper())
	if err := settings.Validate(viper.GetString("db.database") != ""); err != nil {
		return errors.Join(ErrImageReviewStartup, err)
	}
	s.CollectImageResults = settings.Store
	if !settings.Enabled && (!settings.Store || filter == nil) {
		return nil
	}
	modelHash := ""
	if filter != nil {
		modelHash = filter.ModelSHA256()
	}
	s.ImageRecords = db.NewImageStore(modelHash)
	if err := s.ImageRecords.Ready(ctx); err != nil {
		return errors.Join(ErrImageReviewStartup, err)
	}
	configureImageReclassification(ctx, s, filter, settings.Enabled)
	return nil
}

func configureImageReclassification(ctx context.Context, s *server.Server, filter *imagefilter.Service, enabled bool) {
	if !enabled || filter == nil {
		return
	}
	config := viper.GetViper()
	if config.IsSet("image.review.reclassify.enabled") && !config.GetBool("image.review.reclassify.enabled") {
		return
	}
	tasks, err := reclassify.New(ctx, reclassify.Configuration(config), s.ImageRecords, filter)
	if err != nil {
		// An unavailable maintenance workspace must not stop production APIs.
		log.Warn().Err(err).Msg("image reclassification console unavailable")
		return
	}
	s.ImageReclassification = tasks
}
