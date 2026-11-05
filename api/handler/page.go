package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
)

func pageData(c *base.Context, entry, title string) gin.H {
	css, js := c.GetCSSJsFiles(entry)
	return gin.H{
		"title": title, "entry": entry, "css_files": css, "js_files": js,
		"navigation":  pageNavigation(c),
		"use_vite":    viper.GetString("ui.assetMode") == "vite",
		"vite_origin": strings.TrimRight(viper.GetString("ui.viteDevOrigin"), "/"),
	}
}

// navigationItem describes a link to a feature initialized by the running server.
type navigationItem struct {
	Path   string
	Label  string
	Active bool
}

func pageNavigation(c *base.Context) []navigationItem {
	items := make([]navigationItem, 0, 4)
	for _, feature := range []struct{ key, path, label string }{
		{"navigation.image_filter", "/image-filter", "图片检测"},
		{"navigation.ocr", "/ocr", "OCR 识别"},
		{"navigation.image_review", "/image-review", "识别记录"},
		{"navigation.config_center", "/config-center", "配置中心"},
	} {
		if !c.GetBool(feature.key) {
			continue
		}
		active := c.Request.URL.Path == feature.path || strings.HasPrefix(c.Request.URL.Path, feature.path+"/")
		items = append(items, navigationItem{Path: feature.path, Label: feature.label, Active: active})
	}
	return items
}
