package route

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
)

// EventReporter is the HTTP layer's narrow usage reporting contract.
type EventReporter interface {
	Enqueue(context.Context, string, time.Time, time.Duration) error
}

// ImageUsage counts requests admitted by apiAuthorized, including invalid input,
// busy responses and inference failures. Delivery cannot replace the API result.
func ImageUsage(reporter EventReporter, eventName string) base.HandlerFunc {
	return func(c *base.Context) {
		admittedAt := time.Now()
		// Inference handlers replace Request.Context with a timeout context and
		// cancel it on return. Retain the incoming request context for reporting.
		ctx := c.Request.Context()
		defer func() {
			if reporter == nil {
				return
			}
			if err := reporter.Enqueue(ctx, eventName, admittedAt, time.Since(admittedAt)); err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("event_name", eventName).Msg("Pulse usage reporting failed")
			}
		}()
		c.Next()
	}
}

var (
	jwtSecret             []byte
	publicKey             *rsa.PublicKey
	oauthCallbackPath     = "oauth/path/to/callback"
	configCenterIsEnabled bool
	configCenterAuth      gin.HandlerFunc
	// ErrJWTSigningMethod indicates unsupported JWT signing method.
	ErrJWTSigningMethod = errors.New("unsupported JWT signing method")
	// ErrJWTPublicKeyAbsent indicates JWT public key not found.
	ErrJWTPublicKeyAbsent = errors.New("JWT public key not found")
)

const unauthorizedMessage = "unauthorized"

// InitMiddleware performs the init middleware operation.
func InitMiddleware() {
	configureImageReviewAccess()
	configureConfigCenterAccess(
		viper.GetBool("configcenter.enabled"),
		viper.GetString("configcenter.username"),
		viper.GetString("configcenter.password"),
	)
	jwtSecret = []byte(viper.GetString("auth.api.secret"))
	publicKeyByte, err := os.ReadFile(viper.GetString("oauth.publicKeyPath"))
	if err != nil {
		log.Error().Err(err).Msg("read oauth public key failed")
		return
	}
	publicKey, err = jwt.ParseRSAPublicKeyFromPEM(publicKeyByte)
	if err != nil {
		log.Error().Err(err).Msg("parse oauth public key failed")
		return
	}
}

func configureConfigCenterAccess(enabled bool, username, password string) {
	configCenterIsEnabled = enabled
	configCenterAuth = nil
	if enabled {
		configCenterAuth = gin.BasicAuth(gin.Accounts{username: password})
	}
}

func configCenterEnabled(c *base.Context) {
	if !configCenterIsEnabled {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Next()
}

func configCenterNoStore(c *base.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Authorization")
	c.Next()
}

// This middleware can be used to verify the login status of real users.
func authorized(c *base.Context) {
	uid := c.GetUint("uid")
	if uid == 0 {
		login(c)
		c.Abort()
		return
	}

	c.Next()
}

// This middleware can be used to verify the authorization status of a JWT token for an API.
func apiAuthorized(c *base.Context) {
	apiUser := c.GetAPIUser()
	if apiUser == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			base.KeyStatus:  http.StatusForbidden,
			base.KeyMessage: unauthorizedMessage,
			base.KeyData:    gin.H{},
		})
		return
	}
	c.Next()
}

// AttemptAuth performs the attempt auth operation.
func AttemptAuth() base.HandlerFunc {
	return func(c *base.Context) {
		if isConfigCenterPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		uid := sessionUserID(c)
		if uid == 0 {
			uid = bearerUserID(c)
		}
		if uid == 0 && redirectWechatLogin(c) {
			return
		}
		c.Set("uid", uint(uid))
		c.Next()
	}
}

func sessionUserID(c *base.Context) uint64 {
	id, err := c.DecodeCookie("session_id")
	if err != nil {
		if !errors.Is(err, http.ErrNoCookie) {
			log.Debug().Err(err).Msg("decode session cookie")
		}
		return 0
	}
	if id == "" {
		return 0
	}
	uid, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		log.Debug().Err(err).Msg("parse session user ID")
		return 0
	}
	return uid
}

func bearerUserID(c *base.Context) uint64 {
	bearerToken := getBearerToken(c.GetHeader("Authorization"))
	if bearerToken == "" {
		return 0
	}
	token, err := jwt.Parse(bearerToken, jwtVerificationKey)
	if err != nil {
		log.Error().Err(err).Msg("parse bearer token error")
		return 0
	}
	subject, subjectErr := token.Claims.GetSubject()
	if subjectErr != nil {
		log.Error().Err(subjectErr).Msg("get token subject error")
	}
	accountID, parseErr := strconv.ParseUint(subject, 10, 64)
	if parseErr != nil {
		setAPIUser(c, token, subject)
		return 0
	}
	return persistedUserID(c, accountID)
}

func jwtVerificationKey(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodHMAC); ok {
		return jwtSecret, nil
	}
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, ErrJWTSigningMethod
	}
	if publicKey == nil {
		return nil, ErrJWTPublicKeyAbsent
	}
	return publicKey, nil
}

func setAPIUser(c *base.Context, token *jwt.Token, subject string) {
	audiences, err := token.Claims.GetAudience()
	if err != nil || !slices.Contains(audiences, viper.GetString("auth.api.audience")) {
		return
	}
	issuer, err := token.Claims.GetIssuer()
	if err != nil || !slices.Contains(viper.GetStringSlice("auth.api.issuers"), issuer) {
		return
	}
	c.SetAPIUser(&base.APIUser{User: subject, Issuer: issuer})
}

func persistedUserID(c *base.Context, accountID uint64) uint64 {
	if !c.IsDatabaseEnabled() {
		return accountID
	}
	user, err := db.RegisterUser(c.Request.Context(), uint(accountID))
	if err != nil || user == nil {
		return 0
	}
	c.SetUser(user)
	return uint64(user.ID)
}

func redirectWechatLogin(c *base.Context) bool {
	if c.IsWechatMiniProgram() {
		return false
	}
	userAgent := strings.ToLower(c.GetHeader("user-agent"))
	isWechat := strings.Contains(userAgent, "micromessenger")
	isCallback := strings.Contains(c.Request.URL.Path, oauthCallbackPath)
	if !isWechat || isCallback {
		return false
	}
	login(c)
	c.Abort()
	return true
}

func isConfigCenterPath(path string) bool {
	return path == "/config-center" || strings.HasPrefix(path, "/config-center/")
}

func getBearerToken(authorization string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// AuthorizedAllowSpider performs the authorized allow spider operation.
func AuthorizedAllowSpider() base.HandlerFunc {
	return func(c *base.Context) {
		ag := strings.ToLower(c.GetHeader("user-agent"))
		if strings.Contains(ag, "twitterbot") ||
			c.Request.Method == http.MethodGet ||
			c.Request.Method == http.MethodHead ||
			c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		// 处理登陆逻辑
		authorized(c)
	}
}

func login(c *base.Context) {
	c.Unauthorized("you should login", gin.H{
		"result": "you are not implement the login function",
	})
}

// PageNavigation exposes only initialized feature links to shared page templates.
func PageNavigation(filter, recognizer, review bool) base.HandlerFunc {
	return func(c *base.Context) {
		c.Set("navigation.image_filter", filter)
		c.Set("navigation.ocr", recognizer)
		c.Set("navigation.image_review", review)
		c.Set("navigation.config_center", configCenterIsEnabled)
		c.Next()
	}
}
