package base

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUnauthorizedAPIKeyUsesCallerRealm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router := NewRouter(engine)
	router.GET("/protected", func(c *Context) {
		c.UnauthorizedAPIKey("shared-api", "invalid API key", gin.H{})
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	const wantChallenge = `ApiKey realm="shared-api"`
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", challenge, wantChallenge)
	}
}

func TestUnauthorizedAPIKeyQuotesCallerRealm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router := NewRouter(engine)
	router.GET("/protected", func(c *Context) {
		c.UnauthorizedAPIKey("shared\"api", "invalid API key", gin.H{})
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	const wantChallenge = `ApiKey realm="shared\"api"`
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", challenge, wantChallenge)
	}
}
