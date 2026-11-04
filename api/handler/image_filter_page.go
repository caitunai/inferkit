package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
)

const imageFilterEntry = "src/image-filter/main.js"

// ImageFilterPage serves the workbench shell; inference still requires API authentication.
func ImageFilterPage(service ImageChecker) base.HandlerFunc {
	maxBytes, timeout, _ := service.Limits()
	return func(c *base.Context) {
		cssFiles, jsFiles := c.GetCSSJsFiles(imageFilterEntry)
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.View("image-filter.index", gin.H{
			"title": "图片检测 · InferKit", "entry": imageFilterEntry,
			"use_vite":    viper.GetString("ui.assetMode") == "vite",
			"vite_origin": strings.TrimRight(viper.GetString("ui.viteDevOrigin"), "/"),
			"css_files":   cssFiles, "js_files": jsFiles,
			"max_bytes": maxBytes, "timeout_ms": timeout.Milliseconds(),
		})
	}
}
