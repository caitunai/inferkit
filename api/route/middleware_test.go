package route

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
)

const (
	testConfigCenterUsername = "config-admin"
	testConfigCenterPassword = "correct horse battery staple"
	testBearerToken          = "token-value"
	testBasicAuthChallenge   = `Basic realm="Authorization Required"`
)

//nolint:funlen,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestConfigCenterAccessMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		username      string
		password      string
		requestUser   string
		requestPass   string
		wantStatus    int
		enabled       bool
		setBasicAuth  bool
		wantChallenge bool
	}{
		{
			name:       "disabled is hidden",
			enabled:    false,
			username:   testConfigCenterUsername,
			password:   testConfigCenterPassword,
			wantStatus: http.StatusNotFound,
		},
		{
			name:          "missing credentials",
			enabled:       true,
			username:      testConfigCenterUsername,
			password:      testConfigCenterPassword,
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: true,
		},
		{
			name:          "wrong credentials",
			enabled:       true,
			username:      testConfigCenterUsername,
			password:      testConfigCenterPassword,
			requestUser:   testConfigCenterUsername,
			requestPass:   "wrong password value",
			setBasicAuth:  true,
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: true,
		},
		{
			name:         "valid credentials",
			enabled:      true,
			username:     testConfigCenterUsername,
			password:     testConfigCenterPassword,
			requestUser:  testConfigCenterUsername,
			requestPass:  testConfigCenterPassword,
			setBasicAuth: true,
			wantStatus:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configureConfigCenterAccess(tt.enabled, tt.username, tt.password)
			engine := gin.New()
			router := base.NewRouter(engine)
			group := router.Group("/config-center", configCenterEnabled, configCenterNoStore)
			if configCenterAuth != nil {
				group.RouterGroup.Use(configCenterAuth)
			}
			group.GET("", func(c *base.Context) {
				if tt.wantStatus == http.StatusOK && c.GetString(gin.AuthUserKey) != tt.username {
					t.Errorf("authenticated user = %q, want %q", c.GetString(gin.AuthUserKey), tt.username)
				}
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/config-center", nil)
			if tt.setBasicAuth {
				request.SetBasicAuth(tt.requestUser, tt.requestPass)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if got := response.Header().Get("WWW-Authenticate"); tt.wantChallenge && got != testBasicAuthChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, testBasicAuthChallenge)
			}
		})
	}
}

func TestConfigCenterRuntimeOnlyUsesEnabledMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configureConfigCenterAccess(true, testConfigCenterUsername, testConfigCenterPassword)
	engine := gin.New()
	router := base.NewRouter(engine)
	router.Group("/config-center/api", configCenterEnabled).GET("/runtime/test/production", func(c *base.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/config-center/api/runtime/test/production", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Fatalf("runtime API unexpectedly requested Basic Auth: %q", challenge)
	}
}

func TestGetBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization string
		want          string
	}{
		{name: "bearer", authorization: "Bearer " + testBearerToken, want: testBearerToken},
		{name: "case insensitive scheme", authorization: "bearer " + testBearerToken, want: testBearerToken},
		{name: "basic is ignored", authorization: "Basic YWRtaW46c2VjcmV0"},
		{name: "missing scheme is ignored", authorization: testBearerToken},
		{name: "empty bearer is ignored", authorization: "Bearer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := getBearerToken(tt.authorization); got != tt.want {
				t.Fatalf("getBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionUserIDDoesNotLogMissingCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	previousLogger := log.Logger
	log.Logger = zerolog.New(&output)
	t.Cleanup(func() {
		log.Logger = previousLogger
	})

	response := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(response)
	ginContext.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	if uid := sessionUserID(&base.Context{Context: ginContext}); uid != 0 {
		t.Fatalf("sessionUserID() = %d, want anonymous user", uid)
	}
	if strings.Contains(output.String(), "decode session cookie") {
		t.Fatalf("missing session cookie produced an error log: %s", output.String())
	}
}

func TestBearerUserIDAcceptsAPIUserWhenSubjectIsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		audience = "test-api-audience"
		issuer   = "test-api-issuer"
	)
	secret := []byte("test-api-signing-secret")
	previousSecret := jwtSecret
	previousAudience := viper.Get("auth.api.audience")
	previousIssuers := viper.Get("auth.api.issuers")
	t.Cleanup(func() {
		jwtSecret = previousSecret
		viper.Set("auth.api.audience", previousAudience)
		viper.Set("auth.api.issuers", previousIssuers)
	})
	jwtSecret = secret
	viper.Set("auth.api.audience", audience)
	viper.Set("auth.api.issuers", []string{issuer})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": 123,
		"aud": audience,
		"iss": issuer,
	})
	signedToken, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	response := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(response)
	ginContext.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	ginContext.Request.Header.Set("Authorization", "Bearer "+signedToken)
	requestContext := &base.Context{Context: ginContext}

	if uid := bearerUserID(requestContext); uid != 0 {
		t.Fatalf("bearerUserID() = %d, want 0 for an API user", uid)
	}
	apiUser := requestContext.GetAPIUser()
	if apiUser == nil {
		t.Fatal("bearerUserID() did not set the API user")
	}
	if apiUser.User != "" || apiUser.Issuer != issuer {
		t.Fatalf("API user = %#v, want empty subject and issuer %q", apiUser, issuer)
	}
}

func TestIsConfigCenterPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/config-center", want: true},
		{path: "/config-center/", want: true},
		{path: "/config-center/api/runtime/service/production", want: true},
		{path: "/config-center-other", want: false},
		{path: "/api/config-center", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := isConfigCenterPath(tt.path); got != tt.want {
				t.Fatalf("isConfigCenterPath(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}
