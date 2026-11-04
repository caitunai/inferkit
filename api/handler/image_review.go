package handler

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagereview"
)

const imageReviewEntry = "src/image-review/main.js"

// ImageReviewRepository exposes only the management page's persistence needs.
type ImageReviewRepository interface {
	List(context.Context, db.ImageQuery) (db.ImagePage, error)
	Get(context.Context, uint) (*db.ImagePrediction, error)
	Annotate(context.Context, uint, uint64, string, string) error
}

// ImageReviewPage serves either the filtered gallery or single-image annotation view.
func ImageReviewPage(review bool) base.HandlerFunc {
	return func(c *base.Context) {
		data := pageData(c, imageReviewEntry, "识别结果与人工标注 · InferKit")
		data["review"] = review
		data["labels"] = imagereview.Labels()
		c.View("image-review.index", data)
	}
}

// ImageReviewControl registers protected management endpoints under /image-review/api.
func ImageReviewControl(r *base.Router, store ImageReviewRepository) {
	h := imageReviewHandler{store: store}
	r.GET("/results", h.list)
	r.GET("/results/:id", h.get)
	r.PUT("/results/:id/annotation", h.annotate)
}

type imageReviewHandler struct {
	store ImageReviewRepository
}

func (h imageReviewHandler) list(c *base.Context) {
	query := db.DefaultImageQuery()
	if err := c.ShouldBindQuery(&query); err != nil {
		imageReviewError(c, errors.Join(db.ErrImageQuery, err))
		return
	}
	page, err := h.store.List(c.Request.Context(), query)
	if err != nil {
		imageReviewError(c, err)
		return
	}
	c.Success(gin.H{"page": page})
}

func (h imageReviewHandler) get(c *base.Context) {
	id, err := imageReviewID(c)
	if err != nil {
		imageReviewError(c, err)
		return
	}
	record, err := h.store.Get(c.Request.Context(), id)
	if err != nil {
		imageReviewError(c, err)
		return
	}
	c.Success(gin.H{"record": record})
}

func imageReviewID(c *base.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize-1)
	if err != nil || id == 0 {
		return 0, errors.Join(db.ErrImageQuery, err)
	}
	return uint(id), nil
}

type imageAnnotation struct {
	Revision *uint64 `json:"revision"`
	Label    string  `json:"label"`
}

func (h imageReviewHandler) annotate(c *base.Context) {
	if !imageReviewWriteAllowed(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{base.KeyStatus: http.StatusForbidden, base.KeyMessage: "review request rejected", base.KeyData: gin.H{}})
		return
	}
	id, err := imageReviewID(c)
	if err != nil {
		imageReviewError(c, err)
		return
	}
	var form imageAnnotation
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err = json.UnmarshalRead(c.Request.Body, &form); err != nil || form.Revision == nil || form.Label == "" {
		imageReviewError(c, db.ErrImageQuery)
		return
	}
	reviewer := c.GetString(gin.AuthUserKey)
	if err = h.store.Annotate(c.Request.Context(), id, *form.Revision, form.Label, reviewer); err != nil {
		imageReviewError(c, err)
		return
	}
	c.Success(gin.H{"saved": true})
}

func imageReviewWriteAllowed(c *base.Context) bool {
	if c.GetHeader("X-Requested-With") != "XMLHttpRequest" || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return false
	}
	origin := c.GetHeader("Origin")
	return origin == "" || origin == c.Origin()
}

func imageReviewError(c *base.Context, err error) {
	status, message := http.StatusServiceUnavailable, "识别记录服务暂不可用"
	switch {
	case errors.Is(err, db.ErrImageQuery):
		status, message = http.StatusBadRequest, "筛选条件或标注参数不合法"
	case errors.Is(err, db.ErrImageNotFound):
		status, message = http.StatusNotFound, "识别记录不存在"
	case errors.Is(err, db.ErrImageConflict):
		status, message = http.StatusConflict, "该记录已被其他人修改，请重新加载后再标注"
	default:
		log.Ctx(c.Request.Context()).Error().Err(err).Msg("image review operation failed")
	}
	c.AbortWithStatusJSON(status, gin.H{base.KeyStatus: status, base.KeyMessage: message, base.KeyData: gin.H{}})
}
