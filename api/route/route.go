package route

import (
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/api/handler"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/ocr"
	"github.com/caitun/inferkit/services/pulse"
	"github.com/caitun/inferkit/services/reclassify"
)

// InitRoute performs the init route operation.
func InitRoute(r *base.Router, filter *imagefilter.Service, records *db.ImageStore, collect bool, recognizer *ocr.Service, reporter EventReporter, tasks *reclassify.Service) {
	InitMiddleware()
	r.Use(AttemptAuth())
	r.Use(PageNavigation(filter != nil, recognizer != nil, records != nil && imageReviewAuth != nil))

	initPackageHandler(r)
	if filter != nil {
		r.GET("/image-filter", handler.ImageFilterPage(filter))
	}
	if recognizer != nil {
		r.GET("/ocr", handler.OCRPage(recognizer))
	}
	initConfigCenterHandler(r)
	initImageReviewHandler(r, records, tasks)
	initAPIHandler(r, filter, records, collect, recognizer, reporter)
}

func initImageReviewHandler(r *base.Router, records *db.ImageStore, tasks *reclassify.Service) {
	if records == nil || imageReviewAuth == nil {
		return
	}
	review := r.Group("/image-review", imageReviewNoStore)
	review.RouterGroup.Use(imageReviewAuth)
	review.GET("", handler.ImageReviewPage(false))
	review.GET("/review", handler.ImageReviewPage(true))
	handler.ImageReviewControl(review.Group("/api"), records)
	review.GET("/reclassify", handler.ImageReclassificationPage())
	var controller handler.ReclassificationController
	if tasks != nil {
		controller = tasks
	}
	handler.ImageReclassificationControl(review.Group("/api/reclassification"), controller)
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

func initAPIHandler(r *base.Router, filter *imagefilter.Service, records *db.ImageStore, collect bool, recognizer *ocr.Service, reporter EventReporter) {
	api := r.Group("/api", apiAuthorized)
	api.GET("/", handler.APIHomePage)
	if filter != nil {
		var recorder handler.ImageRecorder
		if records != nil && collect {
			recorder = records
		}
		native, goBackend := handler.FilterImageBackends(filter, recorder)
		usage := ImageUsage(reporter, pulse.ImageFilter)
		api.POST("/image/filter", usage, native)
		api.POST("/image/filter/libvips", usage, native)
		api.POST("/image/filter/go", usage, goBackend)
	}

	if recognizer != nil {
		api.POST("/image/ocr", ImageUsage(reporter, pulse.ImageOCR), handler.RecognizeImage(recognizer))
	}

	// Add CRUD services for db.User
	handler.UserControl(api.Group("/users"))
}
