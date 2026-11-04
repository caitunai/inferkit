package route

import (
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/api/handler"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagefilter"
)

// InitRoute performs the init route operation.
func InitRoute(r *base.Router, filter *imagefilter.Service, records *db.ImageStore, collect bool) {
	InitMiddleware()
	r.Use(AttemptAuth())

	initPackageHandler(r)
	if filter != nil {
		r.GET("/image-filter", handler.ImageFilterPage(filter))
	}
	initConfigCenterHandler(r)
	initImageReviewHandler(r, records)
	initAPIHandler(r, filter, records, collect)
}

func initImageReviewHandler(r *base.Router, records *db.ImageStore) {
	if records == nil || imageReviewAuth == nil {
		return
	}
	review := r.Group("/image-review", imageReviewNoStore)
	review.RouterGroup.Use(imageReviewAuth)
	review.GET("", handler.ImageReviewPage(false))
	review.GET("/review", handler.ImageReviewPage(true))
	handler.ImageReviewControl(review.Group("/api"), records)
}

func initPackageHandler(r *base.Router) {
	r.GET("/", handler.HomePage)
	r.GET("/assets/*filepath", handler.ServeAssetFile)
	r.HEAD("/assets/*filepath", handler.ServeAssetFile)
	r.NoRoute(handler.ServeRootStaticFiles)
}

func initConfigCenterHandler(r *base.Router) {
	if !viper.GetBool("configcenter.enabled") {
		return
	}
	configCenter := r.Group("/config-center", configCenterEnabled, configCenterNoStore)
	if configCenterAuth != nil {
		configCenter.RouterGroup.Use(configCenterAuth)
	}
	configCenter.GET("", handler.ConfigCenterPage)
	handler.ConfigCenterControl(configCenter.Group("/api"))

	configCenterRuntime := r.Group("/config-center/api", configCenterEnabled)
	handler.ConfigCenterRuntimeControl(configCenterRuntime)
}

func initAPIHandler(r *base.Router, filter *imagefilter.Service, records *db.ImageStore, collect bool) {
	api := r.Group("/api", apiAuthorized)
	api.GET("/", handler.APIHomePage)
	if filter != nil {
		var recorder handler.ImageRecorder
		if records != nil && collect {
			recorder = records
		}
		api.POST("/image/filter", handler.FilterImage(filter, recorder))
	}

	// Add CRUD services for db.User
	handler.UserControl(api.Group("/users"))
}
