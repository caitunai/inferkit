package route

import (
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagereview"
)

var imageReviewAuth gin.HandlerFunc

func configureImageReviewAccess() {
	imageReviewAuth = nil
	settings := imagereview.Configuration(viper.GetViper())
	if settings.Enabled && settings.Validate(true) == nil {
		imageReviewAuth = gin.BasicAuth(gin.Accounts{settings.Username: settings.Password})
	}
}

func imageReviewNoStore(c *base.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Authorization")
	c.Header("Referrer-Policy", "no-referrer")
	c.Next()
}
