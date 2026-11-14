package handler

import (
	"context"
	"encoding/json/v2"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/reclassify"
)

// ReclassificationController exposes maintenance operations without database access.
type ReclassificationController interface {
	Status() reclassify.View
	Start(context.Context, reclassify.Start) (reclassify.View, error)
	Control(context.Context, string) (reclassify.View, error)
}

const imageReclassificationEntry = "src/image-review/reclassify-main.js"

// ImageReclassificationPage renders the authenticated maintenance console.
func ImageReclassificationPage() base.HandlerFunc {
	return func(c *base.Context) {
		data := pageData(c, imageReclassificationEntry, "历史图片重新分类 · InferKit")
		c.View("image-reclassification.index", data)
	}
}

// ImageReclassificationControl registers status and explicit job controls.
func ImageReclassificationControl(r *base.Router, service ReclassificationController) {
	h := reclassificationHandler{service: service}
	r.GET("", h.status)
	r.POST("/start", h.start)
	r.POST("/control", h.control)
}

type reclassificationHandler struct{ service ReclassificationController }

func (h reclassificationHandler) status(c *base.Context) {
	if h.service == nil {
		reclassificationError(c, reclassify.ErrUnavailable)
		return
	}
	c.Success(gin.H{"task": h.service.Status()})
}

func (h reclassificationHandler) authorize(c *base.Context) bool {
	if h.service == nil {
		reclassificationError(c, reclassify.ErrUnavailable)
		return false
	}
	if !imageReviewWriteAllowed(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{base.KeyStatus: http.StatusForbidden, base.KeyMessage: "重分类请求被拒绝", base.KeyData: gin.H{}})
		return false
	}
	return true
}

func (h reclassificationHandler) start(c *base.Context) {
	if !h.authorize(c) {
		return
	}
	var form reclassify.Start
	if !readReclassification(c, &form) {
		return
	}
	if !form.Confirm || len(form.ModelSHA256) != 64 || form.IntervalMS < reclassify.MinIntervalMS || form.IntervalMS > reclassify.MaxIntervalMS {
		c.BadRequest("请确认覆盖预测，并设置 0–60000 毫秒的处理间隔", gin.H{})
		return
	}
	view, err := h.service.Start(c.Request.Context(), form)
	reclassificationAccepted(c, view, err)
}

func (h reclassificationHandler) control(c *base.Context) {
	if !h.authorize(c) {
		return
	}
	var form struct {
		Action string `json:"action"`
	}
	if !readReclassification(c, &form) {
		return
	}
	view, err := h.service.Control(c.Request.Context(), form.Action)
	reclassificationAccepted(c, view, err)
}

func readReclassification(c *base.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := json.UnmarshalRead(c.Request.Body, target); err != nil {
		c.BadRequest("任务参数不合法", gin.H{})
		return false
	}
	return true
}

func reclassificationAccepted(c *base.Context, view reclassify.View, err error) {
	if err != nil {
		reclassificationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{base.KeyStatus: 0, base.KeyMessage: "accepted", base.KeyData: gin.H{"task": view}})
}

func reclassificationError(c *base.Context, err error) {
	reviewRequestError(c, err, "任务不可用，请检查持久卷、连接池配置和服务日志", []reviewErrorRule{
		{err: reclassify.ErrBusy, status: http.StatusConflict, message: "已有任务运行或由其它容器持有，请勿重复启动"},
		{err: reclassify.ErrModel, status: http.StatusConflict, message: "模型版本已变化，请刷新页面；旧任务需取消后重新开始"},
		{err: reclassify.ErrState, status: http.StatusConflict, message: "任务状态已变化，请刷新进度后重试"},
		{err: reclassify.ErrUnavailable, status: http.StatusServiceUnavailable, message: "维护功能不可用，请检查模型、持久目录及数据库连接池（至少 2 个连接）"},
	})
}
