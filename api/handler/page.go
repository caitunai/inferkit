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
		"use_vite":    viper.GetString("ui.assetMode") == "vite",
		"vite_origin": strings.TrimRight(viper.GetString("ui.viteDevOrigin"), "/"),
	}
}
