package handler

import (
	"errors"
	"net/http"

	"github.com/caitun/inferkit/api/base"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type reviewErrorRule struct {
	err     error
	message string
	status  int
}

// Review and maintenance writes share response shape and safe error reporting.
func reviewRequestError(c *base.Context, err error, fallback string, rules []reviewErrorRule) {
	for _, rule := range rules {
		if errors.Is(err, rule.err) {
			rejectReviewRequest(c, rule.status, rule.message)
			return
		}
	}
	log.Ctx(c.Request.Context()).Error().Err(err).Msg("image review operation failed")
	rejectReviewRequest(c, http.StatusServiceUnavailable, fallback)
}

func rejectReviewRequest(c *base.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{base.KeyStatus: status, base.KeyMessage: message, base.KeyData: gin.H{}})
}
