package handler

import (
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
)

const imageFilterEntry = "src/image-filter/main.js"

// ImageFilterPage serves the workbench shell; inference still requires API authentication.
func ImageFilterPage(service ImageChecker) base.HandlerFunc {
	maxBytes, timeout, _ := service.Limits()
	return func(c *base.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		data := pageData(c, imageFilterEntry, "图片检测 · InferKit")
		data["max_bytes"], data["timeout_ms"] = maxBytes, timeout.Milliseconds()
		data["review_enabled"] = viper.GetBool("image.review.enabled")
		c.View("image-filter.index", data)
	}
}
