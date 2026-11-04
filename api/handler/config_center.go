package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/configformat"
)

const (
	configCenterEntry = "src/config-center/main.js"
	configAPIKeyRealm = "api"
)

const (
	keyConfigNamespace   = "namespace"
	keyConfigEnvironment = "environment"
	keyInheritanceChain  = "inheritance_chain"
	keyConfigReason      = "reason"
)

var errConfigInheritance = errors.New("resolve inherited configuration failed")

type configNamespaceForm struct {
	Name        string `json:"name" binding:"required"`
	Slug        string `json:"slug" binding:"required"`
	Description string `json:"description"`
	APIKey      string `json:"api_key"`
}

type configEnvironmentForm struct {
	Name        string `json:"name" binding:"required"`
	Slug        string `json:"slug" binding:"required"`
	Description string `json:"description"`
	ParentID    uint   `json:"parent_id"`
}

type configDraftForm struct {
	Config       json.RawMessage `json:"config" binding:"required"`
	Descriptions json.RawMessage `json:"descriptions"`
}

type configPublishForm struct {
	Reason         string `json:"reason" binding:"required,max=1000"`
	EnvironmentIDs []uint `json:"environment_ids" binding:"required,min=1,max=100"`
}

// ConfigCenterPage performs the config center page operation.
func ConfigCenterPage(c *base.Context) {
	cssFiles, jsFiles := c.GetCSSJsFiles(configCenterEntry)
	assetMode := viper.GetString("ui.assetMode")
	if assetMode == "" {
		assetMode = "embedded"
	}
	c.View("config-center.index", gin.H{
		"title":       "配置中心",
		"use_vite":    assetMode == "vite",
		"vite_origin": strings.TrimRight(viper.GetString("ui.viteDevOrigin"), "/"),
		"entry":       configCenterEntry,
		"css_files":   cssFiles,
		"js_files":    jsFiles,
	})
}

// ConfigCenterControl performs the config center control operation.
func ConfigCenterControl(r *base.Router) {
	r.GET("/namespaces", listConfigNamespaces)
	r.POST("/namespaces", createConfigNamespace)
	r.PUT("/namespaces/:namespace_id", updateConfigNamespace)
	r.DELETE("/namespaces/:namespace_id", deleteConfigNamespace)
	r.GET("/namespaces/:namespace_id/environments", listConfigEnvironments)
	r.POST("/namespaces/:namespace_id/environments", createConfigEnvironment)
	r.PUT("/namespaces/:namespace_id/environments/:id", updateConfigEnvironment)
	r.DELETE("/namespaces/:namespace_id/environments/:id", deleteConfigEnvironment)
	r.GET("/namespaces/:namespace_id/environments/:id/config", getConfigDraft)
	r.PUT("/namespaces/:namespace_id/environments/:id/config", saveConfigDraft)
	r.GET("/namespaces/:namespace_id/environments/:id/final", getFinalConfig)
	r.GET("/namespaces/:namespace_id/environments/:id/published", getPublishedConfig)
	r.GET("/namespaces/:namespace_id/environments/:id/releases", listConfigReleases)
	r.GET("/namespaces/:namespace_id/environments/:id/releases/:version", getConfigRelease)
	r.GET("/namespaces/:namespace_id/releases", listAllConfigReleases)
	r.POST("/namespaces/:namespace_id/publish", publishConfigs)
}

// ConfigCenterRuntimeControl performs the config center runtime control operation.
func ConfigCenterRuntimeControl(r *base.Router) {
	r.GET("/runtime/:namespace/:environment", getPublishedEnvironmentConfig)
}

func listConfigNamespaces(c *base.Context) {
	namespaces, err := db.ListConfigNamespaces(c.Request.Context())
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"namespaces": namespaces})
}

func createConfigNamespace(c *base.Context) {
	form := &configNamespaceForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("命名空间信息格式不正确", gin.H{KeyError: err.Error()})
		return
	}
	namespace, err := db.CreateConfigNamespace(c.Request.Context(), configNamespaceInput(form))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("namespace_id", namespace.ID).Msg("config namespace created")
	c.Success(gin.H{keyConfigNamespace: namespace})
}

func updateConfigNamespace(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	form := &configNamespaceForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("命名空间信息格式不正确", gin.H{KeyError: err.Error()})
		return
	}
	namespace, err := db.UpdateConfigNamespace(c.Request.Context(), namespaceID, configNamespaceInput(form))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("namespace_id", namespaceID).Msg("config namespace updated")
	c.Success(gin.H{keyConfigNamespace: namespace})
}

func deleteConfigNamespace(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	if err := db.DeleteConfigNamespace(c.Request.Context(), namespaceID); err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("namespace_id", namespaceID).Msg("config namespace deleted")
	c.Success(gin.H{"id": namespaceID})
}

func listConfigEnvironments(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	environments, err := db.ListConfigEnvironments(c.Request.Context(), namespaceID)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"environments": environments})
}

func createConfigEnvironment(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	form := &configEnvironmentForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("环境信息格式不正确", gin.H{KeyError: err.Error()})
		return
	}
	environment, err := db.CreateConfigEnvironment(c.Request.Context(), namespaceID, configEnvironmentInput(form))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("environment_id", environment.ID).Msg("config environment created")
	c.Success(gin.H{keyConfigEnvironment: environment})
}

func updateConfigEnvironment(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	form := &configEnvironmentForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("环境信息格式不正确", gin.H{KeyError: err.Error()})
		return
	}
	environment, err := db.UpdateConfigEnvironment(c.Request.Context(), namespaceID, id, configEnvironmentInput(form))
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("environment_id", id).Msg("config environment updated")
	c.Success(gin.H{keyConfigEnvironment: environment})
}

func deleteConfigEnvironment(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	if err := db.DeleteConfigEnvironment(c.Request.Context(), namespaceID, id); err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("environment_id", id).Msg("config environment deleted")
	c.Success(gin.H{"id": id})
}

func getConfigDraft(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	resolved, draft, descriptions, err := db.GetConfigDraft(c.Request.Context(), namespaceID, id)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	inherited, inheritedDescriptions, err := inheritedConfig(c, namespaceID, resolved.Environment.ParentID)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{
		keyConfigEnvironment:     resolved.Environment,
		keyInheritanceChain:      resolved.Chain,
		"draft":                  draft,
		"draft_descriptions":     descriptions,
		"inherited":              inherited,
		"inherited_descriptions": inheritedDescriptions,
		"final":                  resolved.Config,
		"final_descriptions":     resolved.Descriptions,
	})
}

func saveConfigDraft(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		int64(db.MaxConfigBytes+db.MaxConfigDescriptionBytes+2048),
	)
	form := &configDraftForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("配置内容格式不正确", gin.H{KeyError: err.Error()})
		return
	}
	descriptions := form.Descriptions
	if len(descriptions) == 0 {
		descriptions = json.RawMessage("{}")
	}
	resolved, err := db.SaveConfigDraft(c.Request.Context(), namespaceID, id, form.Config, descriptions)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().Uint("environment_id", id).Msg("config draft saved")
	inherited, inheritedDescriptions, err := inheritedConfig(c, namespaceID, resolved.Environment.ParentID)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{
		keyConfigEnvironment:     resolved.Environment,
		keyInheritanceChain:      resolved.Chain,
		"inherited":              inherited,
		"inherited_descriptions": inheritedDescriptions,
		"final":                  resolved.Config,
		"final_descriptions":     resolved.Descriptions,
	})
}

func getFinalConfig(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	resolved, err := db.ResolveConfigDraft(c.Request.Context(), namespaceID, id)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{
		keyConfigEnvironment: resolved.Environment,
		keyInheritanceChain:  resolved.Chain,
		"config":             resolved.Config,
		"descriptions":       resolved.Descriptions,
	})
}

func getPublishedConfig(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	published, err := db.LatestPublishedConfig(c.Request.Context(), namespaceID, id)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"release": published})
}

func getPublishedEnvironmentConfig(c *base.Context) {
	outputFormat, err := configformat.Parse(c.Query("format"))
	if err != nil {
		c.ErrorForm("输出格式不支持，可使用 json、yaml、toml、env 或 ini", gin.H{})
		return
	}
	namespace, environment, published, err := db.LatestPublishedConfigBySlugs(
		c.Request.Context(),
		c.Param("namespace"),
		c.Param("environment"),
		c.GetHeader("X-API-Key"),
	)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	setPublishedConfigHeaders(c, namespace.Slug, environment.Slug, published)
	if outputFormat == configformat.JSON {
		c.Success(gin.H{
			keyConfigNamespace: gin.H{
				"name": namespace.Name,
				"slug": namespace.Slug,
			},
			keyConfigEnvironment: gin.H{
				"name": environment.Name,
				"slug": environment.Slug,
			},
			"version":      published.Version,
			"batch_id":     published.BatchID,
			"reason":       published.Reason,
			"published_at": published.PublishedAt,
			"config":       published.Config,
			"descriptions": published.Descriptions,
		})
		return
	}
	output, err := configformat.Render(
		published.Config,
		map[string]string(published.Descriptions),
		outputFormat,
	)
	if err != nil {
		log.Ctx(c.Request.Context()).Error().Err(err).Msg("format published configuration failed")
		c.ErrorMessage("配置格式转换失败")
		return
	}
	c.Header(
		"Content-Disposition",
		"inline; filename=\""+namespace.Slug+"-"+environment.Slug+"-v"+
			strconv.FormatUint(published.Version, 10)+"."+outputFormat.Extension()+"\"",
	)
	c.Data(http.StatusOK, outputFormat.ContentType(), output)
}

func setPublishedConfigHeaders(c *base.Context, namespaceSlug, environmentSlug string, published *db.PublishedConfig) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Config-Namespace", namespaceSlug)
	c.Header("X-Config-Environment", environmentSlug)
	c.Header("X-Config-Version", strconv.FormatUint(published.Version, 10))
	c.Header("X-Config-Batch", published.BatchID)
	c.Header("Last-Modified", published.PublishedAt.UTC().Format(http.TimeFormat))
}

func listConfigReleases(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	releases, err := db.ListConfigReleases(c.Request.Context(), namespaceID, id)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"releases": releases})
}

func listAllConfigReleases(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	releases, err := db.ListAllConfigReleases(c.Request.Context(), namespaceID)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"releases": releases})
}

func getConfigRelease(c *base.Context) {
	namespaceID, id, ok := configResourceIDs(c)
	if !ok {
		return
	}
	version, err := strconv.ParseUint(c.Param("version"), 10, 64)
	if err != nil || version == 0 {
		c.ErrorForm("发布版本不正确", gin.H{})
		return
	}
	published, err := db.PublishedConfigVersion(c.Request.Context(), namespaceID, id, version)
	if err != nil {
		respondConfigError(c, err)
		return
	}
	c.Success(gin.H{"release": published})
}

func publishConfigs(c *base.Context) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return
	}
	form := &configPublishForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		c.ErrorForm("请选择需要发布的环境并填写发布原因", gin.H{KeyError: err.Error()})
		return
	}
	result, err := db.PublishConfigs(c.Request.Context(), namespaceID, db.ConfigPublishInput{
		EnvironmentIDs: form.EnvironmentIDs,
		Reason:         form.Reason,
	})
	if err != nil {
		respondConfigError(c, err)
		return
	}
	log.Ctx(c.Request.Context()).Info().
		Str("batch_id", result.BatchID).
		Int("environment_count", len(result.Releases)).
		Msg("config environments published")
	c.Success(gin.H{"publication": result})
}

func configEnvironmentID(c *base.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		c.ErrorForm("环境 ID 不正确", gin.H{})
		return 0, false
	}
	return uint(parsed), true
}

func configNamespaceID(c *base.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("namespace_id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		c.ErrorForm("命名空间 ID 不正确", gin.H{})
		return 0, false
	}
	return uint(parsed), true
}

func configResourceIDs(c *base.Context) (uint, uint, bool) {
	namespaceID, ok := configNamespaceID(c)
	if !ok {
		return 0, 0, false
	}
	environmentID, ok := configEnvironmentID(c)
	if !ok {
		return 0, 0, false
	}
	return namespaceID, environmentID, true
}

func configNamespaceInput(form *configNamespaceForm) db.ConfigNamespaceInput {
	return db.ConfigNamespaceInput{
		Name:        form.Name,
		Slug:        form.Slug,
		Description: form.Description,
		APIKey:      form.APIKey,
	}
}

func configEnvironmentInput(form *configEnvironmentForm) db.ConfigEnvironmentInput {
	return db.ConfigEnvironmentInput{
		Name:        form.Name,
		Slug:        form.Slug,
		Description: form.Description,
		ParentID:    form.ParentID,
	}
}

func inheritedConfig(c *base.Context, namespaceID, parentID uint) (map[string]any, db.ConfigDescriptions, error) {
	if parentID == 0 {
		return make(map[string]any), make(db.ConfigDescriptions), nil
	}
	resolved, err := db.ResolveConfigDraft(c.Request.Context(), namespaceID, parentID)
	if err != nil {
		return nil, nil, errors.Join(errConfigInheritance, err)
	}
	return resolved.Config, resolved.Descriptions, nil
}

func respondConfigError(c *base.Context, err error) {
	if respondConfigNamespaceError(c, err) || respondConfigEnvironmentError(c, err) {
		return
	}
	log.Ctx(c.Request.Context()).Error().Err(err).Msg("config center request failed")
	c.ErrorMessage("配置系统暂时不可用")
}

func respondConfigNamespaceError(c *base.Context, err error) bool {
	switch {
	case errors.Is(err, db.ErrConfigNamespaceNotFound):
		c.NotFound("命名空间不存在", gin.H{})
	case errors.Is(err, db.ErrConfigNamespaceConflict):
		c.Conflict("命名空间标识已存在", gin.H{})
	case errors.Is(err, db.ErrConfigNamespaceInvalid):
		c.ErrorForm("命名空间信息不合法", gin.H{keyConfigReason: err.Error()})
	case errors.Is(err, db.ErrConfigAPIKeyInvalid):
		c.ErrorForm("API Key 必须包含 32 至 256 个 URL 安全字符", gin.H{})
	case errors.Is(err, db.ErrConfigAPIKeyUnauthorized):
		c.UnauthorizedAPIKey(configAPIKeyRealm, "API Key 无效", gin.H{})
	case errors.Is(err, db.ErrConfigEncryptionRequired):
		c.ErrorMessage("配置加密未启用，无法安全保存 API Key")
	default:
		return false
	}
	return true
}

func respondConfigEnvironmentError(c *base.Context, err error) bool {
	switch {
	case errors.Is(err, db.ErrConfigEnvironmentNotFound):
		c.NotFound("配置环境不存在", gin.H{})
	case errors.Is(err, db.ErrConfigReleaseNotFound):
		c.NotFound("该环境尚未发布", gin.H{})
	case errors.Is(err, db.ErrConfigReleaseInvalid):
		c.ErrorForm("请填写 1 至 1000 个字符的发布原因", gin.H{})
	case errors.Is(err, db.ErrConfigEnvironmentConflict):
		c.Conflict("环境标识已存在", gin.H{})
	case errors.Is(err, db.ErrConfigEnvironmentInUse):
		c.Conflict("请先调整子环境的继承关系", gin.H{})
	case errors.Is(err, db.ErrConfigInvalid):
		c.ErrorForm("配置内容不合法，请检查字段类型和结构", gin.H{keyConfigReason: err.Error()})
	case errors.Is(err, db.ErrConfigEnvironmentInvalid):
		c.ErrorForm("环境信息或继承关系不合法", gin.H{keyConfigReason: err.Error()})
	default:
		return false
	}
	return true
}
