package base

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/storage"
)

func TestPageAssetsFollowBrowserOrigin(t *testing.T) {
	const (
		entry     = "src/image-filter/main.js"
		secureURL = "https://local1.caitun.com/image-filter"
	)
	manifest := storage.ParseManifest()
	if len(manifest.GetCSSFiles(entry)) == 0 || len(manifest.GetJsFiles(entry)) == 0 {
		t.Skip("run npm run build in storage/ui before testing embedded page assets")
	}
	oldMode := viper.Get("ui.assetMode")
	viper.Set("ui.assetMode", "embedded")
	t.Cleanup(func() { viper.Set("ui.assetMode", oldMode) })
	for _, tc := range []struct {
		name, backendURL, pageURL, forwardedProto string
	}{
		{"HTTP", "http://localhost:8080/image-filter", "http://localhost:8080/image-filter", ""},
		{"HTTPS", secureURL, secureURL, ""},
		{"TLS terminated at proxy", "http://local1.caitun.com/image-filter", secureURL, ""},
		{"forwarded HTTPS", "http://backend:8080/image-filter", "https://local1.caitun.com:8443/image-filter", "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.backendURL, nil)
			context.Request.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			css, js := (&Context{Context: context}).GetCSSJsFiles(entry)
			if len(css) == 0 || len(js) == 0 {
				t.Fatal("page must include both stylesheet and script assets")
			}
			assertPageAssetOrigin(t, tc.pageURL, slices.Concat(css, js))
		})
	}
}

func assertPageAssetOrigin(t *testing.T, pageURL string, assets []string) {
	t.Helper()
	page, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if !strings.HasPrefix(asset, "/assets/") {
			t.Errorf("asset must use the browser origin: %q", asset)
		}
		ref, parseErr := url.Parse(asset)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		resolved := page.ResolveReference(ref)
		if resolved.Scheme != page.Scheme || resolved.Host != page.Host {
			t.Errorf("asset %q resolves to %s instead of browser origin %s", asset, resolved, page)
		}
	}
}
