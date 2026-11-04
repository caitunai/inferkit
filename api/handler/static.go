package handler

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/storage"
)

var (
	rootStaticFileServer = http.FileServer(http.FS(storage.Static))
	assetFileServer      = http.StripPrefix("/assets/", http.FileServer(http.FS(storage.Assets)))
)

// ServeRootStaticFiles performs the serve root static files operation.
func ServeRootStaticFiles(c *base.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.NotFound("resource not found", gin.H{})
		return
	}
	staticPath := strings.TrimPrefix(c.Request.URL.Path, "/")
	if staticPath == "" || !fs.ValidPath(staticPath) || rootStaticPathHidden(staticPath) {
		c.NotFound("resource not found", gin.H{})
		return
	}
	fileInfo, err := fs.Stat(storage.Static, staticPath)
	if err != nil || fileInfo.IsDir() {
		c.NotFound("resource not found", gin.H{})
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	rootStaticFileServer.ServeHTTP(c.Writer, c.Request)
}

// ServeAssetFile performs the serve asset file operation.
func ServeAssetFile(c *base.Context) {
	filepath := strings.TrimPrefix(c.Param("filepath"), "/")
	if filepath == "" || !fs.ValidPath(filepath) {
		c.NotFound("resource not found", gin.H{})
		return
	}
	fileInfo, err := fs.Stat(storage.Assets, filepath)
	if err != nil || fileInfo.IsDir() {
		c.NotFound("resource not found", gin.H{})
		return
	}
	if isImmutableAsset(filepath) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "public, max-age=60")
	}
	assetFileServer.ServeHTTP(c.Writer, c.Request)
}

func rootStaticPathHidden(path string) bool {
	return strings.HasPrefix(path, ".") || strings.Contains(path, "/.")
}

func isImmutableAsset(filepath string) bool {
	return strings.HasSuffix(filepath, ".css") ||
		strings.HasSuffix(filepath, ".js") ||
		strings.HasSuffix(filepath, ".woff2") ||
		strings.HasSuffix(filepath, ".woff") ||
		strings.HasSuffix(filepath, ".png") ||
		strings.HasSuffix(filepath, ".jpg") ||
		strings.HasSuffix(filepath, ".jpeg") ||
		strings.HasSuffix(filepath, ".svg") ||
		strings.HasSuffix(filepath, ".webp")
}
