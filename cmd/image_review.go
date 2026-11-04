package cmd

import (
	"context"
	"errors"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/server"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imagereview"
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
	return nil
}
