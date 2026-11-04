package base

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/storage"
	"github.com/caitun/inferkit/xutil"
)

// ErrCookieDecode indicates decode cookie failed.
var ErrCookieDecode = errors.New("decode cookie failed")

const (
	// HTTP identifies the "http" value.
	HTTP = "http"
	// HTTPS identifies the "https" value.
	HTTPS = "https"
	// KeyStatus identifies the "status" value.
	KeyStatus = "status"
	// KeyMessage identifies the "message" value.
	KeyMessage = "message"
	// KeyData identifies the "data" value.
	KeyData = "data"
)

// APIUser The api user information from bearer token, issuer is jwt iss attribute
// user is jwt sub attribute
type APIUser struct {
	User   string `json:"user"`
	Issuer string `json:"issuer"`
}

// Context represents context data.
type Context struct {
	*gin.Context
}

// Scheme performs the scheme operation.
func (c *Context) Scheme() string {
	// Can't use `r.Request.URL.Scheme`
	// See: https://groups.google.com/forum/#!topic/golang-nuts/pMUkBlQBDF0
	if c.Request.TLS != nil {
		return HTTPS
	}
	if scheme := c.GetHeader("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	if scheme := c.GetHeader("X-Forwarded-Protocol"); scheme != "" {
		return scheme
	}
	if ssl := c.GetHeader("X-Forwarded-Ssl"); ssl == "on" {
		return HTTPS
	}
	if scheme := c.GetHeader("X-Url-Scheme"); scheme != "" {
		return scheme
	}
	return HTTP
}

// Port performs the port operation.
func (c *Context) Port() string {
	port := c.GetHeader("X-Forwarded-Port")
	if port == "" {
		port = c.Request.URL.Port()
	}
	if port == "" {
		port = "80"
	}
	return port
}

// Origin performs the origin operation.
func (c *Context) Origin() string {
	scheme := c.Scheme()
	port := c.Port()
	if scheme == HTTP || port == "80" {
		port = ""
	}
	if scheme == HTTPS || port == "443" {
		port = ""
	}
	if c.Request.Host == "" {
		return viper.GetString("url")
	}
	if port == "" {
		return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
	}
	return fmt.Sprintf("%s://%s:%s", scheme, c.Request.Host, port)
}

// GetCSSJsFiles returns css js files.
func (c *Context) GetCSSJsFiles(entry string) (css, js []string) {
	if viper.GetString("ui.assetMode") == "vite" {
		return css, js
	}
	manifest := storage.ParseManifest()
	css = manifest.GetCSSFiles(entry)
	js = manifest.GetJsFiles(entry)
	prefix := c.Origin()
	for i, v := range css {
		css[i] = prefix + "/" + v
	}
	for i, v := range js {
		js[i] = prefix + "/" + v
	}
	return css, js
}

// Ok performs the ok operation.
func (c *Context) Ok(body string) {
	c.String(http.StatusOK, body)
}

// Success performs the success operation.
func (c *Context) Success(data gin.H) {
	c.JSON(http.StatusOK, gin.H{
		KeyStatus:  0,
		KeyMessage: "ok",
		KeyData:    data,
	})
}

// Error response with http status 500
//
// code the error code
//
// message the message of this error
//
// data the data map for this response
func (c *Context) Error(code int, message string, data gin.H) {
	c.Header("x-error-code", strconv.Itoa(code))
	c.response(http.StatusInternalServerError, gin.H{
		KeyStatus:  code,
		KeyMessage: message,
		KeyData:    data,
	})
}

// ErrorMessage response with http status 500
//
// message the error message to response
func (c *Context) ErrorMessage(message string) {
	c.response(http.StatusInternalServerError, gin.H{
		KeyStatus:  http.StatusInternalServerError,
		KeyMessage: message,
	})
}

// Unauthorized performs the unauthorized operation.
func (c *Context) Unauthorized(message string, data gin.H) {
	c.Header(
		"WWW-Authenticate",
		"Bearer error=\"invalid_token\", error_description=\"The token is invalid, request with a new token\"",
	)
	c.response(http.StatusUnauthorized, gin.H{
		KeyStatus:  http.StatusUnauthorized,
		KeyMessage: message,
		KeyData:    data,
	})
}

// UnauthorizedAPIKey performs the unauthorized api key operation.
func (c *Context) UnauthorizedAPIKey(realm, message string, data gin.H) {
	c.Header("WWW-Authenticate", "ApiKey realm="+strconv.Quote(realm))
	c.JSON(http.StatusUnauthorized, gin.H{
		KeyStatus:  http.StatusUnauthorized,
		KeyMessage: message,
		KeyData:    data,
	})
}

// Forbidden performs the forbidden operation.
func (c *Context) Forbidden(message string, data gin.H) {
	c.response(http.StatusForbidden, gin.H{
		KeyStatus:  http.StatusForbidden,
		KeyMessage: message,
		KeyData:    data,
	})
}

// Conflict performs the conflict operation.
func (c *Context) Conflict(message string, data gin.H) {
	c.response(http.StatusConflict, gin.H{
		KeyStatus:  http.StatusConflict,
		KeyMessage: message,
		KeyData:    data,
	})
}

// BadRequest performs the bad request operation.
func (c *Context) BadRequest(message string, data gin.H) {
	c.response(http.StatusBadRequest, gin.H{
		KeyStatus:  http.StatusBadRequest,
		KeyMessage: message,
		KeyData:    data,
	})
}

// ErrorForm identifies a or form failure.
func (c *Context) ErrorForm(message string, data gin.H) {
	c.response(http.StatusUnprocessableEntity, gin.H{
		KeyStatus:  http.StatusUnprocessableEntity,
		KeyMessage: message,
		KeyData:    data,
	})
}

// NotFound performs the not found operation.
func (c *Context) NotFound(message string, data gin.H) {
	c.response(http.StatusNotFound, gin.H{
		KeyStatus:  http.StatusNotFound,
		KeyMessage: message,
		KeyData:    data,
	})
}

// View performs the view operation.
func (c *Context) View(name string, data gin.H) {
	c.HTML(http.StatusOK, name, data)
}

func (c *Context) response(code int, data gin.H) {
	if !c.WantsJSONResponse() {
		if c.Request.Method == http.MethodHead {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.Status(code)
			return
		}

		message, ok := data["message"]
		if ok {
			data["title"] = message
		} else {
			data["title"] = "unknown error"
		}
		c.HTML(code, "errors."+strconv.Itoa(code), data)
		return
	}
	c.JSON(code, data)
}

// WantsJSONResponse reports whether json response.
func (c *Context) WantsJSONResponse() bool {
	if IsDocumentFetch(c) {
		return false
	}
	if strings.EqualFold(c.GetHeader("X-Requested-With"), "XMLHttpRequest") {
		return true
	}
	if headerHasJSONMediaType(c.GetHeader("Accept")) {
		return true
	}
	if headerHasJSONMediaType(c.GetHeader("Content-Type")) {
		return true
	}
	if strings.EqualFold(c.GetHeader("Sec-Fetch-Dest"), "empty") && !headerHasHTMLMediaType(c.GetHeader("Accept")) {
		return true
	}
	return false
}

// IsDocumentFetch reports whether document fetch.
func IsDocumentFetch(c *Context) bool {
	return strings.EqualFold(c.GetHeader("Sec-Fetch-Dest"), "document") ||
		strings.EqualFold(c.GetHeader("Sec-Fetch-Mode"), "navigate")
}

func headerHasJSONMediaType(header string) bool {
	for mediaType := range headerMediaTypes(header) {
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
			return true
		}
	}
	return false
}

func headerHasHTMLMediaType(header string) bool {
	for mediaType := range headerMediaTypes(header) {
		if mediaType == "text/html" {
			return true
		}
	}
	return false
}

func headerMediaTypes(header string) map[string]struct{} {
	mediaTypes := make(map[string]struct{})
	for part := range strings.SplitSeq(header, ",") {
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && mediaType != "" {
			mediaTypes[strings.ToLower(mediaType)] = struct{}{}
		}
	}
	return mediaTypes
}

// PayRequired performs the pay required operation.
func (c *Context) PayRequired(message string, data gin.H) {
	c.response(http.StatusPaymentRequired, gin.H{
		KeyStatus:  http.StatusPaymentRequired,
		KeyMessage: message,
		KeyData:    data,
	})
}

// SendCookie performs the send cookie operation.
func (c *Context) SendCookie(key, value string, second int) error {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		key,
		xutil.Encrypt([]byte(viper.GetString("key")), value),
		second,
		"/",
		"",
		true,
		true,
	)
	return nil
}

// DecodeCookie decodes cookie.
func (c *Context) DecodeCookie(key string) (string, error) {
	cookie, err := c.Cookie(key)
	if err != nil {
		return "", errors.Join(err, ErrCookieDecode)
	}

	if cookie != "" {
		id := xutil.Decrypt([]byte(viper.GetString("key")), cookie)
		return id, nil
	}
	return "", nil
}

// GetUserID returns user id.
func (c *Context) GetUserID() uint {
	return c.GetUint("uid")
}

// LoginUser performs the login user operation.
func (c *Context) LoginUser() *db.User {
	u, ok := c.Get("user")
	if ok {
		user, valid := u.(*db.User)
		if valid {
			return user
		}
	}
	qu := db.GetUser(c.Request.Context(), c.GetUserID())
	c.SetUser(qu)
	return qu
}

// SetUser sets user.
func (c *Context) SetUser(u *db.User) {
	c.Set("user", u)
}

// SetAPIUser sets api user.
func (c *Context) SetAPIUser(u *APIUser) {
	c.Set("api_user", u)
}

// GetAPIUser returns api user.
func (c *Context) GetAPIUser() *APIUser {
	u, ok := c.Get("api_user")
	if ok {
		user, valid := u.(*APIUser)
		if valid {
			return user
		}
	}
	return nil
}

// IsWechatMiniProgram reports whether wechat mini program.
func (c *Context) IsWechatMiniProgram() bool {
	return strings.Contains(c.GetHeader("referer"), "https://servicewechat.com")
}

// GetWechatAppID returns wechat app id.
func (c *Context) GetWechatAppID() string {
	refererList := strings.Split(c.GetHeader("referer"), "/")
	appid := ""
	if len(refererList) > 3 {
		appid = refererList[3]
	}
	return appid
}

// IsDatabaseEnabled reports whether database enabled.
func (c *Context) IsDatabaseEnabled() bool {
	dbName := viper.GetString("db.database")
	return dbName != ""
}
